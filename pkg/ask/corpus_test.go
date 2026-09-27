package ask

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// The PHRASE BANK as a gate (2026-09-27, the owner's call: «una corrida manual
// algún día no se corre»). 151 sentences a person really says to an agenda —
// 31 of them verbatim from the owner's history — judged against the parser
// with the MODEL OFF: no key, no network, no database, deterministic.
//
// Two directions, both of them regressions the bank caught in three days:
//   - a sentence the parser settles today must keep settling it, WITH the same
//     intention (a create confirmed as a log entry, «persona esposa» turned
//     into a new person);
//   - a sentence the parser leaves to the model must keep leaving it, or the
//     widening gets reviewed (a period phrase turning a read into a change).
//
// Re-baselining is deliberate: when a case legitimately moves, flip `settled`
// in testdata/corpus_es.json and say why in the commit.

type corpusCase struct {
	ID       string         `json:"id"`
	Q        string         `json:"q"`
	Scen     string         `json:"scen"`
	Settled  bool           `json:"settled"`
	Stateful bool           `json:"stateful"`
	Expect   map[string]any `json:"expect"`
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/corpus_es.json")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var doc struct {
		Cases []corpusCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("corpus json: %v", err)
	}
	if len(doc.Cases) < 100 {
		t.Fatalf("corpus has %d cases — it is the regression instrument, it does not shrink", len(doc.Cases))
	}
	return doc.Cases
}

