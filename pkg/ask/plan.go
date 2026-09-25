// Package ask turns a free-text question in the owner's language ("cuántas
// citas tengo hoy", "qué pedidos están sin pagar") into a READ the engine
// already knows how to execute — and never anything else (VOZ-PREGUNTAS-S1,
// step 2 of the voice plan A-70; design ADR-033).
//
// The contract, in one breath: a language model receives the schema's
// VOCABULARY (which resources, fields, enum values and states exist — never a
// row) and the question, and answers with a PLAN over a closed grammar
// (resource, filters, a period token, count/list/sum/…, group_by). The engine
// validates every name in the plan against the schema and REJECTS what does
// not exist (one correction round, then "no entendí" — never a guess), resolves
// proper names against what actually exists (one match → used and said; several
// → asked; none → said, never a zero), executes the plan through the same
// query builders REST uses, scoped by the asking role's RBAC, and composes the
// reply as a template over the engine's numbers. The model never writes SQL,
// never sees data, never produces a number. Read-only by grammar: there is no
// plan kind that writes.
//
// This package is pure — no HTTP, no SQL, no network. The model is reached
// through aigen.ModelClient (a seam tests stub), the data through Executor (a
// seam the /api/ask handler implements with the engine's builders).
package ask

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Plan is the ONLY thing the model may say. Every kind maps 1:1 onto an
// engine read: count/sum/avg/min/max → GET /api/{resource}/aggregate; list →
// GET /api/{resource}. `unclear` and `write` are first-class non-answers.
type Plan struct {
	// Kind is one of count | list | sum | avg | min | max | unclear | write.
	Kind     string `json:"kind"`
	Resource string `json:"resource,omitempty"`
	// Filters are the REST filter grammar, one operator per filter.
	Filters []Filter `json:"filters,omitempty"`
	// Period restricts a time field to a closed token ("today", "this_week"…)
	// that the ENGINE resolves to concrete timestamps — the model never emits a
	// date it computed.
	Period *Period `json:"period,omitempty"`
	// Field is the numeric/time field a sum/avg/min/max applies to.
	Field string `json:"field,omitempty"`
	// GroupBy splits a count/aggregate by one field (an enum/state, a bool…).
	GroupBy string `json:"group_by,omitempty"`
	// Limit bounds a list (default 10, max 20).
	Limit int `json:"limit,omitempty"`
	// Reason accompanies unclear/write: what the model could not map.
	Reason string `json:"reason,omitempty"`

	// ── writes (VOZ-ESCRITURAS-S1, ADR-037) ──
	// Data carries the values of a create, or the changes of an update: a
	// literal of the field's type (an enum member verbatim, a number, a
	// boolean, text SAID by the owner), a time TOKEN the engine resolves
	// ("today", "tomorrow", "next_monday", "tomorrow 15:00", or an ISO date),
	// or — for a relation field — an object {"match": "<name as said>"} that
	// the engine resolves against the rows that exist. Never an id, never a
	// governed field, never null.
	Data map[string]any `json:"data,omitempty"`
	// Where identifies the row(s) an update applies to (the read filter
	// grammar, `match` allowed). Exactly ONE row must match at confirmation
	// time; several → the owner is asked which; none → said.
	Where []Filter `json:"where,omitempty"`
	// Refs (VOZ-20) are the names of a write whose field the parser could
	// not tell; resolved against every candidate target before confirming.
	Refs []Ref `json:"refs,omitempty"`
}

// Filter is one predicate. Exactly one of Value / Match is set: Value is a
// literal (an enum member, a number, a boolean); Match is a PROPER NAME as the
// owner said it, which the engine resolves against the rows that exist (a
// relation field → the target resource; a string field → its own values).
type Filter struct {
	Field string `json:"field"`
	Op    string `json:"op,omitempty"` // eq (default) | gt | gte | lt | lte | partial | start | is_null
	Value any    `json:"value,omitempty"`
	Match string `json:"match,omitempty"`
	// Fields (VOZ-20, AGENDA-ASISTENTE-S1): the fields a proper name MAY
	// belong to when the sentence does not say («las tareas de Esposa» on a
	// resource with an area AND a person): the engine tries each target and
	// keeps the one where the name exists; both → it asks which; none → said.
	// Set only by the parser; Field is then empty until resolved.
	Fields []string `json:"fields,omitempty"`
	// Label is the row a resolved name matched («Fabián Gómez», «trabajo»),
	// for the understood text — never sent to the engine.
	Label string `json:"-"`
}

