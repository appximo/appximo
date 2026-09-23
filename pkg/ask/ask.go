package ask

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/appximo/appximo/pkg/aigen"
)

// Executor is the data seam: every read a plan needs, executed by the caller
// with the ENGINE's own query builders and scoped by the asking role. The
// /api/ask handler implements it over query.BuildQuery / BuildAggregate +
// the tenant pool; tests implement it in memory.
type Executor interface {
	// Aggregate runs GET /api/{resource}/aggregate semantics with params
	// (count/sum/…/group_by/filter[...]) and returns the rows the engine
	// would serialize (one row without group_by).
	Aggregate(ctx context.Context, resource string, params url.Values) ([]map[string]any, error)
	// List runs GET /api/{resource} semantics (filter[...], search, fields,
	// per_page, sort) and returns the page plus the total matching count.
	List(ctx context.Context, resource string, params url.Values) (rows []map[string]any, total int64, err error)
}

// ExecError carries the engine's status for a failed read: a 400 (a value the
// builder rejected) feeds the model's correction round; a 403 (a field the
// role may not read) is answered to the owner as such.
type ExecError struct {
	Status int
	Msg    string
}

func (e *ExecError) Error() string { return e.Msg }

// Deps is everything Answer needs.
type Deps struct {
	Vocab *Vocabulary
	Model aigen.ModelClient // nil = the model may not be called (see ModelOff)
	Exec  Executor
	Now   time.Time
	// ModelName is used for the cost estimate.
	ModelName string
	// ModelOff says WHY Model is nil: "disabled" (no key) or "capped" (the
	// daily spend cap, VOZ-SIN-IA-S1) or "minute" (the per-minute cap). The
	// parser and the cache answer regardless; only a question they cannot
	// solve gets the corresponding reply.
	ModelOff string
	// Cache is the plan cache (nil = none); CacheScope is the tenant+role
	// (+ vocabulary fingerprint) prefix of its READ keys — a plan never
	// crosses tenants or roles, nor a vocabulary change (VOZ-AHORRO-S2: a
	// synonym declared after a «no entendí» cures it). CacheScopeWrite is
	// the tenant+role+USER prefix of the WRITE keys: a write plan is an
	// order, and an order is personal — one user's dictated sentence never
	// seeds another user's pending. Empty = write plans are not cached.
	Cache           *PlanCache
	CacheScope      string
	CacheScopeWrite string
	// NoParser skips the deterministic parser (tests of the model path).
	NoParser bool
	// Trace appends who answered, the latency, the cost and — when the parser
	// fell through — why, to the human TEXT of the reply (VOZ-TRAZABILIDAD-S1).
	// Never to Speech. For whoever administers; off by default.
	Trace bool

	// ── writes (VOZ-ESCRITURAS-S1) ──
	// Write executes a CONFIRMED create/update through the engine; nil = the
	// channel only reads (a write plan is refused in words). Pending holds
	// the writes waiting for confirmation; PendingKey is the asking identity
	// (tenant|user) — one pending per key, never shared.
	Write      Writer
	Pending    *PendingStore
	PendingKey string
	// Conflicts (MOTOR-AGENDA-S1) answers the collision check of a write on
	// a range with a no-overlap rule, before the owner confirms; nil = no
	// pre-check (the database constraint still decides at write time).
	Conflicts ConflictChecker
}