func gateVocab(t *testing.T) *Vocabulary {
	t.Helper()
	raw, err := os.ReadFile("testdata/agenda.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	s, err := schema.LoadFromBytes(raw)
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	return BuildWithWrites(s, "Agenda", func(string) (bool, []string) { return true, nil }, func(string) (bool, bool) { return true, true })
}

func TestCorpus_TheParserKeepsSettlingWhatItSettles(t *testing.T) {
	v := gateVocab(t)
	cases := loadCorpus(t)
	var settled, model int
	for _, c := range cases {
		if c.Stateful {
			continue // an answer to a pending («sí», «el segundo»): needs a pending, not a parse
		}
		pr := Parse(c.Q, v)
		switch {
		case c.Settled && !pr.Sure:
			t.Errorf("%s %q: the parser stopped settling it (%s) — it used to, and the model is off in the gate", c.ID, c.Q, pr.Reason)
		case !c.Settled && pr.Sure:
			t.Errorf("%s %q: the parser now settles it as %s/%s — a widening is welcome, but flip `settled` in testdata/corpus_es.json and say why", c.ID, c.Q, pr.Plan.Kind, pr.Plan.Resource)
		}
		if !c.Settled || !pr.Sure {
			if c.Settled {
				settled++
			} else {
				model++
			}
			continue
		}
		settled++
		why := judgePlan(c.Expect, pr.Plan)
		for _, alt := range asList(c.Expect["alt"]) {
			if why == "" {
				break
			}
			if a, ok := alt.(map[string]any); ok && judgePlan(a, pr.Plan) == "" {
				why = "" // the bank accepts this reading too
			}
		}
		if why != "" {
			t.Errorf("%s %q [%s]: %s", c.ID, c.Q, c.Scen, why)
		}
	}
	t.Logf("phrase bank: %d settled by the parser, %d left to the model, %d stateful", settled, model, len(cases)-settled-model)
}

var timePrefixRe = regexp.MustCompile(`^(today|tomorrow|day_after|next_|last_|yesterday|end_of|date:|month:|past_)`)

// judgePlan is the reply-level scorer of the lab runner, reduced to what a
// PARSE can answer: the plan's kind, resource, data, row, filters, period,
// group_by and limit. A pending's stage (confirm / ask_field / ambiguous) and
// a resolved name are the engine's, not the parser's.
func judgePlan(exp map[string]any, p Plan) string {
	ek, _ := exp["kind"].(string)
	switch ek {
	case "": // nothing to judge
		return ""
	case "guide", "help", "unclear", "write_refused", "summary", "census", "spend", "cancelled", "written":
		if p.Kind != "unclear" && p.Kind != ek {
			return fmt.Sprintf("kind %q, want %q (or unclear)", p.Kind, ek)
		}
		return ""
	case "confirm", "ask_field", "ambiguous", "not_found":
		// the reply is a pending; the PLAN under it is a create or an update
		if p.Kind != "create" && p.Kind != "update" {
			return fmt.Sprintf("kind %q, want a create/update plan under the %s", p.Kind, ek)
		}
	default:
		if p.Kind != ek {
			return fmt.Sprintf("kind %q, want %q", p.Kind, ek)
		}
	}
	if want, ok := exp["resource"].(string); ok && want != "" && p.Resource != want {
		return fmt.Sprintf("resource %q, want %q", p.Resource, want)
	}
	if want, ok := exp["data"].(map[string]any); ok {
		if why := judgeData(want, p.Data, p.Refs); why != "" {
			return why
		}
	}
	if want, ok := exp["where_match"].(string); ok && want != "" {
		var said []string
		for _, w := range p.Where {
			said = append(said, normalize(w.Match), normalize(literal(w.Value)))
		}
		if !strings.Contains(strings.Join(said, " "), normalize(want)) {
			return fmt.Sprintf("row %v does not name %q", said, want)
		}
	}
	for _, f := range asList(exp["filters"]) {
		want, _ := f.(map[string]any)
		if want == nil {
			continue
		}
		if why := judgeFilter(want, p.Filters); why != "" {
			return why
		}
	}
	if want, ok := exp["period"].(string); ok && want != "" {
		if p.Period == nil || p.Period.Range != want {
			return fmt.Sprintf("period %v, want %q", p.Period, want)
		}
	}
	if want, ok := exp["at"].(string); ok && want != "" {
		if p.Period == nil || p.Period.At != want {
			return fmt.Sprintf("at %v, want %q", p.Period, want)
		}
	}
	if want, ok := exp["group_by"].(string); ok && want != "" && p.GroupBy != want {
		return fmt.Sprintf("group_by %q, want %q", p.GroupBy, want)
	}
	if want, ok := exp["limit"].(float64); ok && int(want) > 0 && p.Limit != int(want) {
		return fmt.Sprintf("limit %d, want %d", p.Limit, int(want))
	}
	return ""
}

// judgeData reads the bank's three ways of writing an expected value: «~x» is
// «contains x» (a title as dictated), «match:x» is a name the engine will
// resolve later, a time token is compared by prefix, anything else exactly.
func judgeData(want map[string]any, got map[string]any, refs []Ref) string {
	for f, w := range want {
		g := got[f]
		s, isStr := w.(string)
		switch {
		case isStr && strings.HasPrefix(s, "~"):
			if !strings.Contains(normalize(fmt.Sprint(g)), normalize(strings.TrimPrefix(s, "~"))) {
				return fmt.Sprintf("%s = %v, want it to contain %q", f, g, strings.TrimPrefix(s, "~"))
			}
		case isStr && strings.HasPrefix(s, "match:"):
			// the name may sit in the field, or travel as a REF the engine
			// resolves against every candidate target (VOZ-20)
			name := strings.TrimPrefix(s, "match:")
			said := []string{}
			if m, ok := g.(map[string]any); ok {
				said = append(said, fmt.Sprint(m["match"]))
			} else if g != nil {
				said = append(said, fmt.Sprint(g))
			}
			for _, r := range refs {
				said = append(said, r.Match)
			}
			head := normalize(name)
			if len(head) > 4 {
				head = head[:4]
			}
			if !strings.Contains(normalize(strings.Join(said, " ")), head) {
				return fmt.Sprintf("%s = %v (refs %v), want the name %q", f, g, refs, name)
			}
		case isStr && timePrefixRe.MatchString(s):
			if !strings.HasPrefix(normalize(fmt.Sprint(g)), normalize(s)) {
				return fmt.Sprintf("%s = %v, want %q", f, g, s)
			}
		default:
			if fmt.Sprint(g) != fmt.Sprint(w) {
				return fmt.Sprintf("%s = %v, want %v", f, g, w)
			}
		}
	}
	return ""
}

func judgeFilter(want map[string]any, got []Filter) string {
	field, _ := want["field"].(string)
	for _, g := range got {
		if g.Field != field && !containsStr(g.Fields, field) {
			continue
		}
		if name, ok := want["match"].(string); ok {
			if strings.Contains(normalize(g.Match), normalize(name)[:min(4, len(normalize(name)))]) {
				return ""
			}
			continue
		}
		if val, ok := want["value"]; ok {
			if fmt.Sprint(g.Value) == fmt.Sprint(val) {
				return ""
			}
			continue
		}
		return ""
	}
	return fmt.Sprintf("filter %v missing in %+v", want, got)
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
