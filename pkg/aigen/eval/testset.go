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

// SampleStratified returns at most n cases per stratum, taking the first n by the
// deterministic (stratum, id) order LoadCorpus already guarantees. n <= 0 returns
// all cases unchanged. Used to bound a cost-/rate-limited --live run to a
// stratified subsample while the full corpus stays the standing measurement set.
func SampleStratified(cases []Case, n int) []Case {
	if n <= 0 {
		return cases
	}
	perStratum := map[string]int{}
	out := make([]Case, 0, n*len(Strata))
	for _, c := range cases {
		if perStratum[c.Stratum] < n {
			out = append(out, c)
			perStratum[c.Stratum]++
		}
	}
	return out
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

// CorpusNote is the honest one-liner about the corpus's statistical limits. At
// 40/stratum it is a 5x scale-up from the original seed (a real narrowing of the
// Wilson intervals) but still ~1/3 of the ~120-160/stratum the research wants for
// full power, so a McNemar with few discordants is still flagged INCONCLUSIVE.
const CorpusNote = "40/stratum (5x the original seed) — a substantial scale-up that narrows the " +
	"Wilson intervals, still below the ~120-160/stratum ideal for full power; an underpowered " +
	"McNemar (few discordants) remains EXPECTED and is flagged INCONCLUSIVE, not sold as significant"