// Result is the reply, composed by the engine.
type Result struct {
	// Kind: answer | unclear | ambiguous | not_found | write_refused |
	// forbidden | unavailable | disabled | invalid.
	Kind string `json:"kind"`
	// Text is the reply in Telegram HTML; Speech the same in plain words for
	// a screen reader / Siri; Headline the one-line version (the number first).
	Text     string `json:"text"`
	Speech   string `json:"speech"`
	Headline string `json:"headline"`
	// Display is the reply as READ ALOUD: the Speech, plus — with tracing on —
	// the cost line ("Costo: parser, 5 ms, US$ 0") as its last line, so a
	// shortcut that speaks display hears the same content in both states and
	// only that line changes. Speech never carries the trace; Text (Telegram
	// HTML) carries it as the ⚙︎ line.
	Display string `json:"display"`
	// Number is the engine's figure when the answer has one (count / sum…).
	Number *float64 `json:"number,omitempty"`
	// Understood is the plan in the owner's words ("citas · hoy · optometra: Ana Gómez").
	Understood string `json:"understood,omitempty"`
	// Plan is the validated plan that ran (nil for non-answers).
	Plan *Plan `json:"plan,omitempty"`
	// Groups carries a group_by answer's rows for the image (label, count/value).
	Groups []Group `json:"groups,omitempty"`
	// Accounting: model tokens, approximate USD, wall time.
	Usage     aigen.Usage `json:"usage"`
	CostUSD   float64     `json:"cost_usd"`
	ModelMS   int64       `json:"model_ms"`
	TotalMS   int64       `json:"total_ms"`
	Corrected bool        `json:"corrected,omitempty"`
	// Source says who produced the plan: "parser" (no model, no cache),
	// "cache" (a remembered plan), "model" (a paid call), or "" (no plan).
	Source string `json:"source,omitempty"`
	// Fallback is the parser's reason for NOT answering (its raw code, e.g.
	// "unknown word: vendimos") whenever the question went past it — the
	// datum that says what the parser should learn next. FallbackES is the
	// same in the owner's words.
	Fallback   string `json:"fallback,omitempty"`
	FallbackES string `json:"fallback_es,omitempty"`
	// Detail is the engine-side reason for the log (never the owner).
	Detail string `json:"-"`
	// Pending is the write waiting for the owner (kinds confirm | ask_field
	// | ambiguous | not_found with a pending id); Written the rows a
	// confirmed write produced (kind written).
	Pending *Pending  `json:"pending,omitempty"`
	Written []Written `json:"written,omitempty"`
}

// Group is one row of a grouped answer.
type Group struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Text  string  `json:"text"` // the value formatted (money, count)
}

// Answer runs the whole loop for one question: translate → validate →
// resolve names → execute → compose. It never panics on a model failure: the
// Result says `unavailable` and the caller degrades to the fixed commands.
func Answer(ctx context.Context, d Deps, question string) Result {
	start := time.Now()
	res := answer(ctx, d, question)
	res.TotalMS = time.Since(start).Milliseconds()
	res.CostUSD = res.Usage.CostUSD(d.ModelName)
	if res.Fallback != "" {
		res.FallbackES = fallbackES(res.Fallback)
	}
	if res.Speech == "" {
		res.Speech = Speech(res.Text) // the voice never carries the trace
	}
	// Display is the reply as it is READ ALOUD, always: the spoken form and —
	// only when tracing is on — the cost line after it, itself speakable. A
	// shortcut that speaks display (and not speech, so the answer is never
	// heard twice) hears the same content with the trace on or off; the ONE
	// thing that changes is that last line (APP-AGENDA, decided with Miguel's
	// own Siri shortcut). Screens (the Siri card, the drill) show the same.
	res.Display = res.Speech
	if d.Trace && res.Text != "" {
		res.Text += "\n" + TraceLine(res)
		res.Display = strings.TrimSpace(res.Display + "\n" + SpokenTrace(res))
	}
	return res
}

// TraceLine words who answered, in how long, at what cost — and why the
// parser passed — for the administrator's eyes: "⚙︎ parser · 2 ms · US$ 0",
// "⚙︎ modelo · 1,1 s · US$ 0,0029 · al modelo porque: palabra fuera del
// schema «vendimos»".
func TraceLine(r Result) string {
	who := map[string]string{"parser": "parser", "cache": "caché", "model": "modelo", "confirm": "confirmación (sin modelo)"}[r.Source]
	if who == "" {
		who = "sin plan"
	}
	lat := fmt.Sprintf("%d ms", r.TotalMS)
	if r.TotalMS >= 1000 {
		lat = strings.Replace(fmt.Sprintf("%.1f s", float64(r.TotalMS)/1000), ".", ",", 1)
	}
	cost := "US$ 0"
	if r.CostUSD > 0 {
		cost = strings.Replace(fmt.Sprintf("US$ %.4f", r.CostUSD), ".", ",", 1)
	}
	line := fmt.Sprintf("<i>⚙︎ %s · %s · %s", who, lat, cost)
	if r.FallbackES != "" && r.Source != "parser" {
		// The same wording for a model answer and its cached repeat: what the
		// PARSER could not do is the datum, wherever the plan came from.
		line += " · el parser pasó: " + esc(r.FallbackES)
	}
	return line + "</i>"
}

