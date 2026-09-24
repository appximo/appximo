package ask

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Voice WRITES (VOZ-ESCRITURAS-S1, ADR-037) — create and update, never delete.
//
// The doctrine, in order of what it protects:
//
//  1. Nothing is written without a confirmation the owner gives AFTER reading
//     exactly what will be written — every value, every matched name — and the
//     confirmation is a plain, unambiguous yes; anything else does not execute.
//  2. The write goes through the engine's own write cores (the SAME code the
//     batch transaction runs: RBAC, validation, hooks, state machines, outbox
//     events). The voice layer never touches a table.
//  3. A proper name never lands in a relation field as text: it is matched
//     against the rows that exist BEFORE the confirmation, and the confirmation
//     shows which row was chosen. Several candidates → the owner picks; none →
//     the owner is offered to create it (which confirms too).
//  4. A required field the owner did not say is ASKED, never invented — one
//     question at a time.
//  5. An update applies to exactly ONE row, identified before the confirmation;
//     several → the owner picks; none → said. A field is never emptied.

// Writer executes ONE confirmed write through the engine. The /api/ask handler
// binds it to the transaction cores (pkg/codegen) for the caller's tenant and
// role; tests implement it in memory. kind is create|update; id is the row
// (update only); the returned row is what the engine wrote.
type Writer interface {
	Write(ctx context.Context, kind, resource, id string, data map[string]any) (map[string]any, error)
}

// WriteError is the engine's refusal of a confirmed write, with its status
// (403 forbidden, 409 conflict, 422 validation…) and the per-field messages
// when validation failed — worded back to the owner, never masked as success.
type WriteError struct {
	Status int
	Msg    string
	Fields []FieldError
	// Conflicts (MOTOR-AGENDA-S1): the rows a refused write collided with under
	// a declared no-overlap rule — the reply names them.
	Conflicts []map[string]any
}

// FieldError is one failing field of a refused write.
type FieldError struct {
	Field   string
	Rule    string
	Message string
}

func (e *WriteError) Error() string { return e.Msg }

// PendingTTL bounds how long a write waits for its confirmation.
const PendingTTL = 5 * time.Minute

// Pending is one write waiting for the owner. Exactly one per (tenant, user):
// a new write replaces the previous one, and the owner is told.
type Pending struct {
	ID       string    `json:"id"`
	Key      string    `json:"-"` // tenant|user
	Expires  time.Time `json:"expires"`
	Kind     string    `json:"kind"` // create | update
	Resource string    `json:"resource"`
	RowID    string    `json:"row_id,omitempty"`    // update: the ONE row
	RowLabel string    `json:"row_label,omitempty"` // update: how the row is named
	// Data is what will be written: names already resolved to ids, time
	// tokens to timestamps. Labels remembers, per relation field, the name of
	// the chosen row for the wording.
	Data   map[string]any    `json:"data"`
	Labels map[string]string `json:"labels,omitempty"`
	// Stage: confirm (waiting for yes/no) | field (waiting for the value of
	// Field) | which (waiting for a pick among Options) | create_ref (waiting
	// for yes/no to create the row RefName in RefTarget for RefField).
	Stage   string      `json:"stage"`
	Field   string      `json:"field,omitempty"`
	Options []Candidate `json:"options,omitempty"`
	// which: what the pick is for — "row" (the update's row) or a relation
	// field name (a create/update value).
	WhichFor  string `json:"which_for,omitempty"`
	RefField  string `json:"ref_field,omitempty"`
	RefTarget string `json:"ref_target,omitempty"`
	RefName   string `json:"ref_name,omitempty"`
	// Lookup is the update's where as it is being settled (a picked name →
	// its id); Where the fully resolved filters the row lookup ran. Plan.Where
	// stays as the model said it (the reply, the history).
	Lookup []Filter `json:"-"`
	Where  []Filter `json:"where,omitempty"`
	// Plan / Question: the original, for the history and the trace.
	Plan     Plan   `json:"plan"`
	Question string `json:"question"`
	Asked    int    `json:"asked"` // follow-up questions so far (bounded)
	// Prior is the previous pending this one replaced (worded once).
	Prior string `json:"-"`
	// OpenRefs (VOZ-20) are the multi-field names still to resolve after a
	// pick settles one of them.
	OpenRefs []Ref `json:"-"`
}

// PendingStore holds the writes waiting for confirmation, in memory, one per
// key, expiring. A restart forgets them — a write that was not confirmed did
// not happen, which is the safe direction.
type PendingStore struct {
	mu   sync.Mutex
	byID map[string]*Pending
	byKy map[string]*Pending
}

func NewPendingStore() *PendingStore {
	return &PendingStore{byID: map[string]*Pending{}, byKy: map[string]*Pending{}}
}

// Put stores p under its key, replacing (and returning the description of)
// any previous pending of the same owner.
func (s *PendingStore) Put(p *Pending) (replaced *Pending) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	if old := s.byKy[p.Key]; old != nil {
		delete(s.byID, old.ID)
		replaced = old
	}
	if p.ID == "" {
		p.ID = newPendingID()
	}
	s.byID[p.ID] = p
	s.byKy[p.Key] = p
	return replaced
}

// Get returns the owner's pending write, if any and not expired.
func (s *PendingStore) Get(key string) *Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	return s.byKy[key]
}

// ByID returns a pending by id ONLY when it belongs to key — an id is never
// enough on its own (another user's id, a replayed id, are all "nothing").
func (s *PendingStore) ByID(id, key string) *Pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	p := s.byID[id]
	if p == nil || p.Key != key {
		return nil
	}
	return p
}

// Delete forgets a pending (executed or cancelled).
func (s *PendingStore) Delete(p *Pending) {
	if p == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur := s.byID[p.ID]; cur == p {
		delete(s.byID, p.ID)
		delete(s.byKy, p.Key)
	}
}

// Len is the number of live pendings (for /metrics and tests).
func (s *PendingStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep()
	return len(s.byID)
}

func (s *PendingStore) sweep() {
	now := time.Now()
	for id, p := range s.byID {
		if now.After(p.Expires) {
			delete(s.byID, id)
			if s.byKy[p.Key] == p {
				delete(s.byKy, p.Key)
			}
		}
	}
}

func newPendingID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ── the confirmation words ────────────────────────────────────────────────

// yesWords are the ONLY answers that execute — exact, after normalization.
// "sí pero…", "creo que sí", "sí mañana" are not a yes: they cancel. The
// list is closed on purpose; a broader reading is the ambiguous yes the brief
// forbids.
var yesWords = set("si", "sí", "confirmo", "confirmar", "confirmado", "dale", "ok", "okey", "okay", "listo", "de acuerdo", "correcto", "hacelo", "hazlo", "adelante", "afirmativo", "claro", "claro que si", "si dale", "si confirmo", "si hacelo", "si listo", "exacto", "yes")

// noWords cancel.
var noWords = set("no", "cancelar", "cancela", "cancelá", "cancelalo", "cancélalo", "olvidalo", "olvídalo", "dejalo", "déjalo", "nada", "negativo", "no gracias", "no no", "para", "pará", "parar", "stop", "no cancelar", "no cancela")

// IsYes / IsNo classify a confirmation answer.
func IsYes(text string) bool { return yesWords[normalize(text)] }
func IsNo(text string) bool  { return noWords[normalize(text)] }

// ── time tokens for VALUES ────────────────────────────────────────────────

// TimeTokens is the closed set a write may put in a time field (optionally
// followed by " HH:MM"), plus an ISO date the owner literally said. The
// engine does the arithmetic in the app's timezone; the model never writes a
// date it computed.
var TimeTokens = []string{"now", "today", "tomorrow", "day_after_tomorrow", "yesterday", "day_before_yesterday", "next_week", "next_monday", "next_tuesday", "next_wednesday", "next_thursday", "next_friday", "next_saturday", "next_sunday", "last_monday", "last_tuesday", "last_wednesday", "last_thursday", "last_friday", "last_saturday", "last_sunday", "end_of_month"}

