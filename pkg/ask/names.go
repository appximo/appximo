package ask

import (
	"sort"
	"strings"
	"unicode"

	"github.com/appximo/appximo/pkg/schema"
)

// Proper names, as dictated: Spanish speech-to-text mangles them ("Gómez" →
// "Gomes", "Jiménez" → "Gimenes", "Valdés" → "Baldes", "Jeison" → "Yeison"),
// so a name never goes into a filter as said. It is matched against the
// values that EXIST, server-side, with three layers:
//
//  1. normalize — lowercase, accents stripped, punctuation out, spaces folded
//  2. phonetic  — Spanish homophones folded (b/v, s/z/soft c, ll/y, silent h,
//     qu/k/hard c, g/j before e/i, double letters), so what SOUNDS the same
//     scores the same
//  3. similarity — Jaro-Winkler over the phonetic forms, whole string and
//     token-wise (so "Juan Peres" ≈ "Juan Carlos Pérez")
//
// The decision is deterministic and CONSERVATIVE: one strong match is used
// and said back; several strong ones are asked about; none is said — a zero
// never masquerades as an answer.

const (
	strongMatch = 0.86 // ≥ → a candidate the engine will use (if alone)
	weakMatch   = 0.72 // ≥ → worth a "¿quisiste decir…?"
	maxOptions  = 5
)

// normalize lowercases, strips accents (keeps ñ as n — dictation writes both)
// and punctuation, and collapses whitespace. ONE source, shared with the
// schema validator (schema.NormalizeText): an alias the validator proved
// unique is compared at runtime in exactly the form it was proved in.
func normalize(s string) string { return schema.NormalizeText(s) }

// phonetic folds Spanish homophones on a normalized string.
func phonetic(s string) string {
	s = normalize(s)
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		next := func() rune {
			if i+1 < len(rs) {
				return rs[i+1]
			}
			return 0
		}
		var out rune
		switch c {
		case 'h':
			continue // silent
		case 'v':
			out = 'b'
		case 'z':
			out = 's'
		case 'x':
			out = 's'
		case 'c':
			switch next() {
			case 'e', 'i':
				out = 's'
			case 'h':
				out = 'x' // "ch" → one symbol
				i++
			default:
				out = 'k'
			}
		case 'q':
			out = 'k'
			if next() == 'u' {
				i++
			}
		case 'g':
			switch next() {
			case 'e', 'i':
				out = 'j'
			default:
				out = 'g'
			}
		case 'l':
			if next() == 'l' {
				out = 'y'
				i++
			} else {
				out = 'l'
			}
		case 'y':
			if next() == 0 || next() == ' ' {
				out = 'i'
			} else {
				out = 'y'
			}
		case 'w':
			out = 'u'
		default:
			out = c
		}
		if out == 'u' && i > 0 && (rs[i-1] == 'g' || rs[i-1] == 'q') && (next() == 'e' || next() == 'i') {
			continue // gue/gui: silent u
		}
		// collapse doubles — letters only: a repeated DIGIT is information
		// (ORD-1001 is not ORD-101; found live, VOZ-AHORRO-S2)
		if b.Len() > 0 {
			prev := []rune(b.String())
			if prev[len(prev)-1] == out && out != ' ' && !unicode.IsDigit(out) {
				continue
			}
		}
		b.WriteRune(out)
	}
	return b.String()
}

// jaroWinkler is the standard similarity in [0,1] (1 = identical).
func jaroWinkler(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	window := max(len(ra), len(rb))/2 - 1
	if window < 0 {
		window = 0
	}
	ma := make([]bool, len(ra))
	mb := make([]bool, len(rb))
	matches := 0
	for i := range ra {
		lo, hi := max(0, i-window), min(len(rb)-1, i+window)
		for j := lo; j <= hi; j++ {
			if mb[j] || ra[i] != rb[j] {
				continue
			}
			ma[i], mb[j] = true, true
			matches++
			break
		}
	}
	if matches == 0 {
		return 0
	}
	t := 0
	k := 0
	for i := range ra {
		if !ma[i] {
			continue
		}
		for !mb[k] {
			k++
		}
		if ra[i] != rb[k] {
			t++
		}
		k++
	}
	m := float64(matches)
	jaro := (m/float64(len(ra)) + m/float64(len(rb)) + (m-float64(t)/2)/m) / 3
	prefix := 0
	for i := 0; i < min(4, min(len(ra), len(rb))); i++ {
		if ra[i] != rb[i] {
			break
		}
		prefix++
	}
	return jaro + float64(prefix)*0.1*(1-jaro)
}