// fallbackES puts the parser's reason code in the owner's words.
func fallbackES(code string) string {
	switch {
	case strings.HasPrefix(code, "unknown word: "):
		return "palabra fuera del schema «" + strings.TrimPrefix(code, "unknown word: ") + "»"
	case code == "no resource named":
		return "no nombra ningún recurso del schema"
	case strings.HasPrefix(code, "two resources: "):
		return "nombra dos recursos (" + strings.TrimPrefix(code, "two resources: ") + ")"
	case strings.HasPrefix(code, "two operations: "):
		return "dos operaciones (" + strings.TrimPrefix(code, "two operations: ") + ")"
	case code == "two periods":
		return "dos períodos"
	case code == "two names":
		return "dos nombres propios"
	case strings.HasPrefix(code, "name could match "):
		return "el nombre podría ir en " + strings.ReplaceAll(strings.TrimPrefix(code, "name could match "), " or ", " o en ")
	case code == "name with no place to match":
		return "un nombre propio sin dónde casarlo"
	case strings.HasPrefix(code, "two values for "):
		return "dos valores para " + strings.TrimPrefix(code, "two values for ")
	case strings.HasPrefix(code, "value "):
		return "un valor de otro recurso (" + strings.TrimPrefix(code, "value ") + ")"
	case code == "no amount field", strings.HasPrefix(code, "two amount fields"):
		return "no sé qué monto sumar"
	case strings.HasPrefix(code, "write verb: "):
		return "verbo de escritura «" + strings.TrimPrefix(code, "write verb: ") + "» (lo planea el modelo)"
	case strings.HasPrefix(code, "invalid: "):
		return "plan inválido: " + strings.TrimPrefix(code, "invalid: ")
	case code == "empty":
		return "pregunta vacía"
	}
	return code
}

// Redact replaces, in the question text, every proper name the plan carries
// as a `match` with [nombre] — the history keeps the SHAPE the parser needs
// to learn from, not the person (HistoryText=redacted).
func Redact(question string, p *Plan) string {
	if p == nil {
		return question
	}
	if p.IsWrite() {
		// A write's text IS the data (a title, a note, a name): the history
		// keeps only the shape — kind, resource, the fields touched.
		return "[" + p.Kind + " " + p.Resource + ": " + strings.Join(sortedKeys(p.Data), ", ") + "]"
	}
	out := question
	for _, f := range p.Filters {
		if f.Match == "" {
			continue
		}
		out = replaceFold(out, f.Match, "[nombre]")
	}
	return out
}

// replaceFold replaces needle in s ignoring case and accents, keeping the
// rest of s intact.
func replaceFold(s, needle, repl string) string {
	ns := normalize(needle)
	if ns == "" {
		return s
	}
	words := strings.Fields(s)
	nw := strings.Fields(ns)
	for i := 0; i+len(nw) <= len(words); i++ {
		ok := true
		for k := range nw {
			if normalize(words[i+k]) != nw[k] {
				ok = false
				break
			}
		}
		if ok {
			words = append(append(append([]string{}, words[:i]...), repl), words[i+len(nw):]...)
			i += 0
		}
	}
	return strings.Join(words, " ")
}

func answer(ctx context.Context, d Deps, question string) Result {
	question = strings.TrimSpace(question)
	if question == "" {
		return Result{Kind: "invalid", Text: "No llegó ninguna pregunta.", Headline: "Sin pregunta"}
	}
	if d.Vocab == nil || d.Vocab.Len() == 0 {
		return Result{Kind: "forbidden", Headline: "Nada que consultar",
			Text: "Tu rol no puede leer ningún recurso, así que no hay nada que preguntar."}
	}

	// 0. A write waiting for this owner: the message is first read as its
	// answer (yes / no / a value / a pick). Anything that is not one cancels
	// the pending — an ambiguous yes never executes — and the message is
	// then processed as a new question, saying so.
	cancelled := false
	if d.Pending != nil && d.PendingKey != "" {
		if pend := d.Pending.Get(d.PendingKey); pend != nil {
			r, handled := continuePending(ctx, d, pend, question)
			if handled {
				r.Source = "confirm"
				return r
			}
			cancelled = true
		}
		// A bare yes/no with nothing pending never reaches the model.
		if IsYes(question) || IsNo(question) {
			return Result{Kind: "invalid", Headline: "Nada pendiente", Source: "parser",
				Text: "No hay ninguna escritura pendiente que confirmar o cancelar."}
		}
	}
	out := answerQuestion(ctx, d, question, cancelled)
	if cancelled && out.Detail != "discard: stray_confirmation" {
		out.Text = "<i>Cancelé la escritura que estaba pendiente. No escribí nada.</i>\n" + out.Text
	}
	return out
}