// Ref (VOZ-20) is a proper name of a WRITE that may belong to several
// relation fields («crear tarea: pagar el seguro, Casa» — an area or a
// person?). Resolved before the confirmation like any other name; Soft marks
// a bare word that, matching nothing, is kept in the title instead of
// refusing the write.
type Ref struct {
	Match   string   `json:"match"`
	Fields  []string `json:"fields"`
	Soft    bool     `json:"soft,omitempty"`
	InTitle bool     `json:"in_title,omitempty"` // the words already sit in the title text
}

// Period is a time window over one time field.
type Period struct {
	// Field is a `time` field of the resource — or a declared time RANGE
	// (MOTOR-AGENDA-S1: what is scheduled in the window); empty = the range
	// when the resource declares one, else the creation timestamp.
	Field string `json:"field,omitempty"`
	Range string `json:"range"`
	// At (MOTOR-AGENDA-S1) narrows the window to one instant of its first
	// day, "HH:MM" — «tengo algo mañana a las 4» → the blocks containing
	// tomorrow 16:00. Only meaningful over a range.
	At string `json:"at,omitempty"`
}

// The closed sets. Anything outside them is a validation error the model gets
// back once, then "no entendí".
var (
	Kinds = []string{"count", "list", "sum", "avg", "min", "max", "unclear", "write", "create", "update", "free"}
	Ops   = []string{"eq", "gt", "gte", "lt", "lte", "partial", "start", "is_null"}
	// Ranges: the past windows, plus the FUTURE days an agenda is asked about
	// (MOTOR-AGENDA-S1): tomorrow, day_after_tomorrow, next_week, next_<weekday>.
	Ranges = []string{"today", "yesterday", "day_before_yesterday", "this_week", "last_week", "this_month", "last_month", "last_7_days", "last_30_days", "this_year",
		"tomorrow", "day_after_tomorrow", "next_week", "next_monday", "next_tuesday", "next_wednesday", "next_thursday", "next_friday", "next_saturday", "next_sunday"}
	numericOps = map[string]bool{"eq": true, "gt": true, "gte": true, "lt": true, "lte": true, "is_null": true}
	textOps    = map[string]bool{"eq": true, "partial": true, "start": true, "is_null": true}
	flatOps    = map[string]bool{"eq": true, "is_null": true}
)

const (
	DefaultListLimit = 10
	MaxListLimit     = 20
)

