package ask

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/appximo/appximo/pkg/schema"
)

// Field is one askable field of a resource — the subset of schema.FieldDef the
// plan grammar cares about.
type Field struct {
	Name     string
	Type     string
	Enum     []string
	Relation string
	// Pending lists the state-machine states declared as "waiting for someone"
	// (the digest's attention states); Terminal the states with no way out.
	Pending, Terminal []string
	HasMachine        bool
	// IsCreated marks the resource's creation timestamp (auto:"create").
	IsCreated bool
	// IsUpdated marks the modification timestamp (auto:"update").
	IsUpdated bool
	// Money marks an int64 in a currency's minor unit, by the name convention
	// the engine documents (price_cents, total_centavos).
	Money bool
	// Required / HasDefault / Auto (VOZ-ESCRITURAS-S1): what a create must
	// carry, what the engine fills, what the client may never write.
	Required   bool
	HasDefault bool
	Default    any // the declared default (create-only), when HasDefault
	Auto       bool
	// Initial lists the state machine's initial states (a create may only be
	// born there); Transitions maps state → reachable states.
	Initial     []string
	Transitions map[string][]string
	// Aliases (VOZ-AHORRO-S2, ADR-038) map a declared enum value to the words
	// people say for it («sin pagar» → pendiente_pago). Declared in the
	// schema, validated unique per resource at load; the parser consumes an
	// alias exactly like the value.
	Aliases map[string][]string
}

// valueForm is one way an enum value is said: the normalized phrase, the
// declared value it means, and whether the phrase has several words (those
// are consumed before resources, so «pendiente de pago» never reads «pago»
// as the resource pagos).
type valueForm struct {
	form, val string
	multi     bool
	alias     bool
}

// valueForms lists every form of every declared value of f — the value's own
// forms (schema.ValueForms) and its aliases' — in schema order.
func (f *Field) valueForms() []valueForm {
	var out []valueForm
	for _, val := range f.Enum {
		for _, form := range schema.ValueForms(val) {
			out = append(out, valueForm{form: form, val: val, multi: strings.Contains(form, " ")})
		}
		for _, a := range f.Aliases[val] {
			for _, form := range schema.ValueForms(a) {
				out = append(out, valueForm{form: form, val: val, multi: strings.Contains(form, " "), alias: true})
			}
		}
	}
	return out
}

func (f *Field) IsNumeric() bool {
	return f.Type == "int" || f.Type == "int64" || f.Type == "float64"
}
func (f *Field) IsText() bool { return f.Type == "string" || f.Type == "text" }

// Groupable mirrors the aggregate endpoint's rule (anything but json) narrowed
// to what makes sense as a group key for a phone reply.
func (f *Field) Groupable() bool {
	switch f.Type {
	case "string", "bool", "int", "int64", "uuid":
		return true
	}
	return false
}

// Resource is one askable resource: its readable fields, in schema order.
type Resource struct {
	Name   string
	Fields []*Field
	byName map[string]*Field
	// Listed is true when the schema's summary.resources names it (the owner's
	// own ranking — the first thing kept when the vocabulary must be trimmed).
	Listed bool
	// CanCreate / CanUpdate (VOZ-ESCRITURAS-S1): what the asking role may
	// write on this resource. The model is told, so it never plans a write
	// the role cannot make; the executor re-checks regardless.
	CanCreate, CanUpdate bool
	// Aliases (VOZ-AHORRO-S2) are the words people use for this resource
	// («pedidos», «ventas» for ordenes), declared in the schema and validated
	// unique across it; the parser names the resource by them too.
	Aliases []string
	// Ranges (MOTOR-AGENDA-S1) are the resource's declared time ranges — the
	// agenda: a period asks what is scheduled then, a write fills both
	// bounds, the confirmation checks the collision.
	Ranges []Range
}

func (r *Resource) Field(name string) *Field { return r.byName[name] }

