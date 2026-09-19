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
	// prefix of its keys — a plan never crosses tenants or roles.
	Cache      *PlanCache
	CacheScope string
	// NoParser skips the deterministic parser (tests of the model path).
	NoParser bool
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
	// Detail is the engine-side reason for the log (never the owner).
	Detail string `json:"-"`
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
	if res.Speech == "" {
		res.Speech = Speech(res.Text)
	}
	return res
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

	// 1. The deterministic parser — no model, no cache, microseconds. It
	// answers only when SURE (parser.go); otherwise it says why, for the log.
	var p Plan
	var tr Translation
	source := ""
	parseReason := ""
	if !d.NoParser {
		if pr := Parse(question, d.Vocab); pr.Sure {
			p, source = pr.Plan, "parser"
		} else {
			parseReason = pr.Reason
		}
	}
	// 2. The plan cache: the same question, tenant and role → the plan the
	// model produced before; the data is recomputed below.
	key := ""
	if source == "" && d.Cache != nil {
		key = Key(d.CacheScope, question)
		if cp, _, ok := d.Cache.Get(key); ok {
			p, source = cp, "cache"
		}
	}
	// 3. The model — only for what neither could solve, and only when allowed.
	base := Result{}
	if source == "" {
		if d.Model == nil {
			return modelOffResult(d, parseReason)
		}
		mstart := time.Now()
		var err error
		tr, err = Translate(ctx, d.Model, d.Vocab, question, d.Now)
		base = Result{Usage: tr.Usage, ModelMS: time.Since(mstart).Milliseconds(), Corrected: tr.Corrected, Source: "model"}
		if err != nil {
			base.Kind = "unavailable"
			base.Detail = err.Error()
			base.Headline = "El modelo no respondió"
			base.Text = "⚠️ No pude pensar la pregunta ahora (el modelo no respondió a tiempo). Los comandos fijos siguen funcionando: <b>resumen</b>, <b>estado</b>, <b>ayuda</b>."
			return base
		}
		p, source = tr.Plan, "model"
		if d.Cache != nil && key != "" {
			d.Cache.Put(key, p, "model")
		}
	}
	base.Source = source
	if parseReason != "" {
		base.Detail = "parser: " + parseReason
	}
	switch p.Kind {
	case "write":
		base.Kind = "write_refused"
		base.Headline = "Por acá solo leo"
		base.Text = "🔒 Por acá solo <b>leo</b>. Crear, cambiar, cancelar o borrar datos llega en una próxima etapa, con confirmación. " + askable(d.Vocab)
		base.Detail = "write intent: " + p.Reason
		return base
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
		nameRes.Usage, nameRes.ModelMS, nameRes.Corrected, nameRes.Plan, nameRes.Source = base.Usage, base.ModelMS, base.Corrected, &p, source
		return nameRes
	}
	said := nameRes.Understood // "Entendí «Gomes» como Ana Gómez."

	out := execute(ctx, d, resolved)
	out.Usage, out.ModelMS, out.Corrected, out.Plan, out.Source = base.Usage, base.ModelMS, base.Corrected, &p, source
	if base.Detail != "" && out.Detail == "" {
		out.Detail = base.Detail
	}
	if said != "" && out.Kind == "answer" {
		out.Text = said + "\n" + out.Text
	}
	return out
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
	if p.Period != nil {
		w, _ := Resolve(p.Period.Range, d.Now)
		field := p.Period.Field
		if field == "" {
			field = res.DefaultTimeField()
		}
		params.Set("filter["+field+"][gte]", w.From.UTC().Format(time.RFC3339))
		params.Set("filter["+field+"][lt]", w.To.UTC().Format(time.RFC3339))
		understood = append(understood, w.Words)
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
	params.Set("per_page", strconv.Itoa(limit))
	params.Set("count", "true")
	cols := listColumns(res)
	params.Set("fields", strings.Join(cols, ","))
	if tf := res.DefaultTimeField(); tf != "" {
		params.Set("sort", tf)
		params.Set("order", "desc")
	}
	rows, total, err := d.Exec.List(ctx, p.Resource, params)
	if err != nil {
		return execFailure(err)
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
