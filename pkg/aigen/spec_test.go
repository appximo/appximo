package aigen

import (
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// TestSystemPromptComposition pins the shared-source guarantee (JSON-EDITOR-S3):
// the internal generation prompt and the printable spec are both built FROM
// GrammarCore, so the grammar can never diverge between the two consumers.
func TestSystemPromptComposition(t *testing.T) {
	if !strings.Contains(systemPrompt, GrammarCore) {
		t.Fatal("systemPrompt no longer embeds GrammarCore — the internal loop and `appximo spec` have diverged")
	}
	if !strings.Contains(Spec(), GrammarCore) {
		t.Fatal("Spec() no longer embeds GrammarCore — the printed grammar has diverged from the generation prompt")
	}
}

// TestSpecExamplesValidate runs every JSON example embedded in the spec through
// the REAL validator (ValidateReport — structural + semantic): the printed
// grammar can never teach a shape the engine rejects.
func TestSpecExamplesValidate(t *testing.T) {
	// The canonical example inside GrammarCore (the last { … } block).
	core := GrammarCore
	idx := strings.Index(core, "CANONICAL EXAMPLE")
	if idx < 0 {
		t.Fatal("GrammarCore lost its canonical example")
	}
	brace := strings.Index(core[idx:], "{")
	if brace < 0 {
		t.Fatal("canonical example has no JSON object")
	}
	canonical := core[idx+brace:]
	// The operational example (CAPACIDADES-VISIBLES-S1): the agenda that
	// reminds, reacts, is spoken to and reports — the shape the generator is
	// told to copy, so it must validate too.
	oidx := strings.Index(core, "OPERATIONAL EXAMPLE")
	if oidx < 0 {
		t.Fatal("GrammarCore lost its operational example")
	}
	operational := firstJSONObject(core[oidx:])

	for name, doc := range map[string]string{
		"canonical (GrammarCore)":        canonical,
		"operational (GrammarCore)":      operational,
		"advanced (SpecExampleAdvanced)": SpecExampleAdvanced,
	} {
		rep := schema.ValidateReport([]byte(doc))
		if !rep.Valid {
			for _, e := range rep.Errors {
				t.Errorf("%s: %s [%s] %s (fix: %s)", name, e.Path, e.Rule, e.Message, e.Fix)
			}
			t.Fatalf("%s: the spec embeds an INVALID example", name)
		}
	}
}

// firstJSONObject returns the first balanced {…} object in s.
func firstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	for i := start; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' && (i == 0 || s[i-1] != '\\'):
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

// TestSpecCoversAdvancedGrammar keeps the printed spec honest about the blocks
// the compact internal prompt omits — if a section is dropped, this names it.
func TestSpecCoversAdvancedGrammar(t *testing.T) {
	spec := Spec()
	for _, needle := range []string{
		"state_machine", "permissions", "condition_actions", "foreign_keys",
		"references", "on_update", "hooks", "hmac_secret_env", "events",
		"renamed_from", "validate --json",
		// CAPACIDADES-VISIBLES-S1: the operational blocks and their signals.
		"workflows", "summary.telegram", "aliases", "OPERATIONAL BLOCKS", "pending",
		// FILES-1: the per-field file attach policy must stay teachable.
		"max_bytes", "file_policy",
	} {
		if !strings.Contains(spec, needle) {
			t.Errorf("Spec() lost coverage of %q", needle)
		}
	}
}
