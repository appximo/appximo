package ask

import (
	"fmt"
	"sort"
	"strings"

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
}

func (r *Resource) Field(name string) *Field { return r.byName[name] }

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
	AppName   string
	resources map[string]*Resource
	order     []string
}

// ReadCheck answers, for one resource, whether the role may read it and which
// fields it may see (nil = all). The handler binds it to rbac.Policy.Evaluate.
type ReadCheck func(resource string) (allowed bool, fields []string)

// Build derives the vocabulary from the schema for one role.
func Build(s *schema.APISchema, appName string, check ReadCheck) *Vocabulary {
	v := &Vocabulary{AppName: appName, resources: map[string]*Resource{}}
	if v.AppName == "" {
		v.AppName = s.Name
	}
	listed := map[string]bool{}
	if s.Summary != nil {
		for _, n := range s.Summary.Resources {
			listed[n] = true
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
		v.resources[name] = r
		v.order = append(v.order, name)
	}
	return v
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
		f := &Field{Name: fname, Type: fd.Type, Enum: fd.Enum, Relation: fd.Relation}
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
			p.WriteString("; values: " + strings.Join(f.Enum, "|"))
		}
		if f.HasMachine {
			if len(f.Pending) > 0 {
				p.WriteString("; waiting-for-action states: " + strings.Join(f.Pending, "|"))
			}
			if len(f.Terminal) > 0 {
				p.WriteString("; final states: " + strings.Join(f.Terminal, "|"))
			}
		}
		p.WriteString(")")
		parts = append(parts, p.String())
	}
	return "- " + r.Name + ": " + strings.Join(parts, " ") + "\n"
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