func answerQuestion(ctx context.Context, d Deps, question string, cancelled bool) Result {

	// 1. The deterministic parser — no model, no cache, microseconds. It
	// answers only when SURE (parser.go); otherwise it says why, for the log.
	var p Plan
	var tr Translation
	source := ""
	parseReason := ""
	if !d.NoParser {
		if pr := Parse(question, d.Vocab); pr.Sure {
			if pr.Discard != "" {
				// Sure it is NOT a data question (Part C): answered here, at
				// zero cost — never cached, never billed.
				return discardResult(d, pr, question, cancelled)
			}
			p, source = pr.Plan, "parser"
		} else {
			parseReason = pr.Reason
		}
	}
	// 2. The plan cache: the same question, tenant and role → the plan the
	// model produced before; the data is recomputed below. A WRITE plan is
	// remembered under the user's own scope (VOZ-AHORRO-S2 Part B): the plan
	// — never the result — and everything it needs (names, the row, the
	// time tokens, the required fields) is re-prepared against the database
	// of the moment before any confirmation is asked.
	key, wkey := "", ""
	if source == "" && d.Cache != nil {
		key = Key(d.CacheScope, question)
		if cp, _, ok := d.Cache.Get(key); ok {
			p, source = cp, "cache"
		} else if d.CacheScopeWrite != "" {
			wkey = Key(d.CacheScopeWrite, question)
			if cp, _, ok := d.Cache.Get(wkey); ok {
				p, source = cp, "cache"
			}
		}
	}
	// 3. The model — only for what neither could solve, and only when allowed.
	base := Result{}
	if source == "" {
		if d.Model == nil {
			r := modelOffResult(d, parseReason)
			r.Fallback = parseReason
			return r
		}
		mstart := time.Now()
		var err error
		tr, err = Translate(ctx, d.Model, d.Vocab, question, d.Now)
		base = Result{Usage: tr.Usage, ModelMS: time.Since(mstart).Milliseconds(), Corrected: tr.Corrected, Source: "model"}
		if err != nil {
			base.Kind = "unavailable"
			base.Fallback = parseReason
			base.Detail = err.Error()
			base.Headline = "El modelo no respondió"
			base.Text = "⚠️ No pude pensar la pregunta ahora (el modelo no respondió a tiempo). Los comandos fijos siguen funcionando: <b>resumen</b>, <b>estado</b>, <b>ayuda</b>."
			return base
		}
		p, source = tr.Plan, "model"
		if d.Cache != nil {
			switch {
			case p.IsWrite() || p.Kind == "write":
				if wkey != "" {
					d.Cache.Put(wkey, p, "model")
				}
			case key != "":
				d.Cache.Put(key, p, "model")
			}
		}
	}
	base.Source = source
	if parseReason != "" {
		base.Detail = "parser: " + parseReason
		base.Fallback = parseReason
	}
	switch p.Kind {
	case "write":
		base.Kind = "write_refused"
		base.Detail = "write intent: " + p.Reason
		if d.Write != nil && d.Vocab.Writable() {
			base.Headline = "Eso no lo hago por voz"
			base.Text = "🔒 Por acá puedo <b>crear</b> y <b>cambiar</b> datos (siempre confirmando antes), pero no borrar, enviar ni otras acciones. " + writable(d.Vocab)
		} else {
			base.Headline = "Por acá solo leo"
			base.Text = "🔒 Por acá solo <b>leo</b>. " + askable(d.Vocab)
		}
		return base
	case "create", "update":
		r := prepareWrite(ctx, d, p, question)
		r.Usage, r.ModelMS, r.Corrected, r.Source, r.Fallback = base.Usage, base.ModelMS, base.Corrected, source, base.Fallback
		if r.Plan == nil {
			r.Plan = &p
		}
		if base.Detail != "" && r.Detail == "" {
			r.Detail = base.Detail
		}
		return r
	case "unclear":
		base.Kind = "unclear"
		base.Headline = "No entendí"
		base.Detail = strings.TrimSpace(base.Detail + " | unclear: " + p.Reason + " | " + tr.FailReason)
		base.Text = "🤔 <b>No entendí</b> la pregunta" + reasonHint(p.Reason) + ". " + askable(d.Vocab)
		return base
	}
	base.Plan = &p

	// Proper names → what exists.
	resolved, nameRes, ok := resolveNames(ctx, d, p)
	if !ok {
		nameRes.Usage, nameRes.ModelMS, nameRes.Corrected, nameRes.Plan, nameRes.Source, nameRes.Fallback = base.Usage, base.ModelMS, base.Corrected, &p, source, base.Fallback
		return nameRes
	}
	said := nameRes.Understood // "Entendí «Gomes» como Ana Gómez."

	out := execute(ctx, d, resolved)
	out.Usage, out.ModelMS, out.Corrected, out.Plan, out.Source, out.Fallback = base.Usage, base.ModelMS, base.Corrected, &p, source, base.Fallback
	if base.Detail != "" && out.Detail == "" {
		out.Detail = base.Detail
	}
	if said != "" && out.Kind == "answer" {
		out.Text = said + "\n" + out.Text
	}
	return out
}