// similarity scores how well a dictated name matches a stored value: the best
// of the whole-string score and a token-wise score (each query token matched
// to its best candidate token; a candidate may carry more tokens than the
// query — "Juan Pérez" against "Juan Carlos Pérez Gómez" still scores high).
func similarity(query, value string) float64 {
	pq, pv := phonetic(query), phonetic(value)
	if pq == "" || pv == "" {
		return 0
	}
	best := jaroWinkler(pq, pv)
	qt, vt := strings.Fields(pq), strings.Fields(pv)
	if len(qt) > 0 && len(vt) > 0 {
		sum := 0.0
		for _, q := range qt {
			m := 0.0
			for _, v := range vt {
				if s := jaroWinkler(q, v); s > m {
					m = s
				}
			}
			sum += m
		}
		tok := sum / float64(len(qt))
		// A one-token query matching one token of a multi-token value is a
		// partial match: dampen slightly so a full-name query still wins.
		if len(qt) < len(vt) {
			tok *= 0.97
		}
		if tok > best {
			best = tok
		}
	}
	return best
}

// Candidate is one existing row (or distinct value) the name may refer to.
type Candidate struct {
	ID    string // the row id (relation match) or "" (own-field match)
	Label string // what to say back ("Ana Gómez")
	Value string // the value to filter by: the id, or the field's own text
	Score float64
}

// Match ranks candidates for a dictated name. Each candidate's Label and, when
// distinct, its extra texts are scored; the best score wins for that
// candidate. Returns the ranked list (descending).
func Match(query string, cands []Candidate, texts func(Candidate) []string) []Candidate {
	out := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		best := similarity(query, c.Label)
		if texts != nil {
			for _, t := range texts(c) {
				if s := similarity(query, t); s > best {
					best = s
				}
			}
		}
		c.Score = best
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// Decision is the outcome of resolving one dictated name.
type Decision struct {
	// Chosen is the single strong match (Kind == "one").
	Chosen *Candidate
	// Options are the alternatives to ask about (Kind == "several" | "maybe").
	Options []Candidate
	// Kind: one | several | maybe | none.
	Kind string
}

// Decide applies the thresholds to a ranked list: exactly one strong match
// → use it; several strong (within 0.04 of the best, or all ≥ strong) → ask;
// no strong but some weak → "¿quisiste decir…?"; nothing → none.
func Decide(ranked []Candidate) Decision {
	var strong, weak []Candidate
	for _, c := range ranked {
		switch {
		case c.Score >= strongMatch:
			strong = append(strong, c)
		case c.Score >= weakMatch:
			weak = append(weak, c)
		}
	}
	// De-duplicate by label (two rows named identically are one option).
	strong = dedupe(strong)
	weak = dedupe(weak)
	switch {
	case len(strong) == 1:
		return Decision{Kind: "one", Chosen: &strong[0]}
	case len(strong) > 1:
		// A phonetically EXACT match ahead of every other is still "one":
		// "Gomes" with Gómez and Gómez Hermanos SAS both present means Gómez
		// (the owner would have said the long name); a second exact match
		// (two rows that sound the same) stays a question. The bar for the
		// runner-up is "not exact" — a CODE like ORD-1001 sits beside
		// ORD-1011 / ORD-1010 at 0.99 (found live, VOZ-AHORRO-S2), and an
		// exact code is the row, not a question.
		if strong[0].Score >= 0.999 && strong[1].Score < 0.999 {
			return Decision{Kind: "one", Chosen: &strong[0]}
		}
		return Decision{Kind: "several", Options: capOptions(strong)}
	case len(weak) > 0:
		return Decision{Kind: "maybe", Options: capOptions(weak)}
	default:
		return Decision{Kind: "none"}
	}
}

func dedupe(cs []Candidate) []Candidate {
	seen := map[string]bool{}
	out := cs[:0:0]
	for _, c := range cs {
		k := normalize(c.Label)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	return out
}

func capOptions(cs []Candidate) []Candidate {
	if len(cs) > maxOptions {
		return cs[:maxOptions]
	}
	return cs
}

// SearchTerms derives the short prefixes used to FETCH candidates through
// the engine's own ?search= (an ILIKE, case- but not accent-insensitive):
// for each dictated token of ≥ 3 letters, its first three letters, unaccented,
// plus the accented variants a stored value might carry ("gom" → "góm";
// "mun" → "muñ"; "per" → "pér"). Deterministic, bounded (≤ 4 per token).
func SearchTerms(query string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, tok := range strings.Fields(normalize(query)) {
		if len([]rune(tok)) < 3 {
			continue
		}
		p := string([]rune(tok)[:3])
		add(p)
		for i, r := range []rune(p) {
			if alt, ok := accentAlt[r]; ok {
				v := []rune(p)
				v[i] = alt
				add(string(v))
			}
		}
	}
	return out
}

var accentAlt = map[rune]rune{'a': 'á', 'e': 'é', 'i': 'í', 'o': 'ó', 'u': 'ú', 'n': 'ñ'}
