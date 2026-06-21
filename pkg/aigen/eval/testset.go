package eval

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// corpusFS embeds the curated NL→schema gold test set so the instrument is
// self-contained (the `ai-eval` binary needs no external path). The corpus is a
// SEED: enough to exercise the harness and give a preliminary signal, explicitly
// below the size required for full statistical power — see CorpusNote.
//
//go:embed corpus
var corpusFS embed.FS

// Strata are the complexity tiers the research requires (to locate where a cheap
// model crosses the competence threshold). Ordered simple → complex.
var Strata = []string{"simple", "media", "compleja"}

// Case is one curated pair: a natural-language app description and the GOLD schema
// — a hand-written, validate-clean Appitools schema capturing that intent. The
// gold is correct by construction (a test asserts every gold validates).
type Case struct {
	ID          string          `json:"id"`
	Stratum     string          `json:"stratum"`
	Domain      string          `json:"domain"`
	Description string          `json:"description"`
	Gold        json.RawMessage `json:"gold"`
}

// LoadCorpus reads every embedded case, sorted by (stratum, id) for deterministic
// order (reproducibility — the same run twice yields the same sequence).
func LoadCorpus() ([]Case, error) {
	var cases []Case
	err := fs.WalkDir(corpusFS, "corpus", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		b, rerr := corpusFS.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		var c Case
		if jerr := json.Unmarshal(b, &c); jerr != nil {
			return fmt.Errorf("%s: %w", path, jerr)
		}
		cases = append(cases, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	stratumRank := map[string]int{"simple": 0, "media": 1, "compleja": 2}
	sort.SliceStable(cases, func(i, j int) bool {
		if cases[i].Stratum != cases[j].Stratum {
			return stratumRank[cases[i].Stratum] < stratumRank[cases[j].Stratum]
		}
		return cases[i].ID < cases[j].ID
	})
	return cases, nil
}

// CorpusCounts returns the number of cases per stratum.
func CorpusCounts(cases []Case) map[string]int {
	out := map[string]int{}
	for _, c := range cases {
		out[c.Stratum]++
	}
	return out
}

// TargetPerStratum is the case count per stratum the research says is needed to
// detect a p_sem shift of 0.70→0.80 at 80% power — the seed is well below it, on
// purpose, and the harness grows to it without changing.
const TargetPerStratum = 120

// CorpusNote is the honest one-liner about the seed's statistical limits.
const CorpusNote = "seed corpus — below the ~120-160/stratum needed for full power; " +
	"wide Wilson intervals and inconclusive McNemar (few discordants) are EXPECTED and correct"