// discardResult words a sentence the parser is SURE is not a data question
// (Part C): a stray answer to a confirmation, a greeting, a help request, a
// bare name. Zero cost, source "parser", kind "help" for the two that ask
// for guidance and "unclear" for the two that are non-answers.
func discardResult(d Deps, pr ParseResult, question string, cancelled bool) Result {
	r := Result{Source: "parser", Detail: "discard: " + pr.Discard, Plan: &pr.Plan}
	guide := askable(d.Vocab)
	if d.Write != nil && d.Vocab.Writable() {
		guide += " " + writable(d.Vocab)
	}
	switch pr.Discard {
	case "stray_confirmation":
		r.Kind = "unclear"
		r.Headline = "Eso no fue un sí"
		if cancelled {
			r.Text = "👌 Cancelé la escritura que estaba pendiente: «" + esc(firstWords(question, 3)) + "…» no es un <b>sí</b> claro, así que no escribí nada. Si querés cambiar algo, decime la orden completa con el cambio (por ejemplo «anotá … para el viernes»)."
		} else {
			r.Text = "🤔 Eso suena a la respuesta a una confirmación, pero no hay ninguna escritura pendiente (si había una, ya se canceló). Decime la orden completa otra vez."
		}
	case "greeting":
		r.Kind = "help"
		r.Headline = "¡Hola!"
		r.Text = "👋 ¡Hola! Preguntame con tus palabras («cuántas órdenes hay hoy») o pedime que anote algo. " + guide
	case "help":
		// EXAMPLES derived from the schema — resources, states, declared
		// aliases, flags, ranges — split into what the parser settles for
		// free and what the model must think (VOZ-19). Never a hand-written
		// list, so it cannot go stale; the speech is composed apart, in short
		// sentences with no symbols or prices, for a voice assistant.
		r.Kind = "help"
		r.Headline = "Qué puedo hacer"
		r.Text, r.Speech = HelpExamples(d.Vocab, d.Write != nil && d.Vocab.Writable())
	default: // bare_name
		r.Kind = "unclear"
		r.Headline = "¿Qué querés saber?"
		r.Text = "🤔 «" + esc(question) + "» parece un nombre, pero no me dijiste qué querés saber. Probá «las órdenes de " + esc(question) + "» o «cuántas … tiene " + esc(question) + "». " + guide
	}
	return r
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		f = f[:n]
	}
	return strings.Join(f, " ")
}

// writable words what the role CAN write, for the refusals.
func writable(v *Vocabulary) string {
	c, u := v.CreatableNames(), v.UpdatableNames()
	var parts []string
	if len(c) > 0 {
		parts = append(parts, "crear: "+strings.Join(firstN(c, 8), ", "))
	}
	if len(u) > 0 {
		parts = append(parts, "cambiar: "+strings.Join(firstN(u, 8), ", "))
	}
	return "Puedo " + strings.Join(parts, "; ") + "."
}

// askable words what the role CAN ask, for the non-answers.
func askable(v *Vocabulary) string {
	names := v.ResourceNames()
	if len(names) > 12 {
		names = append(names[:12], "…")
	}
	return "Puedo contar, listar o sumar sobre: " + strings.Join(names, ", ") + "."
}

func reasonHint(r string) string {
	r = strings.TrimSpace(r)
	if r == "" {
		return ""
	}
	if rs := []rune(r); len(rs) > 160 {
		r = string(rs[:160]) + "…"
	}
	return " (" + esc(r) + ")"
}