// NameForms lists every form the resource is named by: its schema name
// (singular/plural, underscores as spaces) and each declared alias's forms —
// the SAME derivation the validator used to prove them unique.
func (r *Resource) NameForms() []string {
	forms := schema.NameForms(r.Name)
	for _, a := range r.Aliases {
		for _, f := range schema.NameForms(a) {
			if !contains(forms, f) {
				forms = append(forms, f)
			}
		}
	}
	return forms
}

// DefaultTimeField is the field a bare period applies to: the creation
// timestamp when declared; else the only time field; else "".
func (r *Resource) DefaultTimeField() string {
	var only string
	n := 0
	for _, f := range r.Fields {
		if f.IsCreated {
			return f.Name
		}
		if f.Type == "time" {
			only = f.Name
			n++
		}
	}
	if n == 1 {
		return only
	}
	return ""
}

// StateField is the field carrying a state machine, "" when none.
func (r *Resource) StateField() *Field {
	for _, f := range r.Fields {
		if f.HasMachine {
			return f
		}
	}
	return nil
}

func (r *Resource) FieldList() string {
	names := make([]string, 0, len(r.Fields))
	for _, f := range r.Fields {
		names = append(names, f.Name)
	}
	return strings.Join(names, ", ")
}

func (r *Resource) filteredList(keep func(*Field) bool) string {
	var names []string
	for _, f := range r.Fields {
		if keep(f) {
			names = append(names, f.Name)
		}
	}
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

func (r *Resource) TimeFieldList() string {
	return r.filteredList(func(f *Field) bool { return f.Type == "time" })
}
func (r *Resource) NumericFieldList() string {
	return r.filteredList(func(f *Field) bool { return f.IsNumeric() })
}
func (r *Resource) GroupableFieldList() string {
	return r.filteredList(func(f *Field) bool { return f.Groupable() })
}

// LabelFields are the string fields that best NAME a row (for list lines and
// for matching a dictated proper name): fields whose names say so (nombre,
// name, titulo, razon_social, apellido…), else every non-enum string field.
func (r *Resource) LabelFields() []string {
	var primary, secondary, plain []string
	for _, f := range r.Fields {
		if !f.IsText() || len(f.Enum) > 0 {
			continue
		}
		switch {
		case nameRank(f.Name) < len(primaryParts):
			primary = append(primary, f.Name)
		case nameRank(f.Name) < len(nameishParts):
			secondary = append(secondary, f.Name)
		default:
			plain = append(plain, f.Name)
		}
	}
	// A NAME wins alone: "Ana Gómez", never "Ana Gómez 333946139" (the
	// documento_numero that also matched "numero" — seen live). The secondary
	// identifiers (numero, codigo, sku, placa…) label a row only when it has
	// no name-like field (an order is "ORD-1003").
	switch {
	case len(primary) > 0:
		sort.SliceStable(primary, func(i, j int) bool { return nameRank(primary[i]) < nameRank(primary[j]) })
		return primary
	case len(secondary) > 0:
		sort.SliceStable(secondary, func(i, j int) bool { return nameRank(secondary[i]) < nameRank(secondary[j]) })
		return secondary
	}
	return plain
}

// nameRank orders label candidates: the primary name-like parts first (in
// this order), then the secondary identifiers; anything else last.
func nameRank(field string) int {
	for i, p := range nameishParts {
		if strings.Contains(field, p) {
			return i
		}
	}
	return len(nameishParts)
}

var (
	primaryParts = []string{"nombre", "name", "titulo", "title", "razon", "apellido", "asunto", "subject"}
	nameishParts = append(append([]string(nil), primaryParts...), "numero", "codigo", "sku", "placa", "referencia", "descripcion")
)

// Vocabulary is what the model may name: the resources the asking ROLE may
// read, each with the fields that role may read. Built per request from the
// schema + the role's RBAC evaluation, so a role never even receives the word
// for a resource it cannot see.
type Vocabulary struct {
	// listedOrder keeps summary.resources in the OWNER'S order (Listed on a
	// Resource loses it) — what the help shows first.
	listedOrder []string
	AppName     string
	resources   map[string]*Resource
	order       []string
	writeCheck  WriteCheck
	// fp is the vocabulary's fingerprint (Fingerprint): every name the parser
	// and the model may recognize, hashed — a plan cache scoped by it never
	// serves a plan translated against a vocabulary that has since changed
	// (a synonym declared after a «no entendí» must cure it, not sit behind
	// a cached refusal).
	fp string
	// lexicon is every single word and every multi-word phrase the parser
	// could consume as a schema word (resources, aliases, values, fields),
	// built once for the pre-discard check (VOZ-AHORRO-S2 Part C).
	lexOnce sync.Once
	lexWord map[string]bool
	lexMany []string
}

// Fingerprint identifies the vocabulary's content (resources, aliases, fields,
// enum values and value aliases) — stable across processes for the same
// schema and role, different for any change the parser or the model could
// see.
func (v *Vocabulary) Fingerprint() string { return v.fp }

func (v *Vocabulary) fingerprint() string {
	h := sha256.New()
	for _, n := range v.order {
		r := v.resources[n]
		fmt.Fprintf(h, "R %s %s\n", n, strings.Join(r.Aliases, ","))
		for _, rg := range r.Ranges {
			fmt.Fprintf(h, "G %s %s %s %v %s\n", rg.Name, rg.Start, rg.End, rg.NoOverlap, rg.Default)
		}
		for _, f := range r.Fields {
			fmt.Fprintf(h, "F %s %s %s %s\n", f.Name, f.Type, f.Relation, strings.Join(f.Enum, ","))
			for _, val := range sortedKeys(f.Aliases) {
				fmt.Fprintf(h, "A %s=%s\n", val, strings.Join(f.Aliases[val], ","))
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// lexicon builds the schema-word index once: every single-word form goes to
// lexWord, every multi-word form to lexMany.
func (v *Vocabulary) lexicon() (map[string]bool, []string) {
	v.lexOnce.Do(func() {
		v.lexWord = map[string]bool{}
		for _, n := range v.order {
			r := v.resources[n]
			for _, f := range r.NameForms() {
				v.addLex(f)
			}
			for _, f := range r.Fields {
				for _, form := range schema.NameForms(f.Name) {
					v.addLex(form)
				}
				for _, vf := range f.valueForms() {
					v.addLex(vf.form)
				}
			}
		}
	})
	return v.lexWord, v.lexMany
}

func (v *Vocabulary) addLex(form string) {
	if form == "" {
		return
	}
	if strings.Contains(form, " ") {
		if !contains(v.lexMany, form) {
			v.lexMany = append(v.lexMany, form)
		}
		return
	}
	v.lexWord[form] = true
}

// BuildWithWrites is Build plus the role's write abilities per resource.
func BuildWithWrites(s *schema.APISchema, appName string, check ReadCheck, wcheck WriteCheck) *Vocabulary {
	v := &Vocabulary{AppName: appName, resources: map[string]*Resource{}, writeCheck: wcheck}
	fill(v, s, check)
	return v
}

// ReadCheck answers, for one resource, whether the role may read it and which
// fields it may see (nil = all). The handler binds it to rbac.Policy.Evaluate.
type ReadCheck func(resource string) (allowed bool, fields []string)

// WriteCheck answers whether the role may create / update a resource.
type WriteCheck func(resource string) (create, update bool)

// Build derives the vocabulary from the schema for one role.
func Build(s *schema.APISchema, appName string, check ReadCheck) *Vocabulary {
	v := &Vocabulary{AppName: appName, resources: map[string]*Resource{}}
	fill(v, s, check)
	return v
}

func fill(v *Vocabulary, s *schema.APISchema, check ReadCheck) {
	if v.AppName == "" {
		v.AppName = s.Name
	}
	listed := map[string]bool{}
	if s.Summary != nil {
		for _, n := range s.Summary.Resources {
			listed[n] = true
			if allowed, _ := check(n); allowed {
				v.listedOrder = append(v.listedOrder, n)
			}
		}
	}
	for _, name := range sortedKeys(s.Resources) {
		allowed, allowedFields := check(name)
		if !allowed {
			continue
		}
		res := s.Resources[name]
		r := BuildResource(name, &res, allowedFields)
		r.Listed = listed[name]
		r.Aliases = append([]string(nil), res.Aliases...)
		if v.writeCheck != nil {
			r.CanCreate, r.CanUpdate = v.writeCheck(name)
		}
		v.resources[name] = r
		v.order = append(v.order, name)
	}
	v.fp = v.fingerprint()
}

// BuildResource projects one schema resource onto the askable shape, keeping
// only the fields in allowed (nil = all).
func BuildResource(name string, res *schema.ResourceSchema, allowed []string) *Resource {
	var allow map[string]bool
	if allowed != nil {
		allow = make(map[string]bool, len(allowed))
		for _, f := range allowed {
			allow[f] = true
		}
	}
	r := &Resource{Name: name, byName: map[string]*Field{}}
	for _, fname := range sortedKeys(res.Fields) {
		if allow != nil && !allow[fname] {
			continue
		}
		fd := res.Fields[fname]
		f := &Field{Name: fname, Type: fd.Type, Enum: fd.Enum, Relation: fd.Relation, Required: fd.Required, HasDefault: fd.Default != nil, Default: fd.Default, Auto: fd.Auto.Enabled(), Aliases: fd.Aliases}
		if fd.Auto.Enabled() && fd.Type == "time" {
			if fd.Auto.RefreshesOnUpdate(fname) {
				f.IsUpdated = true
			} else {
				f.IsCreated = true
			}
		}
		if fd.StateMachine != nil {
			f.HasMachine = true
			sm := fd.StateMachine
			for s := range sm.KnownStates() {
				if sm.IsTerminal(s) {
					f.Terminal = append(f.Terminal, s)
				}
			}
			sort.Strings(f.Terminal)
			f.Initial = append(f.Initial, sm.Initial...)
			f.Transitions = sm.Transitions
			if sm.PendingDeclared() {
				f.Pending = append(f.Pending, sm.Pending...)
			}
		}
		if fd.Type == "int64" || fd.Type == "int" {
			f.Money = isMoneyName(fname)
		}
		r.Fields = append(r.Fields, f)
		r.byName[fname] = f
	}
	for _, name := range res.RangeNames() {
		rd := res.Ranges[name]
		if r.byName[rd.Start] == nil || r.byName[rd.End] == nil {
			continue // a bound the role may not read: no agenda for this role
		}
		rg := Range{Name: name, Start: rd.Start, End: rd.End, Default: rd.DefaultDurationValue()}
		if no := rd.NoOverlap; no != nil {
			rg.NoOverlap = true
			rg.Scope = append(rg.Scope, no.Scope...)
			if w := no.When; w != nil {
				rg.WhenField, rg.WhenOp, rg.WhenVal = w.Field, w.Op, w.Val
			}
		}
		r.Ranges = append(r.Ranges, rg)
	}
	return r
}

func isMoneyName(name string) bool {
	return strings.HasSuffix(name, "_cents") || strings.HasSuffix(name, "_centavos") || strings.Contains(name, "centavos") || strings.HasSuffix(name, "_cent")
}

func (v *Vocabulary) Resource(name string) *Resource { return v.resources[name] }
func (v *Vocabulary) ResourceNames() []string        { return append([]string(nil), v.order...) }
func (v *Vocabulary) Len() int                       { return len(v.order) }

// PromptBudget bounds the vocabulary text sent to the model (characters).
// ≈ 3 000 tokens: enough for ~40 fully described resources; a wider schema
// is trimmed (see Render) rather than sent whole.
const PromptBudget = 12000

// Render writes the vocabulary as compact lines — one per resource, its
// fields with type, enum members, the state machine's waiting/terminal states,
// relation targets, the creation timestamp and the money convention. Nothing
// else: no rows, no ids, no counts.
//
// Trimming, deterministic: resources named in summary.resources come first
// (the owner said these matter), then resources with a state machine, then
// the rest alphabetically; when the budget is exceeded the remaining resources
// are listed by NAME ONLY (still askable — a bare count works — but without
// field detail), and the prompt says so.
func (v *Vocabulary) Render() (text string, trimmed []string) {
	order := append([]string(nil), v.order...)
	rank := func(name string) int {
		r := v.resources[name]
		switch {
		case r.Listed:
			return 0
		case r.StateField() != nil:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		ri, rj := rank(order[i]), rank(order[j])
		if ri != rj {
			return ri < rj
		}
		return order[i] < order[j]
	})
	var b strings.Builder
	used := 0
	for i, name := range order {
		line := v.resources[name].renderLine()
		if used+len(line) > PromptBudget && i > 0 {
			trimmed = order[i:]
			break
		}
		b.WriteString(line)
		used += len(line)
	}
	if len(trimmed) > 0 {
		sort.Strings(trimmed)
		fmt.Fprintf(&b, "\nOther resources (name only, fields not listed — a count without filters is fine, otherwise answer unclear): %s\n", strings.Join(trimmed, ", "))
	}
	return b.String(), trimmed
}

func (r *Resource) renderLine() string {
	var parts []string
	for _, f := range r.Fields {
		var p strings.Builder
		p.WriteString(f.Name)
		p.WriteString("(")
		p.WriteString(f.Type)
		switch {
		case f.Relation != "":
			p.WriteString(" → " + f.Relation)
		case f.Money:
			p.WriteString(", money in cents")
		case f.IsCreated:
			p.WriteString(", created at")
		case f.IsUpdated:
			p.WriteString(", updated at")
		}
		if len(f.Enum) > 0 {
			vals := make([]string, 0, len(f.Enum))
			for _, e := range f.Enum {
				if al := f.Aliases[e]; len(al) > 0 {
					// The owner's own words for the value, so the model maps
					// «sin pagar» to pendiente_pago from the schema, not from a
					// guess. Declared, never wired.
					e += " (also: " + strings.Join(al, ", ") + ")"
				}
				vals = append(vals, e)
			}
			p.WriteString("; values: " + strings.Join(vals, "|"))
		}
		if f.HasMachine {
			if len(f.Pending) > 0 {
				p.WriteString("; waiting-for-action states: " + strings.Join(f.Pending, "|"))
			}
			if len(f.Terminal) > 0 {
				p.WriteString("; final states: " + strings.Join(f.Terminal, "|"))
			}
		}
		if f.Required && !f.HasDefault && !f.Auto {
			p.WriteString("; REQUIRED")
		}
		if f.Auto {
			p.WriteString("; engine-owned")
		}
		p.WriteString(")")
		parts = append(parts, p.String())
	}
	can := ""
	switch {
	case r.CanCreate && r.CanUpdate:
		can = " [may create+update]"
	case r.CanCreate:
		can = " [may create]"
	case r.CanUpdate:
		can = " [may update]"
	}
	also := ""
	if len(r.Aliases) > 0 {
		also = " (also called: " + strings.Join(r.Aliases, ", ") + ")"
	}
	for _, rg := range r.Ranges {
		// The agenda: the model learns which two fields form the block, what
		// a bare start lasts, and that a collision is checked by the engine.
		line := fmt.Sprintf(" [time range %s: %s..%s, default duration %s", rg.Name, rg.Start, rg.End, rg.Default)
		if rg.NoOverlap {
			line += ", no overlap"
		}
		parts = append(parts, line+"]")
	}
	return "- " + r.Name + ":" + can + also + " " + strings.Join(parts, " ") + "\n"
}

// MoneyField is the amount a row is best summarized by: a money field named
// total/monto/valor/precio/importe first (an order's descuento_centavos is
// not its amount — seen live as "$ 0" on every line), else the first one.
func (r *Resource) MoneyField() string {
	best, bestRank := "", 99
	for _, f := range r.Fields {
		if !f.Money {
			continue
		}
		rank := 10 // any money field
		for i, p := range []string{"total", "monto", "valor", "importe", "amount", "precio", "price"} {
			switch {
			case strings.HasPrefix(f.Name, p):
				rank = min(rank, i) // "total_centavos" beats "subtotal_centavos"
			case strings.Contains(f.Name, p):
				rank = min(rank, 5+i)
			}
		}
		if rank < bestRank {
			best, bestRank = f.Name, rank
		}
	}
	return best
}
