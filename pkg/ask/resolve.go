package ask

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// A proper name the sentence did not PLACE (VOZ-20, AGENDA-ASISTENTE-S1):
// «las tareas de Esposa», «crear tarea: pagar el seguro, Casa» — on a
// resource with an area AND a person, is «Esposa» an area or a person? The
// parser used to give up («name could match area_id or persona_id») and the
// model was paid to guess. Now the parser says WHICH fields it could be, and
// the engine tries the name against every target with the same matcher a
// placed name gets: the one target where it exists wins; two → the owner is
// asked which, with each option naming its kind («área Casa», «persona
// Casa»); none → said, naming every kind that was tried. The resource's own
// name-like text field is a candidate too («la tarea del techo»).

// resolveMulti tries name against each candidate field of res. Returns the
// decision kind ("one" | "several" | "maybe" | "none"), the winning field and
// candidate for "one", and the options (each carrying its Field) otherwise.
func resolveMulti(ctx context.Context, d Deps, res *Resource, name string, fields []string) (kind, field string, chosen Candidate, options []Candidate, err error) {
	// Relation targets first (a person, an area); the row's own text field
	// («la tarea del techo») only when no target holds the name — a title
	// that merely CONTAINS the name («almuerzo con Fabián») must never
	// compete with the person Fabián.
	var rels, own []string
	for _, fname := range fields {
		if fd := res.Field(fname); fd != nil && fd.Relation != "" {
			rels = append(rels, fname)
		} else if fd != nil {
			own = append(own, fname)
		}
	}
	kind, field, chosen, options, err = resolveAmong(ctx, d, res, name, rels)
	if err != nil || kind == "one" || kind == "several" || len(own) == 0 {
		return
	}
	k2, f2, c2, o2, err2 := resolveAmong(ctx, d, res, name, own)
	if err2 != nil {
		return "", "", Candidate{}, nil, err2
	}
	if k2 == "none" && kind == "maybe" {
		return // the relations' weak options are worth asking about
	}
	return k2, f2, c2, o2, nil
}

// resolveAmong tries name against a set of fields of one kind.
func resolveAmong(ctx context.Context, d Deps, res *Resource, name string, fields []string) (kind, field string, chosen Candidate, options []Candidate, err error) {
	var strong, weak []Candidate
	for _, fname := range fields {
		fd := res.Field(fname)
		if fd == nil {
			continue
		}
		target := res
		targetName := res.Name
		if fd.Relation != "" {
			target = d.Vocab.Resource(fd.Relation)
			targetName = fd.Relation
		}
		if target == nil {
			continue
		}
		cands, ferr := fetchCandidates(ctx, d, target, fd, name)
		if ferr != nil {
			return "", "", Candidate{}, nil, ferr
		}
		dec := Decide(Match(name, cands, nil))
		kindWord := singular(targetName)
		if fd.Relation == "" {
			kindWord = fieldWords(fd)
		}
		tag := func(c Candidate) Candidate {
			c.Field = fname
			c.Kind = kindWord
			return c
		}
		switch dec.Kind {
		case "one":
			strong = append(strong, tag(*dec.Chosen))
		case "several":
			for _, o := range dec.Options {
				strong = append(strong, tag(o))
			}
		case "maybe":
			for _, o := range dec.Options {
				weak = append(weak, tag(o))
			}
		}
	}
	sort.SliceStable(strong, func(i, j int) bool { return strong[i].Score > strong[j].Score })
	sort.SliceStable(weak, func(i, j int) bool { return weak[i].Score > weak[j].Score })
	switch {
	case len(strong) == 1:
		return "one", strong[0].Field, strong[0], nil, nil
	case len(strong) > 1:
		// An exact name in ONE place beats a near-miss elsewhere («Casa» the
		// area, «Casas» a person) — the same rule Decide applies inside one
		// target.
		if strong[0].Score >= 0.999 && strong[1].Score < 0.999 {
			return "one", strong[0].Field, strong[0], nil, nil
		}
		return "several", "", Candidate{}, capOptions(strong), nil
	case len(weak) > 0:
		return "maybe", "", Candidate{}, capOptions(weak), nil
	}
	return "none", "", Candidate{}, nil, nil
}

// kindsWords names the kinds a multi-field name was tried as: «área, persona
// o título».
func kindsWords(v *Vocabulary, res *Resource, fields []string) string {
	var words []string
	for _, fname := range fields {
		fd := res.Field(fname)
		if fd == nil {
			continue
		}
		w := fieldWords(fd)
		if fd.Relation != "" {
			w = singular(fd.Relation)
		}
		if !contains(words, w) {
			words = append(words, w)
		}
	}
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " o " + words[len(words)-1]
}

// optionLabel words one option of a multi-field pick: «área Casa».
func optionLabel(c Candidate) string {
	if c.Kind == "" {
		return c.Label
	}
	return c.Kind + " " + c.Label
}

// numberedKinds is numbered() with the kind of each option in front.
func numberedKinds(cs []Candidate) string {
	var b strings.Builder
	for i, c := range cs {
		fmt.Fprintf(&b, "%d. %s\n", i+1, esc(optionLabel(c)))
	}
	return strings.TrimRight(b.String(), "\n")
}