// resolveNames replaces every `match` filter by the id/value of the ONE row
// it refers to, or stops with the question/none reply. Returns the resolved
// plan, a Result holding the "Entendí … como …" line (in Understood) or the
// stop reply, and ok=false when it stopped.
func resolveNames(ctx context.Context, d Deps, p Plan) (Plan, Result, bool) {
	res := d.Vocab.Resource(p.Resource)
	// Work on a COPY of the filters: the caller keeps the unresolved plan (the
	// `match` as said) for the cache, the reply's `plan` and the history's
	// redaction — mutating the shared backing array turned a cached name into
	// a cached id (found by the history redaction test).
	p.Filters = append([]Filter(nil), p.Filters...)
	var said []string
	for i, f := range p.Filters {
		if f.Match == "" {
			continue
		}
		fd := res.Field(f.Field)
		target := res
		targetName := p.Resource
		if fd.Relation != "" {
			target = d.Vocab.Resource(fd.Relation)
			targetName = fd.Relation
		}
		cands, err := fetchCandidates(ctx, d, target, fd, f.Match)
		if err != nil {
			return p, execFailure(err), false
		}
		dec := Decide(Match(f.Match, cands, nil))
		switch dec.Kind {
		case "one":
			if fd.Relation != "" {
				p.Filters[i] = Filter{Field: f.Field, Op: "eq", Value: dec.Chosen.Value}
			} else {
				p.Filters[i] = Filter{Field: f.Field, Op: "eq", Value: dec.Chosen.Value}
			}
			if normalize(dec.Chosen.Label) != normalize(f.Match) {
				said = append(said, fmt.Sprintf("Entendí «%s» como <b>%s</b>.", esc(f.Match), esc(dec.Chosen.Label)))
			} else {
				said = append(said, fmt.Sprintf("%s: <b>%s</b>.", esc(singular(targetName)), esc(dec.Chosen.Label)))
			}
		case "several":
			return p, Result{Kind: "ambiguous", Headline: "¿Cuál?",
				Text: fmt.Sprintf("🤔 Hay varios %s que se parecen a «%s». ¿Cuál?\n%s\n\nRepetí la pregunta con el nombre completo.", esc(targetName), esc(f.Match), options(dec.Options))}, false
		case "maybe":
			return p, Result{Kind: "not_found", Headline: "No encuentro «" + f.Match + "»",
				Text: fmt.Sprintf("🤷 No encuentro ningún %s que se llame «%s». ¿Quisiste decir?\n%s\n\nRepetí la pregunta con ese nombre.", esc(singular(targetName)), esc(f.Match), options(dec.Options))}, false
		default:
			return p, Result{Kind: "not_found", Headline: "No encuentro «" + f.Match + "»",
				Text: fmt.Sprintf("🤷 No encuentro ningún %s que se llame «%s». Revisá el nombre y volvé a preguntar.", esc(singular(targetName)), esc(f.Match))}, false
		}
	}
	return p, Result{Understood: strings.Join(said, " ")}, true
}