var weekdayTokens = map[string]time.Weekday{"monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday, "sunday": time.Sunday}

var (
	isoDateRe = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2}))?$`)
	clockRe   = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
)

// ResolveTimeValue turns a token ("tomorrow", "next_friday 15:00",
// "2026-10-01") into a timestamp around now. A date without a clock is the
// START of that day in now's location. Returns the Spanish words for the
// confirmation ("mañana (lun 21 sep)").
func ResolveTimeValue(tok string, now time.Time) (time.Time, string, bool) {
	tok = strings.TrimSpace(strings.ToLower(tok))
	if tok == "" {
		return time.Time{}, "", false
	}
	loc := now.Location()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if m := isoDateRe.FindStringSubmatch(tok); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, loc)
		if t.Month() != time.Month(mo) || t.Day() != d {
			return time.Time{}, "", false
		}
		if m[4] != "" {
			h, _ := strconv.Atoi(m[4])
			mi, _ := strconv.Atoi(m[5])
			if h > 23 || mi > 59 {
				return time.Time{}, "", false
			}
			t = t.Add(time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute)
			return t, "el " + dateWords(t) + " a las " + t.Format("15:04"), true
		}
		return t, "el " + dateWords(t), true
	}
	base, clock, _ := strings.Cut(tok, " ")
	var t time.Time
	var words string
	switch base {
	case "now":
		return now, "ahora", true
	case "today":
		t, words = day, "hoy"
	case "tomorrow":
		t, words = day.AddDate(0, 0, 1), "mañana"
	case "day_after_tomorrow":
		t, words = day.AddDate(0, 0, 2), "pasado mañana"
	case "yesterday":
		t, words = day.AddDate(0, 0, -1), "ayer"
	case "day_before_yesterday":
		t, words = day.AddDate(0, 0, -2), "antier"
	case "next_week":
		monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
		t, words = monday.AddDate(0, 0, 7), "la semana que viene"
	case "end_of_month":
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		t, words = first.AddDate(0, 1, -1), "fin de mes"
	default:
		if wd, ok := weekdayTokens[strings.TrimPrefix(base, "last_")]; ok && strings.HasPrefix(base, "last_") {
			// The LAST occurrence, strictly before today («el martes» in a
			// note about what happened): a log looks back.
			delta := (int(day.Weekday()) - int(wd) + 7) % 7
			if delta == 0 {
				delta = 7
			}
			t, words = day.AddDate(0, 0, -delta), "el "+weekdayES(wd)+" pasado"
			break
		}
		wd, ok := weekdayTokens[strings.TrimPrefix(base, "next_")]
		if !ok || !strings.HasPrefix(base, "next_") {
			return time.Time{}, "", false
		}
		// The NEXT occurrence, strictly after today ("el viernes" said on a
		// Friday means the coming one, not today).
		delta := (int(wd) - int(day.Weekday()) + 7) % 7
		if delta == 0 {
			delta = 7
		}
		t, words = day.AddDate(0, 0, delta), "el "+weekdayES(wd)
	}
	if clock != "" {
		m := clockRe.FindStringSubmatch(clock)
		if m == nil {
			return time.Time{}, "", false
		}
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		if h > 23 || mi > 59 {
			return time.Time{}, "", false
		}
		t = t.Add(time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute)
		return t, words + " (" + dateWords(t) + ") a las " + t.Format("15:04"), true
	}
	if base == "today" {
		return t, words, true
	}
	return t, words + " (" + dateWords(t) + ")", true
}

var monthsES = [...]string{"", "ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}
var weekdaysShortES = [...]string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"}

func dateWords(t time.Time) string {
	return fmt.Sprintf("%s %d %s", weekdaysShortES[t.Weekday()], t.Day(), monthsES[t.Month()])
}

// spanishTimeToken maps what an OWNER types as a follow-up value ("mañana",
// "el viernes a las 3") to a token — the deterministic half, no model.
func spanishTimeToken(s string) string {
	raw := strings.ToLower(strings.TrimSpace(s))
	clock := ""
	// «mañana de 4 a 5», «el viernes a las 4 de la tarde», «a las 10»: the
	// clock phrase is read by the agenda parser (MOTOR-AGENDA-S1), which
	// knows «a las 4» means 16:00; the day words are what remains.
	if toks := tokenize(raw); len(toks) > 0 {
		if span, ok := consumeTimeSpan(toks); ok {
			clock = clockString(span.start)
			var rest []string
			for _, t := range toks {
				if !t.used {
					rest = append(rest, t.norm)
				}
			}
			raw = strings.TrimSpace(strings.Join(rest, " "))
			if raw == "" {
				raw = "hoy"
			}
		}
	}
	if i := strings.Index(raw, " a las "); i >= 0 && clock == "" {
		clock = strings.TrimSpace(raw[i+7:])
		raw = strings.TrimSpace(raw[:i])
		clock = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(clock, "hs"), "h"))
		if !strings.Contains(clock, ":") {
			clock += ":00"
		}
		if len(clock) == 4 {
			clock = "0" + clock
		}
		if !clockRe.MatchString(clock) {
			return ""
		}
	}
	n := normalize(raw)
	n = strings.TrimPrefix(n, "para ")
	n = strings.TrimPrefix(n, "el ")
	tok := ""
	switch n {
	case "ahora", "ya":
		tok = "now"
	case "hoy":
		tok = "today"
	case "manana", "mañana":
		tok = "tomorrow"
	case "pasado manana", "pasado mañana", "pasado":
		tok = "day_after_tomorrow"
	case "ayer":
		tok = "yesterday"
	case "semana que viene", "la semana que viene", "proxima semana", "la proxima semana", "semana proxima":
		tok = "next_week"
	case "fin de mes", "a fin de mes":
		tok = "end_of_month"
	case "lunes":
		tok = "next_monday"
	case "martes":
		tok = "next_tuesday"
	case "miercoles":
		tok = "next_wednesday"
	case "jueves":
		tok = "next_thursday"
	case "viernes":
		tok = "next_friday"
	case "sabado":
		tok = "next_saturday"
	case "domingo":
		tok = "next_sunday"
	default:
		if isoDateRe.MatchString(strings.TrimSpace(s)) {
			return strings.TrimSpace(s)
		}
		return ""
	}
	if clock != "" {
		return tok + " " + clock
	}
	return tok
}

// ── validation of a write plan ────────────────────────────────────────────

// validateWrite checks a create/update plan against the vocabulary: the
// resource exists and the role may write it, every data key is a writable
// field of the right type, relation fields carry a match, time fields a
// token, an update has a where and changes nothing to empty. Missing
// REQUIRED fields are NOT an error here — the engine asks the owner for
// them (the model must never invent one).
func (p Plan) validateWrite(v *Vocabulary) error {
	if p.Resource == "" {
		return fmt.Errorf("resource is required; the resources you may write are: %s", strings.Join(v.WritableNames(), ", "))
	}
	res := v.Resource(p.Resource)
	if res == nil {
		return fmt.Errorf("resource %q does not exist (or you may not read it); the resources you may write are: %s", p.Resource, strings.Join(v.WritableNames(), ", "))
	}
	if p.Kind == "create" && !res.CanCreate {
		return fmt.Errorf("the asking role may not create %s; the resources it may create are: %s", p.Resource, strings.Join(v.CreatableNames(), ", "))
	}
	if p.Kind == "update" && !res.CanUpdate {
		return fmt.Errorf("the asking role may not update %s; the resources it may update are: %s", p.Resource, strings.Join(v.UpdatableNames(), ", "))
	}
	if len(p.Data) == 0 && len(p.Refs) == 0 && !(p.Kind == "create" && titleField(res) != nil) {
		// A create that names only the resource («anotá una tarea») is
		// allowed when the engine can ask for its title.
		return fmt.Errorf("%s needs data: the fields the owner SAID, as {field: value}; fields of %s: %s", p.Kind, p.Resource, res.FieldList())
	}
	if p.Filters != nil || p.Period != nil || p.Field != "" || p.GroupBy != "" || p.Limit != 0 {
		return fmt.Errorf("%s takes only resource, data and (for update) where — no filters/period/field/group_by/limit", p.Kind)
	}
	for _, k := range sortedKeys(p.Data) {
		val := p.Data[k]
		fd := res.Field(k)
		if fd == nil {
			if k == "id" {
				return fmt.Errorf("data.id: the id is the engine's; identify a row with where, never write an id")
			}
			return fmt.Errorf("data.%s: %s has no field %q; it has: %s", k, p.Resource, k, res.FieldList())
		}
		if fd.Auto {
			return fmt.Errorf("data.%s is engine-owned (a timestamp the engine sets); leave it out", k)
		}
		if val == nil {
			return fmt.Errorf("data.%s: null is not a value; a voice write never empties a field — leave it out", k)
		}
		if err := checkWriteValue(v, p.Resource, fd, val); err != nil {
			return fmt.Errorf("data.%s: %v", k, err)
		}
	}
	for i, rf := range p.Refs {
		if strings.TrimSpace(rf.Match) == "" || len(rf.Fields) == 0 {
			return fmt.Errorf("refs[%d]: a ref takes a match and the candidate fields", i)
		}
		for _, name := range rf.Fields {
			fd := res.Field(name)
			if fd == nil || fd.Relation == "" || v.Resource(fd.Relation) == nil {
				return fmt.Errorf("refs[%d]: %q is not a relation of %s you may read", i, name, p.Resource)
			}
		}
	}
	if p.Kind == "create" {
		if sf := res.StateField(); sf != nil {
			if v, ok := p.Data[sf.Name].(string); ok && len(sf.Initial) > 0 && !contains(sf.Initial, v) {
				return fmt.Errorf("data.%s: a new row can only start in %s (declared initial states), not %q; leave it out to use the default", sf.Name, strings.Join(sf.Initial, "|"), v)
			}
		}
		if len(p.Where) > 0 {
			return fmt.Errorf("create takes no where")
		}
		return nil
	}
	// update
	if len(p.Where) == 0 {
		return fmt.Errorf("update needs where: the filters that identify the ONE row to change (a name via match, a state, a code…)")
	}
	if err := validateFilters(v, res, p.Resource, p.Where, "where"); err != nil {
		return err
	}
	return nil
}

// checkWriteValue type-checks one data value against its field.
func checkWriteValue(v *Vocabulary, resource string, fd *Field, val any) error {
	if fd.Relation != "" {
		m, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("%s.%s points at %s: write {\"match\": \"<the name as the owner said it>\"}, never a literal or an id", resource, fd.Name, fd.Relation)
		}
		name, _ := m["match"].(string)
		if strings.TrimSpace(name) == "" || len(m) != 1 {
			return fmt.Errorf("%s.%s: the object must be exactly {\"match\": \"<name>\"}", resource, fd.Name)
		}
		if v.Resource(fd.Relation) == nil {
			return fmt.Errorf("%s.%s points at %s, which you may not read", resource, fd.Name, fd.Relation)
		}
		return nil
	}
	switch fd.Type {
	case "string", "text":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("a %s field takes a string", fd.Type)
		}
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("an empty string is not a value; leave the field out")
		}
		if len(fd.Enum) > 0 && !contains(fd.Enum, s) {
			return fmt.Errorf("%q is not one of %s (verbatim)", s, strings.Join(fd.Enum, "|"))
		}
		if len([]rune(s)) > 500 {
			return fmt.Errorf("the value is longer than 500 characters")
		}
	case "int", "int64", "float64":
		switch x := val.(type) {
		case float64:
			if fd.Type != "float64" && x != float64(int64(x)) {
				return fmt.Errorf("%s takes a whole number", fd.Type)
			}
		case string:
			if !looksNumeric(x) {
				return fmt.Errorf("%q is not a number", x)
			}
		default:
			return fmt.Errorf("a %s field takes a number", fd.Type)
		}
	case "bool":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("a bool field takes true or false")
		}
	case "time":
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("a time field takes a token: %s, optionally followed by \" HH:MM\", or an ISO date the owner literally said", strings.Join(TimeTokens, "|"))
		}
		if _, _, ok := ResolveTimeValue(s, time.Now()); !ok {
			return fmt.Errorf("%q is not a time token; use %s (optionally \" HH:MM\") or YYYY-MM-DD", s, strings.Join(TimeTokens, "|"))
		}
	case "uuid":
		return fmt.Errorf("a uuid field is never written by voice")
	default: // json, jsonb, file
		return fmt.Errorf("a %s field is never written by voice", fd.Type)
	}
	return nil
}

// WritableNames / CreatableNames / UpdatableNames list what the role may write.
func (v *Vocabulary) WritableNames() []string {
	var out []string
	for _, n := range v.order {
		if r := v.resources[n]; r.CanCreate || r.CanUpdate {
			out = append(out, n)
		}
	}
	return out
}
func (v *Vocabulary) CreatableNames() []string {
	var out []string
	for _, n := range v.order {
		if v.resources[n].CanCreate {
			out = append(out, n)
		}
	}
	return out
}
func (v *Vocabulary) UpdatableNames() []string {
	var out []string
	for _, n := range v.order {
		if v.resources[n].CanUpdate {
			out = append(out, n)
		}
	}
	return out
}

// Writable reports whether any resource of the vocabulary may be written.
func (v *Vocabulary) Writable() bool { return len(v.WritableNames()) > 0 }

// ── preparing a write: resolve, complete, ask, confirm ────────────────────

// prepareWrite turns a validated write plan into a Pending and the reply
// that asks for the confirmation (or for a missing field, or for a pick).
func prepareWrite(ctx context.Context, d Deps, p Plan, question string) Result {
	if d.Write == nil || d.Pending == nil {
		return Result{Kind: "write_refused", Headline: "Por acá solo leo", Detail: "no writer bound",
			Text: "🔒 Por acá solo <b>leo</b>. Las escrituras por voz no están activadas en esta app (APPXIMO_ASK_WRITES=off)."}
	}
	if d.PendingKey == "" {
		// A write needs to know WHO confirms it: a token without a subject
		// (a bare `appximo token` with no --user) can read, not write.
		return Result{Kind: "write_refused", Headline: "Tu token no dice quién sos", Detail: "no identity for the pending key",
			Text: "🔒 Para escribir necesito saber <b>quién</b> confirma, y este token no trae identidad (sin usuario). Iniciá sesión, o mintiéndolo pasá <code>--user-id</code>."}
	}
	res := d.Vocab.Resource(p.Resource)
	pend := &Pending{
		Key: d.PendingKey, Expires: time.Now().Add(PendingTTL), Kind: p.Kind, Resource: p.Resource,
		Data: map[string]any{}, Labels: map[string]string{}, Plan: p, Question: question, Stage: "confirm",
	}
	pend.Lookup = append([]Filter(nil), p.Where...)
	// Literal values first (time tokens resolved now, in the app's day).
	for _, k := range sortedKeys(p.Data) {
		fd := res.Field(k)
		val := p.Data[k]
		if fd.Relation != "" {
			continue // resolved below, may need a question
		}
		if p.Kind == "create" && fd.HasDefault && fmt.Sprint(fd.Default) == fmt.Sprint(val) {
			// A value equal to the field's declared default adds nothing the
			// engine would not write anyway — and the model tends to fill the
			// state field unprompted ("estado": "pendiente"). Left out, the
			// confirmation shows only what the owner determined.
			continue
		}
		v, label, err := resolveLiteral(fd, val, d.Now)
		if err != nil {
			return Result{Kind: "unclear", Headline: "No entendí", Detail: "write value: " + err.Error(),
				Text: "🤔 <b>No entendí</b> uno de los valores (" + esc(k) + "). Probá con otras palabras."}
		}
		pend.Data[k] = v
		if label != "" {
			pend.Labels[k] = label
		}
	}
	// Relation fields: the name → the row, before anything is confirmed.
	for _, k := range sortedKeys(p.Data) {
		fd := res.Field(k)
		if fd.Relation == "" {
			continue
		}
		name := p.Data[k].(map[string]any)["match"].(string)
		if r, done := resolveRef(ctx, d, pend, fd, name); done {
			return r
		}
	}
	// Names the parser could not place (VOZ-20): tried against every
	// candidate target; a bare word that is nothing anywhere stays in the
	// title rather than refusing the write.
	if r, done := resolveRefs(ctx, d, pend, res, p.Refs); done {
		return r
	}
	if p.Kind == "update" {
		if r, done := resolveRow(ctx, d, pend); done {
			return r
		}
	}
	fillRangeEnd(res, pend, d.Now.Location()) // MOTOR-AGENDA-S1: «a las 10» lasts the default
	r := finishPending(ctx, d, pend)
	if p.Kind == "create" && p.Reason != "" && r.Pending != nil {
		// Two intentions in one sentence: the first is what this pending
		// holds; the second is said back so it is asked apart.
		r.Text = "<i>Entendí lo primero. Lo segundo («" + esc(p.Reason) + "») decímelo aparte cuando confirmes.</i>\n" + r.Text
		r.Speech = "Entendí lo primero. Lo segundo decímelo aparte cuando confirmes. " + r.Speech
	}
	return r
}

// resolveLiteral converts a plan literal to the value the engine will store
// and the words the confirmation shows.
func resolveLiteral(fd *Field, val any, now time.Time) (any, string, error) {
	switch fd.Type {
	case "time":
		t, words, ok := ResolveTimeValue(val.(string), now)
		if !ok {
			return nil, "", fmt.Errorf("bad time token %v", val)
		}
		return t.UTC().Format(time.RFC3339), words, nil
	case "int", "int64":
		switch x := val.(type) {
		case float64:
			return int64(x), "", nil
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
			if err != nil {
				f, ferr := strconv.ParseFloat(strings.TrimSpace(x), 64)
				if ferr != nil {
					return nil, "", err
				}
				n = int64(f)
			}
			return n, "", nil
		}
	case "float64":
		if s, ok := val.(string); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			return f, "", err
		}
	case "string", "text":
		return strings.TrimSpace(val.(string)), "", nil
	}
	return val, "", nil
}

// resolveRef matches a dictated name for a relation field against the
// target's rows. One → stored and worded; several → the owner picks (stage
// which); none → the owner is offered to create it (stage create_ref) when
// the target can be created by voice, else told.
func resolveRef(ctx context.Context, d Deps, pend *Pending, fd *Field, name string) (Result, bool) {
	target := d.Vocab.Resource(fd.Relation)
	cands, err := fetchCandidates(ctx, d, target, fd, name)
	if err != nil {
		return execFailure(err), true
	}
	dec := Decide(Match(name, cands, nil))
	switch dec.Kind {
	case "one":
		pend.Data[fd.Name] = dec.Chosen.Value
		pend.Labels[fd.Name] = dec.Chosen.Label
		return Result{}, false
	case "several":
		pend.Stage, pend.WhichFor, pend.Options, pend.RefName = "which", fd.Name, dec.Options, name
		d.Pending.Put(pend)
		return pendingResult(pend, "ambiguous", "¿Cuál?",
			fmt.Sprintf("🤔 Hay varios %s que se parecen a «%s». ¿Cuál?\n%s\n\nRespondé con el número o el nombre completo (o <b>no</b> para cancelar).", esc(fd.Relation), esc(name), numbered(dec.Options))), true
	default:
		hint := ""
		if len(dec.Options) > 0 {
			hint = "\n¿Quisiste decir?\n" + numbered(dec.Options) + "\n\nRespondé con el número"
		}
		canCreate := target.CanCreate && creatableByName(target)
		if canCreate {
			pend.Stage, pend.RefField, pend.RefTarget, pend.RefName = "create_ref", fd.Name, fd.Relation, name
			if len(dec.Options) > 0 {
				pend.Options, pend.WhichFor = dec.Options, fd.Name
			}
			d.Pending.Put(pend)
			sep := ""
			if hint != "" {
				sep = ", o "
			} else {
				hint = "\n\n"
			}
			return pendingResult(pend, "not_found", "No encuentro «"+name+"»",
				fmt.Sprintf("🤷 No encuentro ningún %s que se llame «%s».%s%s<b>sí</b> para crearlo con ese nombre (<b>no</b> cancela).", esc(singular(fd.Relation)), esc(name), hint, sep)), true
		}
		if len(dec.Options) > 0 {
			pend.Stage, pend.WhichFor, pend.Options, pend.RefName = "which", fd.Name, dec.Options, name
			d.Pending.Put(pend)
			return pendingResult(pend, "not_found", "No encuentro «"+name+"»",
				fmt.Sprintf("🤷 No encuentro ningún %s que se llame «%s».%s (o <b>no</b> para cancelar).", esc(singular(fd.Relation)), esc(name), hint)), true
		}
		return Result{Kind: "not_found", Headline: "No encuentro «" + name + "»",
			Text: fmt.Sprintf("🤷 No encuentro ningún %s que se llame «%s», y por voz no puedo crearlo. Cargalo primero y volvé a decirme.", esc(singular(fd.Relation)), esc(name))}, true
	}
}

// creatableByName reports whether a target resource can be born from its
// name alone: a label field exists and every other required field has a
// default (or is engine-owned).
func creatableByName(r *Resource) bool {
	labels := r.LabelFields()
	if len(labels) == 0 {
		return false
	}
	for _, f := range r.Fields {
		if f.Required && !f.HasDefault && !f.Auto && f.Name != labels[0] {
			return false
		}
	}
	return true
}

// resolveRow finds the ONE row an update applies to: names in where are
// matched first, then the filters run through the engine's own list read
// (RBAC-scoped). One → kept; several → the owner picks; none → said.
func resolveRow(ctx context.Context, d Deps, pend *Pending) (Result, bool) {
	res := d.Vocab.Resource(pend.Resource)
	// Names in where: one → its id; several → the owner PICKS (a write never
	// says "repetí la pregunta" — the pick is a stage of the same pending);
	// none → said.
	pend.Where = append([]Filter(nil), pend.Lookup...)
	// multi tries a name against several fields (relations first, the row's
	// own title last); it answers (result, handled) — handled=false means
	// the name was placed and the loop goes on.
	multi := func(i int, f Filter, fields []string) (Result, bool, error) {
		kind, field, chosen, opts, err := resolveMulti(ctx, d, res, f.Match, fields)
		if err != nil {
			return Result{}, false, err
		}
		switch kind {
		case "one":
			pend.Lookup[i] = Filter{Field: field, Op: "eq", Value: chosen.Value}
			pend.Where[i] = pend.Lookup[i]
			pend.Labels["__where_"+field] = chosen.Label
			return Result{}, false, nil
		case "several", "maybe":
			pend.Stage, pend.WhichFor, pend.Options, pend.RefName = "which", "wheremulti", opts, f.Match
			d.Pending.Put(pend)
			return pendingResult(pend, "ambiguous", "¿Cuál?",
				fmt.Sprintf("🤔 «%s» puede ser más de una cosa. ¿Cuál?\n%s\n\nRespondé con el número (o <b>no</b> para cancelar).", esc(f.Match), numberedKinds(opts))), true, nil
		}
		return Result{Kind: "not_found", Headline: "No encuentro «" + f.Match + "»",
			Text: fmt.Sprintf("🤷 No encuentro «%s» como %s, así que no sé qué %s cambiar.", esc(f.Match), esc(kindsWords(d.Vocab, res, fields)), esc(singular(pend.Resource)))}, true, nil
	}
	for i, f := range pend.Where {
		if f.Match == "" {
			continue
		}
		if len(f.Fields) > 0 {
			r, handled, err := multi(i, f, f.Fields)
			if err != nil {
				return execFailure(err), true
			}
			if handled {
				return r, true
			}
			continue
		}
		fd := res.Field(f.Field)
		target, targetName := res, pend.Resource
		if fd.Relation != "" {
			target, targetName = d.Vocab.Resource(fd.Relation), fd.Relation
		}
		cands, err := fetchCandidates(ctx, d, target, fd, f.Match)
		if err != nil {
			return execFailure(err), true
		}
		dec := Decide(Match(f.Match, cands, nil))
		switch dec.Kind {
		case "one":
			pend.Where[i] = Filter{Field: f.Field, Op: "eq", Value: dec.Chosen.Value}
			if fd.Relation != "" {
				pend.Labels["__where_"+f.Field] = dec.Chosen.Label
			}
		case "several", "maybe":
			pend.Stage, pend.WhichFor, pend.Options, pend.RefName = "which", "where:"+f.Field, dec.Options, f.Match
			d.Pending.Put(pend)
			lead := "Hay varios"
			if dec.Kind == "maybe" {
				lead = "No encuentro exactamente «" + esc(f.Match) + "»; hay"
			}
			return pendingResult(pend, "ambiguous", "¿Cuál?",
				fmt.Sprintf("🤔 %s %s que se parecen a «%s». ¿Cuál?\n%s\n\nRespondé con el número o el nombre completo (o <b>no</b> para cancelar).", lead, esc(targetName), esc(f.Match), numbered(dec.Options))), true
		default:
			if own := ownNameField(res); fd.Relation != "" && own != "" && own != f.Field {
				// the relation holds no such name: the row's OWN title may
				// («cancelá la tarea sobre el agua» is «pagar la factura del
				// agua», not a person) — tried before giving up
				r, handled, err := multi(i, f, []string{f.Field, own})
				if err != nil {
					return execFailure(err), true
				}
				if handled {
					return r, true
				}
				continue
			}
			return Result{Kind: "not_found", Headline: "No encuentro «" + f.Match + "»",
				Text: fmt.Sprintf("🤷 No encuentro ningún %s que se llame «%s». Nada que cambiar.", esc(singular(targetName)), esc(f.Match))}, true
		}
	}
	params := url.Values{"per_page": {"6"}}
	for _, f := range pend.Where {
		fd := res.Field(f.Field)
		if f.Op == "is_null" {
			v := "true"
			if b, ok := f.Value.(bool); ok && !b {
				v = "false"
			}
			params.Set("filter["+f.Field+"][is_null]", v)
			continue
		}
		val := literal(f.Value)
		if fd != nil && fd.Type == "time" && val == "now" {
			val = d.Now.UTC().Format(time.RFC3339)
		}
		params.Set("filter["+f.Field+"]["+f.Op+"]", val)
	}
	cols := listColumns(res)
	params.Set("fields", strings.Join(cols, ","))
	rows, _, err := d.Exec.List(ctx, pend.Resource, params)
	if err != nil {
		return execFailure(err), true
	}
	labels := res.LabelFields()
	var opts []Candidate
	for _, row := range rows {
		id := fmt.Sprint(row["id"])
		label := rowWords(res, row, labels, d.Now.Location())
		opts = append(opts, Candidate{ID: id, Label: label, Value: id})
	}
	switch len(opts) {
	case 0:
		return Result{Kind: "not_found", Headline: "No encuentro esa fila",
			Text: fmt.Sprintf("🤷 No encuentro ningún %s que cumpla eso (%s). Nada que cambiar.", esc(singular(pend.Resource)), esc(describeWhere(res, pend.Plan.Where)))}, true
	case 1:
		pend.RowID, pend.RowLabel = opts[0].ID, opts[0].Label
		return checkTransition(d, pend, rows[0])
	default:
		pend.Stage, pend.WhichFor, pend.Options = "which", "row", opts
		d.Pending.Put(pend)
		more := ""
		if len(opts) > 5 {
			more = "\n(hay más; afiná la descripción)"
			pend.Options = opts[:5]
		}
		return pendingResult(pend, "ambiguous", "¿Cuál?",
			fmt.Sprintf("🤔 Hay varios %s que cumplen eso. ¿Cuál cambio?\n%s%s\n\nRespondé con el número (o <b>no</b> para cancelar).", esc(pend.Resource), numbered(pend.Options), more)), true
	}
}

// checkTransition pre-checks a state change against the declared machine so
// the owner hears "ya está hecha" before confirming, not a 422 after. The
// engine's guard still enforces it at execution.
func checkTransition(d Deps, pend *Pending, row map[string]any) (Result, bool) {
	res := d.Vocab.Resource(pend.Resource)
	sf := res.StateField()
	if sf == nil {
		return Result{}, false
	}
	to, ok := pend.Data[sf.Name].(string)
	if !ok {
		return Result{}, false
	}
	from, _ := row[sf.Name].(string)
	if from == "" || from == to {
		if from == to {
			return Result{Kind: "answer", Headline: "Ya está así",
				Text: fmt.Sprintf("ℹ️ %s «%s» ya está en <b>%s</b>. Nada que cambiar.", esc(strings.Title(singular(pend.Resource))), esc(pend.RowLabel), esc(to))}, true //nolint:staticcheck
		}
		return Result{}, false
	}
	pend.Labels["__from_"+sf.Name] = from
	if sf.Transitions != nil && !contains(sf.Transitions[from], to) {
		allowed := sf.Transitions[from]
		msg := fmt.Sprintf("🔒 %s «%s» está en <b>%s</b> y desde ahí no puede pasar a <b>%s</b>", esc(strings.Title(singular(pend.Resource))), esc(pend.RowLabel), esc(from), esc(to)) //nolint:staticcheck
		if len(allowed) == 0 {
			msg += " (es un estado final)."
		} else {
			msg += "; puede pasar a " + esc(strings.Join(allowed, " / ")) + "."
		}
		return Result{Kind: "forbidden", Headline: "Ese cambio no está permitido", Text: msg}, true
	}
	return Result{}, false
}

// finishPending checks the required fields (asking for the first missing
// one) and, when complete, stores the pending and words the confirmation.
func finishPending(ctx context.Context, d Deps, pend *Pending) Result {
	res := d.Vocab.Resource(pend.Resource)
	if pend.Kind == "create" {
		for _, f := range confirmationOrder(res) {
			if !f.Required || f.HasDefault || f.Auto {
				continue
			}
			if _, ok := pend.Data[f.Name]; ok {
				continue
			}
			if f.Relation == "" && (f.Type == "uuid" || f.Type == "json" || f.Type == "jsonb" || f.Type == "file") {
				return Result{Kind: "write_refused", Headline: "No puedo crear eso por voz",
					Text: fmt.Sprintf("🔒 Para crear %s hace falta <b>%s</b>, que no se puede dictar. Cargalo desde la app.", esc(singular(pend.Resource)), esc(f.Name))}
			}
			if pend.Asked >= 4 {
				return Result{Kind: "unclear", Headline: "Demasiadas preguntas",
					Text: "🤔 Me faltan demasiados datos para crear eso por voz. Decímelo completo en una sola frase."}
			}
			pend.Stage, pend.Field, pend.Asked = "field", f.Name, pend.Asked+1
			d.Pending.Put(pend)
			r := pendingResult(pend, "ask_field", "¿"+fieldWords(f)+"?", askFieldText(pend, f))
			r.Speech = askFieldSpeech(pend, f)
			return r
		}
	}
	pend.Stage = "confirm"
	// The agenda (MOTOR-AGENDA-S1): before the owner confirms, the collision
	// check — «Ya tenés X de 4 a 5. ¿Igual lo agendo?».
	if r, done := checkConflicts(ctx, d, pend); done {
		return r
	}
	replaced := d.Pending.Put(pend)
	note := ""
	if replaced != nil && replaced.ID != pend.ID && replaced.Stage == "confirm" {
		note = "<i>(Cancelé lo anterior, que seguía sin confirmar.)</i>\n"
	}
	text := note + confirmationText(d, pend)
	r := pendingResult(pend, "confirm", "¿Confirmás?", text)
	r.Speech = spokenConfirmation(d, pend)
	return r
}

// spokenConfirmation is the contract read aloud: «Voy a crear una tarea.
// Título: llamar a Fabián. Persona: Fabián Gómez. Urgente: sí. Vence mañana,
// lunes veintiuno de septiembre. ¿Confirmás?»
func spokenConfirmation(d Deps, pend *Pending) string {
	res := d.Vocab.Resource(pend.Resource)
	loc := d.Now.Location()
	var sp []string
	if pend.Kind == "create" {
		sp = append(sp, "Voy a crear "+singularWord(pend.Resource)+".")
	} else {
		// the row's label as a voice says it: «hacer la declaración de
		// renta, pendiente, veintitrés de septiembre» — no parentheses
		sp = append(sp, "Voy a cambiar "+singularWord(pend.Resource)+", "+strings.NewReplacer(" (", ", ", "(", "", ")", "").Replace(pend.RowLabel)+".")
	}
	for _, f := range confirmationOrder(res) {
		v, ok := pend.Data[f.Name]
		if !ok {
			continue
		}
		val := spokenValue(f, v, pend.Labels[f.Name], loc)
		fw := fieldWords(f)
		if from, ok := pend.Labels["__from_"+f.Name]; ok && pend.Kind == "update" {
			sp = append(sp, sentence(fw+": de "+spokenWord(from)+" a "+spokenWord(val)))
			continue
		}
		sp = append(sp, sentence(fw+": "+spokenWord(val)))
	}
	if pend.Labels["__conflict"] != "" {
		sp = append(sp, "¿Igual lo agendo?")
	} else {
		sp = append(sp, "¿Confirmás?")
	}
	return SpokenNumbers(strings.Join(sp, " "))
}

// spokenValue says one value for a voice: a time as day and clock words, a
// bool as sí/no, a name as the row it resolved to.
func spokenValue(f *Field, v any, label string, loc *time.Location) string {
	if f.Type == "time" {
		if s, ok := v.(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				t = t.In(loc)
				words := DateWordsLong(t)
				if strings.HasPrefix(label, "hoy") {
					words = "hoy"
				} else if strings.HasPrefix(label, "mañana") {
					words = "mañana, " + words
				} else if strings.HasPrefix(label, "pasado mañana") {
					words = "pasado mañana, " + words
				}
				if t.Hour() != 0 || t.Minute() != 0 {
					words += " a " + ClockWords(t.Hour(), t.Minute())
				}
				return words
			}
		}
	}
	if b, ok := v.(bool); ok {
		if b {
			return "sí"
		}
		return "no"
	}
	if f.IsNumeric() && !f.Money {
		// a small whole number is said in words («treinta»); money and
		// large figures keep their digits (the text carries them)
		switch n := v.(type) {
		case float64:
			if n == float64(int(n)) && n >= 0 && n <= 999 {
				return NumberWords(int(n))
			}
		case int:
			if n >= 0 && n <= 999 {
				return NumberWords(n)
			}
		case int64:
			if n >= 0 && n <= 999 {
				return NumberWords(int(n))
			}
		}
	}
	if label != "" {
		return label
	}
	return formatValue(f, v, loc)
}

func pendingResult(pend *Pending, kind, headline, text string) Result {
	return Result{Kind: kind, Headline: headline, Text: text, Speech: Speech(text), Pending: pend, Plan: &pend.Plan}
}

// askFieldText words the question for one missing field.
func askFieldText(pend *Pending, f *Field) string {
	what := fieldWords(f)
	switch {
	case len(f.Enum) > 0:
		return fmt.Sprintf("📝 Para crear %s me falta <b>%s</b>. ¿Cuál? (%s)", esc(singular(pend.Resource)), esc(what), esc(strings.Join(f.Enum, " / ")))
	case f.Relation != "":
		return fmt.Sprintf("📝 Para crear %s me falta <b>%s</b>. ¿Cuál %s? Decime el nombre.", esc(singular(pend.Resource)), esc(what), esc(singular(f.Relation)))
	case f.Type == "time":
		return fmt.Sprintf("📝 Para crear %s me falta <b>%s</b>. ¿Cuándo? (hoy, mañana, el viernes, el viernes a las 15…)", esc(singular(pend.Resource)), esc(what))
	case f.IsNumeric():
		return fmt.Sprintf("📝 Para crear %s me falta <b>%s</b>. ¿Cuánto?", esc(singular(pend.Resource)), esc(what))
	case f.Type == "bool":
		return fmt.Sprintf("📝 Para crear %s me falta <b>%s</b>. ¿Sí o no?", esc(singular(pend.Resource)), esc(what))
	}
	return fmt.Sprintf("📝 Para crear %s me falta <b>%s</b>. ¿Qué pongo?", esc(singular(pend.Resource)), esc(what))
}

// askFieldSpeech is the same question for a voice: the examples said as a
// person says them, never a clock in digits.
func askFieldSpeech(pend *Pending, f *Field) string {
	what := fieldWords(f)
	lead := "Para crear " + singularWord(pend.Resource) + " me falta " + what + "."
	switch {
	case len(f.Enum) > 0:
		return lead + " ¿Cuál? Puede ser " + joinSpoken(spokenWords(f.Enum)) + "."
	case f.Relation != "":
		return lead + " ¿Cuál " + singular(f.Relation) + "? Decime el nombre."
	case f.Type == "time":
		return lead + " ¿Cuándo? Por ejemplo hoy, mañana, el viernes, o el viernes a las tres de la tarde."
	case f.IsNumeric():
		return lead + " ¿Cuánto?"
	case f.Type == "bool":
		return lead + " ¿Sí o no?"
	}
	return lead + " ¿Qué pongo?"
}

func spokenWords(ws []string) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = spokenWord(w)
	}
	return out
}

func fieldWords(f *Field) string {
	n := strings.ReplaceAll(f.Name, "_", " ")
	if f.Relation != "" {
		n = strings.TrimSuffix(n, " id")
	}
	return n
}

// confirmationText is the contract: EVERY value that will be written, with
// matched names shown as the row they resolved to.
func confirmationText(d Deps, pend *Pending) string {
	res := d.Vocab.Resource(pend.Resource)
	var b strings.Builder
	if pend.Kind == "create" {
		fmt.Fprintf(&b, "📝 Voy a crear <b>%s</b>:\n", esc(singular(pend.Resource)))
	} else {
		fmt.Fprintf(&b, "✏️ Voy a cambiar <b>%s</b> «%s»:\n", esc(singular(pend.Resource)), esc(pend.RowLabel))
	}
	for _, f := range res.Fields {
		v, ok := pend.Data[f.Name]
		if !ok {
			continue
		}
		line := "• " + esc(fieldWords(f)) + ": "
		if from, ok := pend.Labels["__from_"+f.Name]; ok && pend.Kind == "update" {
			line += esc(from) + " → "
		}
		line += "<b>" + esc(valueWords(f, v, pend.Labels[f.Name], d.Now.Location())) + "</b>"
		b.WriteString(line + "\n")
	}
	if pend.Labels["__conflict"] != "" {
		b.WriteString("\n¿Igual lo agendo? (<b>sí</b> / <b>no</b>)")
		return b.String()
	}
	b.WriteString("\n¿Confirmás? (<b>sí</b> / <b>no</b>)")
	return b.String()
}

// valueWords is one value as the owner reads it.
func valueWords(f *Field, v any, label string, loc *time.Location) string {
	if label != "" {
		return label
	}
	if f.Type == "time" {
		if s, ok := v.(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t.In(loc).Format("02 Jan 15:04")
			}
		}
	}
	return formatValue(f, v, loc)
}

// rowWords labels a row for a pick or a confirmation: its label fields, then
// its state and date when they help tell rows apart.
func rowWords(res *Resource, row map[string]any, labels []string, loc *time.Location) string {
	label := labelOf(row, labels)
	if label == "" {
		label = fmt.Sprint(row["id"])
		if len(label) > 8 {
			label = label[:8]
		}
	}
	var extra []string
	if sf := res.StateField(); sf != nil {
		if s, ok := row[sf.Name].(string); ok && s != "" {
			extra = append(extra, s)
		}
	}
	if tf := res.DefaultTimeField(); tf != "" {
		if s, ok := row[tf].(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				extra = append(extra, t.In(loc).Format("02 Jan"))
			}
		} else if t, ok := row[tf].(time.Time); ok {
			extra = append(extra, t.In(loc).Format("02 Jan"))
		}
	}
	if len(extra) > 0 {
		label += " (" + strings.Join(extra, ", ") + ")"
	}
	return label
}

func describeWhere(res *Resource, where []Filter) string {
	var parts []string
	for _, f := range where {
		if f.Match != "" {
			parts = append(parts, f.Field+" ≈ "+f.Match)
			continue
		}
		parts = append(parts, describeFilter(res.Field(f.Field), f))
	}
	return strings.Join(parts, " · ")
}

func numbered(cs []Candidate) string {
	var b strings.Builder
	for i, c := range cs {
		fmt.Fprintf(&b, "%d. %s\n", i+1, esc(c.Label))
	}
	return strings.TrimRight(b.String(), "\n")
}

// ── continuing a pending: the owner's next message ────────────────────────

// continuePending interprets the owner's message against their pending
// write. It returns handled=false when the message is not an answer to the
// pending (the pending is then CANCELLED and the message is processed as a
// new question — the safe reading of an ambiguous reply).
func continuePending(ctx context.Context, d Deps, pend *Pending, text string) (Result, bool) {
	if IsNo(text) {
		d.Pending.Delete(pend)
		return Result{Kind: "cancelled", Headline: "Cancelado", Plan: &pend.Plan,
			Text: "👌 Cancelado. No escribí nada."}, true
	}
	switch pend.Stage {
	case "confirm":
		if IsYes(text) {
			return executePending(ctx, d, pend), true
		}
		// «no, mejor el viernes» / «sí pero urgente»: a correction the form
		// recognizes re-issues the confirmation (AGENDA-ASISTENTE-S1).
		if r, ok := applyCorrection(ctx, d, pend, text); ok {
			return r, true
		}
		d.Pending.Delete(pend)
		return Result{}, false
	case "field":
		res := d.Vocab.Resource(pend.Resource)
		f := res.Field(pend.Field)
		if f.Relation != "" {
			if r, done := resolveRef(ctx, d, pend, f, strings.TrimSpace(text)); done {
				return r, true
			}
			pend.Stage, pend.Field = "confirm", ""
			return finishPending(ctx, d, pend), true
		}
		v, label, err := coerceSaid(f, text, d.Now)
		if err != nil {
			if pend.Asked >= 4 {
				d.Pending.Delete(pend)
				return Result{Kind: "cancelled", Headline: "Cancelado",
					Text: "🤔 No logré entender ese dato. Cancelé la escritura; decímelo completo en una sola frase."}, true
			}
			pend.Asked++
			d.Pending.Put(pend)
			return pendingResult(pend, "ask_field", "¿"+fieldWords(f)+"?", "🤔 "+esc(err.Error())+"\n"+askFieldText(pend, f)), true
		}
		pend.Data[f.Name] = v
		if label != "" {
			pend.Labels[f.Name] = label
		}
		// A re-said start of a range moves its end by the same span
		// (MOTOR-AGENDA-S1); «de 4 a 5» in the answer sets both.
		if rg := res.Range(); rg != nil && f.Name == rg.Start {
			if endTok, ok := spanishTimeSpanEnd(text); ok {
				if t, w, ok := ResolveTimeValue(endTok, d.Now); ok {
					pend.Data[rg.End], pend.Labels[rg.End] = t.UTC().Format(time.RFC3339), w
				}
			} else {
				delete(pend.Data, rg.End)
				delete(pend.Labels, rg.End)
			}
		}
		pend.Stage, pend.Field = "confirm", ""
		fillRangeEnd(res, pend, d.Now.Location())
		return finishPending(ctx, d, pend), true
	case "which":
		pick := pickOption(text, pend.Options)
		if pick == nil {
			// Not a pick: an answer to something else → cancel and move on.
			d.Pending.Delete(pend)
			return Result{}, false
		}
		switch {
		case pend.WhichFor == "row":
			pend.RowID, pend.RowLabel = pick.ID, pick.Label
		case pend.WhichFor == "ref" && pick.Field != "":
			// VOZ-20: the picked option settles WHICH field the name was.
			pend.Data[pick.Field] = pick.Value
			pend.Labels[pick.Field] = pick.Label
			pend.Stage, pend.WhichFor, pend.Options = "confirm", "", nil
			if r, done := resolveRefs(ctx, d, pend, d.Vocab.Resource(pend.Resource), pend.OpenRefs); done {
				return r, true
			}
			return finishPending(ctx, d, pend), true
		case pend.WhichFor == "wheremulti" && pick.Field != "":
			for i, f := range pend.Lookup {
				if len(f.Fields) > 0 && f.Match != "" {
					pend.Lookup[i] = Filter{Field: pick.Field, Op: "eq", Value: pick.Value}
					pend.Labels["__where_"+pick.Field] = pick.Label
					break
				}
			}
			pend.Stage, pend.WhichFor, pend.Options = "confirm", "", nil
			if r, done := resolveRow(ctx, d, pend); done {
				if r.Pending == nil {
					d.Pending.Delete(pend)
				}
				return r, true
			}
			return finishPending(ctx, d, pend), true
		case strings.HasPrefix(pend.WhichFor, "where:"):
			// The picked row settles one where-name; the row lookup runs
			// again from the top (other names, then the ONE row).
			field := strings.TrimPrefix(pend.WhichFor, "where:")
			for i, f := range pend.Lookup {
				if f.Field == field && f.Match != "" {
					pend.Lookup[i] = Filter{Field: field, Op: "eq", Value: pick.Value}
					pend.Labels["__where_"+field] = pick.Label
				}
			}
			pend.Stage, pend.WhichFor, pend.Options = "confirm", "", nil
			if r, done := resolveRow(ctx, d, pend); done {
				if r.Pending == nil {
					d.Pending.Delete(pend)
				}
				return r, true
			}
			return finishPending(ctx, d, pend), true
		default:
			pend.Data[pend.WhichFor] = pick.Value
			pend.Labels[pend.WhichFor] = pick.Label
		}
		pend.Stage, pend.WhichFor, pend.Options = "confirm", "", nil
		if pend.Kind == "update" && pend.RowID != "" {
			// Re-read the chosen row for the transition pre-check.
			if row := fetchRow(ctx, d, pend); row != nil {
				if r, done := checkTransition(d, pend, row); done {
					d.Pending.Delete(pend)
					return r, true
				}
			}
		}
		return finishPending(ctx, d, pend), true
	case "create_ref":
		if pick := pickOption(text, pend.Options); pick != nil {
			pend.Data[pend.RefField] = pick.Value
			pend.Labels[pend.RefField] = pick.Label
			pend.Stage, pend.Options, pend.WhichFor, pend.RefField, pend.RefTarget, pend.RefName = "confirm", nil, "", "", "", ""
			return finishPending(ctx, d, pend), true
		}
		if !IsYes(text) {
			d.Pending.Delete(pend)
			return Result{}, false
		}
		target := d.Vocab.Resource(pend.RefTarget)
		labels := target.LabelFields()
		row, err := d.Write.Write(ctx, "create", pend.RefTarget, "", map[string]any{labels[0]: pend.RefName})
		if err != nil {
			d.Pending.Delete(pend)
			return writeFailure(err, "crear "+singular(pend.RefTarget)), true
		}
		pend.Data[pend.RefField] = fmt.Sprint(row["id"])
		pend.Labels[pend.RefField] = pend.RefName + " (nuevo)"
		pend.Stage, pend.Options, pend.WhichFor, pend.RefField, pend.RefTarget, pend.RefName = "confirm", nil, "", "", "", ""
		r := finishPending(ctx, d, pend)
		r.Text = fmt.Sprintf("✅ Creé %s <b>%s</b>.\n\n", esc(singular(target.Name)), esc(pend.Labels[refFieldOf(pend)])) + r.Text
		r.Written = append(r.Written, Written{Kind: "create", Resource: target.Name, ID: fmt.Sprint(row["id"])})
		return r, true
	}
	d.Pending.Delete(pend)
	return Result{}, false
}

func refFieldOf(pend *Pending) string {
	for k, l := range pend.Labels {
		if strings.HasSuffix(l, " (nuevo)") {
			return k
		}
	}
	return ""
}

// fetchRow re-reads a row by id through the engine (RBAC-scoped).
func fetchRow(ctx context.Context, d Deps, pend *Pending) map[string]any {
	res := d.Vocab.Resource(pend.Resource)
	params := url.Values{"per_page": {"1"}, "fields": {strings.Join(listColumns(res), ",")}}
	params.Set("filter[id][eq]", pend.RowID)
	rows, _, err := d.Exec.List(ctx, pend.Resource, params)
	if err != nil || len(rows) != 1 {
		return nil
	}
	return rows[0]
}

// pickOption reads "2", "la 2", "el segundo"… or a name among the options.
func pickOption(text string, opts []Candidate) *Candidate {
	n := normalize(text)
	n = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(n, "la "), "el "), "opcion ")
	ordinals := map[string]int{"1": 1, "uno": 1, "primero": 1, "primera": 1, "2": 2, "dos": 2, "segundo": 2, "segunda": 2, "3": 3, "tres": 3, "tercero": 3, "tercera": 3, "4": 4, "cuatro": 4, "cuarto": 4, "cuarta": 4, "5": 5, "cinco": 5, "quinto": 5, "quinta": 5}
	if i, ok := ordinals[n]; ok && i <= len(opts) {
		return &opts[i-1]
	}
	if len(opts) == 0 || len(n) < 3 {
		return nil
	}
	dec := Decide(Match(text, opts, nil))
	if dec.Kind == "one" {
		return dec.Chosen
	}
	return nil
}

// coerceSaid turns a typed follow-up ("urgente", "mañana", "15") into the
// field's value — deterministically, no model.
func coerceSaid(f *Field, text string, now time.Time) (any, string, error) {
	s := strings.TrimSpace(text)
	if s == "" {
		return nil, "", fmt.Errorf("no llegó ningún valor")
	}
	switch {
	case len(f.Enum) > 0:
		if contains(f.Enum, s) {
			return s, "", nil
		}
		var cands []Candidate
		for _, e := range f.Enum {
			cands = append(cands, Candidate{Label: e, Value: e})
		}
		if dec := Decide(Match(s, cands, nil)); dec.Kind == "one" {
			return dec.Chosen.Value, "", nil
		}
		return nil, "", fmt.Errorf("«%s» no es una de las opciones", s)
	case f.IsText():
		if len([]rune(s)) > 500 {
			return nil, "", fmt.Errorf("es demasiado largo")
		}
		return s, "", nil
	case f.IsNumeric():
		ns := strings.ReplaceAll(strings.ReplaceAll(s, ".", ""), ",", ".")
		if f.Money {
			fl, err := strconv.ParseFloat(strings.Fields(ns)[0], 64)
			if err != nil {
				return nil, "", fmt.Errorf("«%s» no es un monto", s)
			}
			return int64(fl*100 + 0.5), "", nil
		}
		if f.Type == "float64" {
			fl, err := strconv.ParseFloat(strings.Fields(ns)[0], 64)
			if err != nil {
				return nil, "", fmt.Errorf("«%s» no es un número", s)
			}
			return fl, "", nil
		}
		n, err := strconv.ParseInt(strings.Fields(ns)[0], 10, 64)
		if err != nil {
			return nil, "", fmt.Errorf("«%s» no es un número entero", s)
		}
		return n, "", nil
	case f.Type == "bool":
		switch normalize(s) {
		case "si", "sí", "verdadero", "true", "claro", "dale":
			return true, "", nil
		case "no", "falso", "false":
			return false, "", nil
		}
		return nil, "", fmt.Errorf("«%s» no es sí ni no", s)
	case f.Type == "time":
		tok := spanishTimeToken(s)
		if tok == "" {
			return nil, "", fmt.Errorf("«%s» no es una fecha que entienda", s)
		}
		t, words, ok := ResolveTimeValue(tok, now)
		if !ok {
			return nil, "", fmt.Errorf("«%s» no es una fecha que entienda", s)
		}
		return t.UTC().Format(time.RFC3339), words, nil
	}
	return nil, "", fmt.Errorf("ese campo no se puede dictar")
}

// ── execution ─────────────────────────────────────────────────────────────

// Written is one row the engine wrote for this reply.
type Written struct {
	Kind     string `json:"kind"`
	Resource string `json:"resource"`
	ID       string `json:"id"`
}

// executePending runs the confirmed write through the Writer and words the
// outcome. The pending is forgotten either way: a refused write is said, not
// retried.
func executePending(ctx context.Context, d Deps, pend *Pending) Result {
	d.Pending.Delete(pend)
	if time.Now().After(pend.Expires) {
		return Result{Kind: "expired", Headline: "Venció", Plan: &pend.Plan,
			Text: "⌛ Esa confirmación venció (5 minutos). Volvé a decirme qué querés escribir."}
	}
	res := d.Vocab.Resource(pend.Resource)
	row, err := d.Write.Write(ctx, pend.Kind, pend.Resource, pend.RowID, pend.Data)
	if err != nil {
		r := writeFailure(err, pend.Kind+" "+singular(pend.Resource))
		r.Plan = &pend.Plan
		return r
	}
	id := fmt.Sprint(row["id"])
	label := rowWords(res, row, res.LabelFields(), d.Now.Location())
	var text string
	if pend.Kind == "create" {
		text = fmt.Sprintf("✅ Listo: creé %s <b>%s</b>.", esc(singular(pend.Resource)), esc(label))
	} else {
		var changes []string
		for _, f := range res.Fields {
			if v, ok := pend.Data[f.Name]; ok {
				changes = append(changes, esc(fieldWords(f))+" → <b>"+esc(valueWords(f, v, pend.Labels[f.Name], d.Now.Location()))+"</b>")
			}
		}
		text = fmt.Sprintf("✅ Listo: %s <b>%s</b>: %s.", esc(singular(pend.Resource)), esc(label), strings.Join(changes, ", "))
	}
	return Result{Kind: "written", Headline: "Listo", Text: text, Speech: Speech(text), Plan: &pend.Plan,
		Written: []Written{{Kind: pend.Kind, Resource: pend.Resource, ID: id}}}
}

// writeFailure words the engine's refusal — the same 403/409/422 the API
// would answer, in the owner's words, never a success face.
func writeFailure(err error, what string) Result {
	var we *WriteError
	if errors.As(err, &we) {
		switch we.Status {
		case 403:
			return Result{Kind: "forbidden", Headline: "No tenés permiso", Detail: we.Msg,
				Text: "🔒 Tu rol no puede " + esc(what) + ". No escribí nada."}
		case 409:
			return Result{Kind: "conflict", Headline: "Choca con algo que existe", Detail: we.Msg,
				Text: "⚠️ No pude " + esc(what) + ": choca con un dato que ya existe (" + esc(we.Msg) + "). No escribí nada."}
		case 422:
			var parts []string
			for _, f := range we.Fields {
				parts = append(parts, esc(f.Field)+": "+esc(f.Message))
			}
			detail := esc(we.Msg)
			if len(parts) > 0 {
				detail = strings.Join(parts, "; ")
			}
			return Result{Kind: "rejected", Headline: "El motor no lo aceptó", Detail: we.Msg,
				Text: "⚠️ No pude " + esc(what) + ": " + detail + ". No escribí nada."}
		case 404:
			return Result{Kind: "not_found", Headline: "Ya no está", Detail: we.Msg,
				Text: "🤷 Esa fila ya no está (alguien la cambió o la borró). No escribí nada."}
		}
	}
	return Result{Kind: "unavailable", Headline: "No pude escribir", Detail: err.Error(),
		Text: "⚠️ No pude escribir en la base ahora. No escribí nada; probá en unos segundos."}
}

// Confirm answers a pending by id (the HTTP door POST /api/ask/confirm and
// the Telegram buttons): the id must belong to the caller.
func Confirm(ctx context.Context, d Deps, pendingID, answer string) Result {
	start := time.Now()
	if d.Pending == nil || d.PendingKey == "" {
		return Result{Kind: "invalid", Headline: "Nada pendiente", Text: "No hay ninguna escritura pendiente."}
	}
	pend := d.Pending.ByID(pendingID, d.PendingKey)
	if pend == nil {
		return Result{Kind: "expired", Headline: "Nada pendiente",
			Text: "⌛ No hay ninguna escritura pendiente con ese id (venció, ya se resolvió, o no es tuya)."}
	}
	r, handled := continuePending(ctx, d, pend, answer)
	if !handled {
		r = Result{Kind: "cancelled", Headline: "Cancelado", Plan: &pend.Plan,
			Text: "👌 Eso no fue un <b>sí</b> claro, así que lo cancelé. No escribí nada."}
	}
	r.Source = "confirm"
	r.TotalMS = time.Since(start).Milliseconds()
	return r
}

// PendingOptions sorts a pending's options deterministically (tests).
func PendingOptions(p *Pending) []string {
	var out []string
	for _, o := range p.Options {
		out = append(out, o.Label)
	}
	sort.Strings(out)
	return out
}

// spanishTimeSpanEnd reads the END of a span the owner typed as a follow-up
// («mañana de 4 a 5» → the token for tomorrow 17:00); ok=false when no end
// was said.
func spanishTimeSpanEnd(s string) (string, bool) {
	toks := tokenize(strings.ToLower(strings.TrimSpace(s)))
	day, hint := consumeDayPart(toks)
	span, ok := consumeTimeSpanHint(toks, hint)
	if !ok || span.end < 0 {
		return "", false
	}
	if day == "" {
		day = "today"
	}
	return day + " " + clockString(span.end), true
}

// resolveRefs (VOZ-20) settles the names a write carries without a field:
// each is tried against every candidate target. One place → written there;
// several → the owner picks (the option names its kind); none → a soft name
// joins the title text, a hard one refuses the write naming what was tried.
func resolveRefs(ctx context.Context, d Deps, pend *Pending, res *Resource, refs []Ref) (Result, bool) {
	for i, rf := range refs {
		kind, field, chosen, opts, err := resolveMulti(ctx, d, res, rf.Match, rf.Fields)
		if err != nil {
			return execFailure(err), true
		}
		switch kind {
		case "one":
			pend.Data[field] = chosen.Value
			pend.Labels[field] = chosen.Label
		case "several", "maybe":
			pend.OpenRefs = append([]Ref(nil), refs[i+1:]...)
			pend.Stage, pend.WhichFor, pend.Options, pend.RefName = "which", "ref", opts, rf.Match
			d.Pending.Put(pend)
			head := "🤔 «%s» puede ser más de una cosa. ¿Cuál?\n%s\n\nRespondé con el número (o <b>no</b> para cancelar)."
			if kind == "maybe" {
				head = "🤷 No encuentro «%s» tal cual. ¿Quisiste decir?\n%s\n\nRespondé con el número (o <b>no</b> para cancelar)."
			}
			return pendingResult(pend, "ambiguous", "¿Cuál?", fmt.Sprintf(head, esc(rf.Match), numberedKinds(opts))), true
		default:
			if len(rf.Fields) == 1 {
				// One place for it: exactly what a placed name gets — the
				// offer to create it, or «no encuentro».
				pend.OpenRefs = append([]Ref(nil), refs[i+1:]...)
				if r, done := resolveRef(ctx, d, pend, res.Field(rf.Fields[0]), rf.Match); done {
					return r, true
				}
				continue
			}
			if !rf.Soft {
				return Result{Kind: "not_found", Headline: "No encuentro «" + rf.Match + "»",
					Text: fmt.Sprintf("🤷 No encuentro «%s» como %s. Cargalo primero o decímelo de otra forma.", esc(rf.Match), esc(kindsWords(d.Vocab, res, rf.Fields)))}, true
			}
			if !rf.InTitle {
				if tf := titleField(res); tf != nil {
					if cur, ok := pend.Data[tf.Name].(string); ok && cur != "" {
						pend.Data[tf.Name] = cur + " " + rf.Match
					} else {
						pend.Data[tf.Name] = rf.Match
					}
				}
			}
		}
	}
	pend.OpenRefs = nil
	return Result{}, false
}

// confirmationOrder is the order a confirmation is READ in: the title, the
// relations, the times (a range's start before its end), then the rest.
func confirmationOrder(res *Resource) []*Field {
	var title, rels, times, rest []*Field
	tf := titleField(res)
	for _, f := range res.Fields {
		switch {
		case f == tf:
			title = append(title, f)
		case f.Relation != "":
			rels = append(rels, f)
		case f.Type == "time":
			times = append(times, f)
		default:
			rest = append(rest, f)
		}
	}
	if rg := res.Range(); rg != nil {
		var ordered []*Field
		if f := res.Field(rg.Start); f != nil {
			ordered = append(ordered, f)
		}
		if f := res.Field(rg.End); f != nil {
			ordered = append(ordered, f)
		}
		for _, f := range times {
			if f.Name != rg.Start && f.Name != rg.End {
				ordered = append(ordered, f)
			}
		}
		times = ordered
	}
	out := append(title, rels...)
	out = append(out, times...)
	return append(out, rest...)
}