// ParsePlan decodes the model's text into a Plan. It tolerates fences and
// prose around the object (the plain-generation defense aigen already needed)
// but is STRICT about unknown keys: a key outside the grammar is an error, the
// same doctrine as the schema loader — a typo must never become silently dead
// config, and here it must never become a silently ignored intent.
func ParsePlan(text string) (Plan, error) {
	raw := extractJSON(text)
	if raw == "" {
		return Plan{}, fmt.Errorf("the reply carried no JSON object")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var p Plan
	if err := dec.Decode(&p); err != nil {
		return Plan{}, fmt.Errorf("the reply is not a valid plan: %v", err)
	}
	p.Kind = strings.ToLower(strings.TrimSpace(p.Kind))
	p.Resource = strings.TrimSpace(p.Resource)
	for i := range p.Filters {
		p.Filters[i].Field = strings.TrimSpace(p.Filters[i].Field)
		p.Filters[i].Op = strings.ToLower(strings.TrimSpace(p.Filters[i].Op))
		if p.Filters[i].Op == "" {
			p.Filters[i].Op = "eq"
		}
		p.Filters[i].Match = strings.TrimSpace(p.Filters[i].Match)
	}
	if p.Period != nil {
		p.Period.Field = strings.TrimSpace(p.Period.Field)
		p.Period.Range = strings.ToLower(strings.TrimSpace(p.Period.Range))
	}
	return p, nil
}

// IsWrite reports whether the plan creates or updates.
func (p Plan) IsWrite() bool { return p.Kind == "create" || p.Kind == "update" }

// IsAggregate reports whether the kind runs through the aggregate endpoint.
func (p Plan) IsAggregate() bool {
	switch p.Kind {
	case "count", "sum", "avg", "min", "max":
		return true
	}
	return false
}

// Validate checks every name in the plan against the vocabulary — the
// resource exists and the role may read it, each field exists on it, each
// operator fits the field's type, each enum/state value is a declared member,
// the period field is a time field, the aggregate field is numeric (or time
// for min/max), group_by is groupable. It REJECTS, never adapts: the message
// names what exists so the model can correct itself once.
func (p Plan) Validate(v *Vocabulary) error {
	if !contains(Kinds, p.Kind) {
		return fmt.Errorf("kind %q is not one of %s", p.Kind, strings.Join(Kinds, "|"))
	}
	if p.Kind == "unclear" || p.Kind == "write" {
		return nil
	}
	if p.IsWrite() {
		return p.validateWrite(v)
	}
	if p.Resource == "" {
		return fmt.Errorf("resource is required; the resources you may ask about are: %s", strings.Join(v.ResourceNames(), ", "))
	}
	res := v.Resource(p.Resource)
	if res == nil {
		return fmt.Errorf("resource %q does not exist (or you may not read it); the resources you may ask about are: %s", p.Resource, strings.Join(v.ResourceNames(), ", "))
	}
	if err := validateFilters(v, res, p.Resource, p.Filters, "filters"); err != nil {
		return err
	}
	if p.Period != nil {
		if !contains(Ranges, p.Period.Range) {
			return fmt.Errorf("period.range %q is not one of %s", p.Period.Range, strings.Join(Ranges, "|"))
		}
		field := p.Period.Field
		if field == "" {
			field, _ = res.PeriodTarget()
			if field == "" {
				return fmt.Errorf("%s has no time field, so it cannot be asked about a period; ask without one", p.Resource)
			}
		}
		if rg := res.RangeNamed(field); rg != nil {
			if p.Period.At != "" && !clockRe.MatchString(p.Period.At) {
				return fmt.Errorf("period.at must be HH:MM")
			}
		} else {
			fd := res.Field(field)
			if fd == nil {
				return fmt.Errorf("period.field: %s has no field %q; its time fields are: %s", p.Resource, field, res.TimeFieldList())
			}
			if fd.Type != "time" {
				return fmt.Errorf("period.field %q is %s, not time; %s's time fields are: %s", field, fd.Type, p.Resource, res.TimeFieldList())
			}
			if p.Period.At != "" {
				return fmt.Errorf("period.at applies only to a time range (%s declares none on %q)", p.Resource, field)
			}
		}
	}
	if p.Kind == "free" {
		if res.Range() == nil {
			return fmt.Errorf("free asks for the gaps of an agenda; %s declares no time range", p.Resource)
		}
		if p.Period == nil {
			return fmt.Errorf("free needs a period (the day or week to look at)")
		}
		if p.Field != "" || p.GroupBy != "" {
			return fmt.Errorf("free takes no field/group_by")
		}
	}
	switch p.Kind {
	case "sum", "avg", "min", "max":
		if p.Field == "" {
			return fmt.Errorf("%s needs a field; numeric fields of %s: %s", p.Kind, p.Resource, res.NumericFieldList())
		}
		fd := res.Field(p.Field)
		if fd == nil {
			return fmt.Errorf("%s has no field %q; numeric fields: %s", p.Resource, p.Field, res.NumericFieldList())
		}
		if !fd.IsNumeric() && !((p.Kind == "min" || p.Kind == "max") && fd.Type == "time") {
			return fmt.Errorf("%s cannot be applied to %s.%s (%s); numeric fields: %s", p.Kind, p.Resource, p.Field, fd.Type, res.NumericFieldList())
		}
	case "list", "free":
		if p.Field != "" || p.GroupBy != "" {
			return fmt.Errorf("list takes no field/group_by (use count with group_by to count by a field)")
		}
	default: // count
		if p.Field != "" {
			return fmt.Errorf("count takes no field (use sum/avg/min/max with a field)")
		}
	}
	if p.GroupBy != "" {
		fd := res.Field(p.GroupBy)
		if fd == nil {
			return fmt.Errorf("group_by: %s has no field %q; groupable fields: %s", p.Resource, p.GroupBy, res.GroupableFieldList())
		}
		if !fd.Groupable() {
			return fmt.Errorf("group_by: %s.%s (%s) is not groupable; groupable fields: %s", p.Resource, p.GroupBy, fd.Type, res.GroupableFieldList())
		}
	}
	if p.Limit < 0 || p.Limit > MaxListLimit {
		return fmt.Errorf("limit must be between 1 and %d", MaxListLimit)
	}
	return nil
}

// validateFilters checks one filter list (a read's filters or an update's
// where) against the resource: field exists, op fits, value/match coherent.
func validateFilters(v *Vocabulary, res *Resource, resource string, filters []Filter, label string) error {
	for i, f := range filters {
		if len(f.Fields) > 0 {
			// A name the parser could not place: every candidate must be a
			// relation with a readable target or the resource's own text field.
			if f.Match == "" || f.Value != nil || (f.Op != "" && f.Op != "eq") {
				return fmt.Errorf(label+"[%d]: fields takes a match with op eq", i)
			}
			for _, name := range f.Fields {
				fd := res.Field(name)
				if fd == nil {
					return fmt.Errorf(label+"[%d]: %s has no field %q", i, resource, name)
				}
				if fd.Relation != "" && v.Resource(fd.Relation) == nil {
					return fmt.Errorf(label+"[%d]: %s.%s points at %s, which you may not read", i, resource, name, fd.Relation)
				}
				if fd.Relation == "" && !fd.IsText() {
					return fmt.Errorf(label+"[%d]: %s.%s cannot hold a name", i, resource, name)
				}
			}
			continue
		}
		if f.Field == "" {
			return fmt.Errorf(label+"[%d]: field is required (fields of %s: %s)", i, resource, res.FieldList())
		}
		fd := res.Field(f.Field)
		if fd == nil {
			return fmt.Errorf(label+"[%d]: %s has no field %q; it has: %s", i, resource, f.Field, res.FieldList())
		}
		if !contains(Ops, f.Op) {
			return fmt.Errorf(label+"[%d]: op %q is not one of %s", i, f.Op, strings.Join(Ops, "|"))
		}
		if f.Match != "" && f.Value != nil {
			return fmt.Errorf(label+"[%d]: use either value or match, not both", i)
		}
		if f.Op == "is_null" {
			if f.Match != "" {
				return fmt.Errorf(label+"[%d]: is_null takes no match", i)
			}
			continue
		}
		if f.Match != "" {
			if f.Op != "eq" {
				return fmt.Errorf(label+"[%d]: match only works with op eq", i)
			}
			if fd.Relation == "" && !fd.IsText() {
				return fmt.Errorf(label+"[%d]: match is for a relation field or a text field; %s.%s is %s — use value", i, resource, f.Field, fd.Type)
			}
			if fd.Relation != "" && v.Resource(fd.Relation) == nil {
				return fmt.Errorf(label+"[%d]: %s.%s points at %s, which you may not read", i, resource, f.Field, fd.Relation)
			}
			continue
		}
		if f.Value == nil {
			return fmt.Errorf(label+"[%d]: value is required for op %s on %s.%s", i, f.Op, resource, f.Field)
		}
		if err := checkFilterValue(fd, f); err != nil {
			return fmt.Errorf(label+"[%d]: %v", i, err)
		}
	}
	return nil
}

// checkFilterValue type-checks a literal filter value against the field.
func checkFilterValue(fd *Field, f Filter) error {
	switch {
	case fd.IsNumeric():
		if !numericOps[f.Op] {
			return fmt.Errorf("op %s does not apply to a %s field; use eq|gt|gte|lt|lte", f.Op, fd.Type)
		}
		if _, ok := f.Value.(float64); !ok {
			if s, isStr := f.Value.(string); !isStr || !looksNumeric(s) {
				return fmt.Errorf("value for %s (%s) must be a number, got %v", f.Field, fd.Type, f.Value)
			}
		}
	case fd.Type == "bool":
		if !flatOps[f.Op] {
			return fmt.Errorf("a bool field takes only eq")
		}
		if _, ok := f.Value.(bool); !ok {
			return fmt.Errorf("value for %s must be true or false", f.Field)
		}
	case fd.Type == "time":
		// The one literal a time filter takes is the token "now" — "vigente",
		// "vencido", "todavía no empieza": a date column against the present,
		// resolved by the ENGINE. A window in the past is `period`.
		if s, ok := f.Value.(string); !ok || s != "now" {
			return fmt.Errorf("a time field is filtered with period (range today|yesterday|this_week|…), or compared to the present with value \"now\" (op gt|gte|lt|lte) — never with a date you computed")
		}
		if f.Op == "eq" {
			return fmt.Errorf("a time field compared to \"now\" takes gt|gte|lt|lte, not eq")
		}
	case fd.IsText():
		if !textOps[f.Op] {
			return fmt.Errorf("op %s does not apply to a text field; use eq|partial|start", f.Op)
		}
		s, ok := f.Value.(string)
		if !ok {
			return fmt.Errorf("value for %s must be a string", f.Field)
		}
		if len(fd.Enum) > 0 && !contains(fd.Enum, s) {
			return fmt.Errorf("%q is not a value of %s; it only takes: %s", s, f.Field, strings.Join(fd.Enum, "|"))
		}
	default: // uuid, file, json, jsonb
		if !flatOps[f.Op] {
			return fmt.Errorf("a %s field takes only eq/is_null", fd.Type)
		}
		if _, ok := f.Value.(string); !ok {
			return fmt.Errorf("value for %s must be a string", f.Field)
		}
	}
	return nil
}

func looksNumeric(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for i, c := range s {
		if c >= '0' && c <= '9' || c == '.' || (i == 0 && (c == '-' || c == '+')) {
			continue
		}
		return false
	}
	return true
}

func contains(set []string, s string) bool {
	for _, x := range set {
		if x == s {
			return true
		}
	}
	return false
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// extractJSON pulls the first balanced top-level object out of a model reply
// (fences and stray prose tolerated). Mirrors aigen's defense.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