func options(cs []Candidate) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString("• " + esc(c.Label) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// fetchCandidates pulls the rows a dictated name could mean, through the
// engine's own ?search= (RBAC-scoped like any list), using short unaccented
// and accented prefixes of each token — then labels each row. For a relation
// field the candidates are rows of the TARGET resource (value = id); for a
// text field, the distinct values of that field on the resource itself.
func fetchCandidates(ctx context.Context, d Deps, target *Resource, fd *Field, said string) ([]Candidate, error) {
	labelFields := target.LabelFields()
	fieldsParam := "id"
	if fd.Relation == "" {
		labelFields = []string{fd.Name}
	}
	if len(labelFields) > 0 {
		fieldsParam += "," + strings.Join(labelFields, ",")
	}
	seen := map[string]bool{}
	var out []Candidate
	for _, term := range SearchTerms(said) {
		params := url.Values{"search": {term}, "per_page": {"100"}, "fields": {fieldsParam}}
		rows, _, err := d.Exec.List(ctx, target.Name, params)
		if err != nil {
			var ee *ExecError
			// A resource whose readable fields include no text (search
			// unsupported) simply has no candidates.
			if errors.As(err, &ee) && ee.Status == 400 {
				return nil, nil
			}
			return nil, err
		}
		for _, row := range rows {
			label := labelOf(row, labelFields)
			if label == "" {
				continue
			}
			key := label
			value := label
			if fd.Relation != "" {
				key = fmt.Sprint(row["id"])
				value = key
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Candidate{ID: fmt.Sprint(row["id"]), Label: label, Value: value})
		}
	}
	return out, nil
}

// labelOf joins the row's label fields ("Ana" + "Gómez" → "Ana Gómez").
func labelOf(row map[string]any, fields []string) string {
	var parts []string
	for _, f := range fields {
		if v, ok := row[f]; ok && v != nil {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, " ")
}

// execute runs a validated, name-resolved plan and composes the reply.
func execute(ctx context.Context, d Deps, p Plan) Result {
	res := d.Vocab.Resource(p.Resource)
	params := url.Values{}
	var understood []string
	understood = append(understood, p.Resource)
	var window Window
	var agenda *Range
	if p.Period != nil {
		w, _ := Resolve(p.Period.Range, d.Now)
		window = w
		field := p.Period.Field
		if field == "" {
			field, _ = res.PeriodTarget()
		}
		if rg := res.RangeNamed(field); rg != nil {
			// The agenda (MOTOR-AGENDA-S1): what is SCHEDULED in the window, or
			// what contains one instant of its first day.
			agenda = rg
			if p.Period.At != "" {
				at := clockOn(w.From, p.Period.At)
				params.Set("filter["+rg.Name+"][contains]", at.UTC().Format(time.RFC3339))
				understood = append(understood, w.Words+" a las "+p.Period.At)
			} else {
				params.Set("filter["+rg.Name+"][overlaps]", w.From.UTC().Format(time.RFC3339)+"/"+w.To.UTC().Format(time.RFC3339))
				understood = append(understood, w.Words)
			}
		} else {
			params.Set("filter["+field+"][gte]", w.From.UTC().Format(time.RFC3339))
			params.Set("filter["+field+"][lt]", w.To.UTC().Format(time.RFC3339))
			understood = append(understood, w.Words)
		}
	}
	for _, f := range p.Filters {
		fd := res.Field(f.Field)
		if f.Op == "is_null" {
			v := "true"
			if b, ok := f.Value.(bool); ok && !b {
				v = "false"
			}
			params.Set("filter["+f.Field+"][is_null]", v)
			understood = append(understood, f.Field+" vacío")
			continue
		}
		val := literal(f.Value)
		if fd != nil && fd.Type == "time" && val == "now" {
			val = d.Now.UTC().Format(time.RFC3339)
		}
		params.Set("filter["+f.Field+"]["+f.Op+"]", val)
		understood = append(understood, describeFilter(fd, f))
	}
	if p.IsAggregate() {
		params.Set("count", "true")
		if p.Kind != "count" {
			params.Set(p.Kind, p.Field)
		}
		if p.GroupBy != "" {
			params.Set("group_by", p.GroupBy)
		}
		rows, err := d.Exec.Aggregate(ctx, p.Resource, params)
		if err != nil {
			return execFailure(err)
		}
		return composeAggregate(d, p, res, rows, strings.Join(understood, " · "))
	}
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if p.Kind == "free" {
		limit = 100 // the whole window's blocks — the gaps need all of them
	}
	params.Set("per_page", strconv.Itoa(limit))
	params.Set("count", "true")
	cols := listColumns(res)
	if rg := res.Range(); rg != nil {
		// An agenda line needs both bounds, in start order.
		cols = appendMissing(cols, rg.Start, rg.End)
		params.Set("sort", rg.Start)
		params.Set("order", "asc")
	} else if tf := res.DefaultTimeField(); tf != "" {
		params.Set("sort", tf)
		params.Set("order", "desc")
	}
	params.Set("fields", strings.Join(cols, ","))
	rows, total, err := d.Exec.List(ctx, p.Resource, params)
	if err != nil {
		return execFailure(err)
	}
	if p.Kind == "free" {
		return composeFree(d, res, res.Range(), rows, window, strings.Join(understood, " · "))
	}
	if agenda != nil || (p.Period == nil && res.Range() != nil) {
		return composeAgenda(d, p, res, res.Range(), rows, total, strings.Join(understood, " · "))
	}
	return composeList(d, p, res, rows, total, cols, strings.Join(understood, " · "))
}

func execFailure(err error) Result {
	var ee *ExecError
	if errors.As(err, &ee) {
		switch ee.Status {
		case 403:
			return Result{Kind: "forbidden", Headline: "No puedo ver eso", Detail: ee.Msg,
				Text: "🔒 Tu rol no puede ver uno de los datos que pide esa pregunta."}
		case 400:
			return Result{Kind: "unclear", Headline: "No entendí", Detail: ee.Msg,
				Text: "🤔 <b>No entendí</b> la pregunta lo bastante bien como para consultarla. Probá con otras palabras."}
		}
	}
	return Result{Kind: "unavailable", Headline: "No pude consultar", Detail: err.Error(),
		Text: "⚠️ No pude consultar la base ahora. Probá en unos segundos."}
}

func literal(v any) string {
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(v)
	}
}

func describeFilter(fd *Field, f Filter) string {
	op := map[string]string{"eq": "=", "gt": ">", "gte": "≥", "lt": "<", "lte": "≤", "partial": "contiene", "start": "empieza por"}[f.Op]
	val := literal(f.Value)
	if fd != nil && fd.Money {
		if n, err := strconv.ParseFloat(val, 64); err == nil {
			val = Money(n)
		}
	}
	if fd != nil && fd.Relation != "" && f.Op == "eq" {
		return singular(fd.Relation) + " elegido"
	}
	if fd != nil && fd.Type == "time" && val == "now" {
		val = "ahora"
	}
	if fd != nil && fd.Type == "bool" && f.Op == "eq" {
		if b, ok := f.Value.(bool); ok {
			if b {
				return f.Field + ": sí"
			}
			return f.Field + ": no"
		}
	}
	return f.Field + " " + op + " " + val
}

// listColumns picks the columns a phone list line needs: the label fields,
// the state field, the first money field, the creation timestamp — at most
// six, id always.
func listColumns(res *Resource) []string {
	cols := []string{"id"}
	add := func(n string) {
		if n == "" || len(cols) >= 7 {
			return
		}
		for _, c := range cols {
			if c == n {
				return
			}
		}
		cols = append(cols, n)
	}
	for _, f := range res.LabelFields() {
		add(f)
		if len(cols) >= 3 {
			break
		}
	}
	if sf := res.StateField(); sf != nil {
		add(sf.Name)
	} else {
		for _, f := range res.Fields {
			if len(f.Enum) > 0 {
				add(f.Name)
				break
			}
		}
	}
	add(res.MoneyField())
	add(res.DefaultTimeField())
	return cols
}

// modelOffResult words a question that neither the parser nor the cache
// could solve while the model may not be called.
func modelOffResult(d Deps, parseReason string) Result {
	r := Result{Detail: "parser: " + parseReason}
	switch d.ModelOff {
	case "capped":
		r.Kind = "capped"
		r.Headline = "Techo diario del modelo alcanzado"
		r.Text = "🧾 Hoy ya se gastó el techo diario del modelo, así que esa pregunta no la puedo pensar hasta mañana. Las preguntas simples (contar, listar, sumar un recurso por su nombre, con estado y período) siguen respondiendo, y los comandos fijos también: <b>resumen</b>, <b>estado</b>, <b>ayuda</b>."
	case "user_capped":
		r.Kind = "capped"
		r.Headline = "Tu cupo diario del modelo se agotó"
		r.Text = "🧾 Ya usaste tu cupo diario del modelo, así que esa pregunta no la puedo pensar hasta mañana. Las preguntas simples siguen respondiendo, y los comandos fijos también: <b>resumen</b>, <b>estado</b>, <b>ayuda</b>."
	case "minute":
		r.Kind = "capped"
		r.Headline = "Demasiadas preguntas al modelo este minuto"
		r.Text = "⏳ Demasiadas preguntas al modelo en este minuto. Esperá un momento y volvé a preguntar; las preguntas simples siguen respondiendo al instante."
	default:
		r.Kind = "disabled"
		r.Headline = "Preguntas al modelo no activadas"
		r.Text = "Esa pregunta necesita el modelo y las preguntas al modelo no están activadas en esta app (falta la clave, ANTHROPIC_API_KEY). Las preguntas simples (contar, listar, sumar un recurso por su nombre, con estado y período) sí responden, y los comandos fijos también: <b>resumen</b>, <b>estado</b>, <b>ayuda</b>."
	}
	return r
}

// clockOn places an HH:MM clock on a day (the window's first day).
func clockOn(day time.Time, clock string) time.Time {
	m := clockRe.FindStringSubmatch(clock)
	if m == nil {
		return day
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	return time.Date(day.Year(), day.Month(), day.Day(), h, mi, 0, 0, day.Location())
}

func appendMissing(cols []string, more ...string) []string {
	for _, m := range more {
		if !containsStr(cols, m) {
			cols = append(cols, m)
		}
	}
	return cols
}
