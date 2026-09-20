package ask

import (
	"testing"
)

// Real dictation damage, not easy invented cases: Spanish speech-to-text
// swaps homophones (s/z/c, b/v, ll/y, g/j), drops the silent h, loses accents,
// and re-spells Anglo names phonetically. Each pair is (what arrived, what
// exists) — the matcher must find the stored name and not confuse it with a
// neighbour.
var colombianDictation = []struct{ said, stored string }{
	{"Gomes", "Gómez"},
	{"Juan Peres", "Juan Pérez"},
	{"Gimenes", "Jiménez"},
	{"Baldes", "Valdés"},
	{"Yeison Ospina", "Jeison Ospina"},
	{"Brayan Cardona", "Bryan Cardona"},
	{"Jhon Jairo", "John Jairo"},
	{"Yesica Basques", "Jessica Vásquez"},
	{"Suluaga", "Zuluaga"},
	{"Enao", "Henao"},
	{"Jiraldo", "Giraldo"},
	{"Kintero", "Quintero"},
	{"Maicol Bedoya", "Michael Bedoya"},
	{"Leidi Muños", "Leidy Muñoz"},
	{"Deisi Rodrigues", "Deisy Rodríguez"},
	{"Estiven Ceballos", "Stiven Ceballos"},
	{"Andres Villegas", "Andrés Villegas"},
	{"Yulieth Arango", "Julieth Arango"},
	{"Sebastian Beles", "Sebastián Vélez"},
	{"Ana Maria Ortis", "Ana María Ortiz"},
	{"Guillermo Hoyos", "Guillermo Hoyos"},
	{"Yenifer Castaño", "Jennifer Castaño"},
	{"Estefania Sapata", "Estefanía Zapata"},
	{"Javier Echeverri", "Javier Echeverry"},
}

// A realistic client list of a Colombian shop: the stored names above plus
// neighbours that a careless matcher would confuse them with.
var clientList = []string{
	"Gómez", "Gómez Hermanos SAS", "Juan Pérez", "Juana Pérez", "Jiménez", "Valdés", "Jeison Ospina",
	"Bryan Cardona", "John Jairo", "Jessica Vásquez", "Zuluaga", "Henao", "Giraldo", "Quintero",
	"Michael Bedoya", "Leidy Muñoz", "Deisy Rodríguez", "Stiven Ceballos", "Andrés Villegas",
	"Julieth Arango", "Sebastián Vélez", "Ana María Ortiz", "Guillermo Hoyos", "Jennifer Castaño",
	"Estefanía Zapata", "Javier Echeverry", "Carlos Restrepo", "Luz Marina Gallego", "Pedro Nel Torres",
	"Diana Carolina Mejía", "Óscar Iván Salazar", "Ferretería El Tornillo", "Distribuidora La 33",
}

func candidates() []Candidate {
	out := make([]Candidate, 0, len(clientList))
	for i, n := range clientList {
		out = append(out, Candidate{ID: string(rune('a' + i)), Label: n, Value: n})
	}
	return out
}

func TestNames_MangledColombianNamesResolveToTheStoredOne(t *testing.T) {
	for _, c := range colombianDictation {
		ranked := Match(c.said, candidates(), nil)
		dec := Decide(ranked)
		switch dec.Kind {
		case "one":
			if dec.Chosen.Label != c.stored {
				t.Errorf("%q → chose %q (%.2f), want %q", c.said, dec.Chosen.Label, dec.Chosen.Score, c.stored)
			}
		case "several":
			// Acceptable ONLY when the stored name is among the options AND a
			// genuine neighbour exists (Gómez vs Gómez Hermanos; Juan vs Juana).
			found := false
			for _, o := range dec.Options {
				if o.Label == c.stored {
					found = true
				}
			}
			if !found {
				t.Errorf("%q → several without the right one: %v", c.said, labels(dec.Options))
			}
			t.Logf("%q → asks: %v", c.said, labels(dec.Options))
		default:
			t.Errorf("%q → %s (top: %q %.2f), want %q", c.said, dec.Kind, ranked[0].Label, ranked[0].Score, c.stored)
		}
	}
}

func TestNames_NonexistentNameIsNeverForced(t *testing.T) {
	for _, said := range []string{"Wilfredo Pacheco", "Ximena Lombana", "Rodrigo Fierro"} {
		dec := Decide(Match(said, candidates(), nil))
		if dec.Kind == "one" {
			t.Errorf("%q does not exist but was resolved to %q (%.2f)", said, dec.Chosen.Label, dec.Chosen.Score)
		}
	}
}

func TestNames_AmbiguousAsks(t *testing.T) {
	cands := []Candidate{{Label: "Ana Gómez", Value: "1"}, {Label: "Luis Gómez", Value: "2"}, {Label: "Carlos Mesa", Value: "3"}}
	dec := Decide(Match("Gómez", cands, nil))
	if dec.Kind != "several" || len(dec.Options) != 2 {
		t.Fatalf("want several [Ana, Luis], got %s %v", dec.Kind, labels(dec.Options))
	}
	// A full name disambiguates.
	dec = Decide(Match("Luis Gomes", cands, nil))
	if dec.Kind != "one" || dec.Chosen.Label != "Luis Gómez" {
		t.Fatalf("want one Luis Gómez, got %s %v", dec.Kind, dec)
	}
}

func TestNames_PhoneticAndSearchTerms(t *testing.T) {
	if phonetic("Vásquez") != phonetic("Basques") {
		t.Errorf("Vásquez/Basques must fold: %q vs %q", phonetic("Vásquez"), phonetic("Basques"))
	}
	if phonetic("Henao") != phonetic("Enao") || phonetic("Llorente") != phonetic("Yorente") {
		t.Errorf("silent h / ll-y must fold")
	}
	terms := SearchTerms("Gomez Muñoz")
	want := map[string]bool{"gom": true, "góm": true, "mun": true, "muñ": true}
	for w := range want {
		if !contains(terms, w) {
			t.Errorf("SearchTerms missing %q in %v", w, terms)
		}
	}
	if len(SearchTerms("de la")) != 0 {
		t.Errorf("tokens under 3 letters produce no term")
	}
}

func labels(cs []Candidate) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Label)
	}
	return out
}

// VOZ-AHORRO-S2: an exact CODE beside its near neighbours is the row, not a
// question — ORD-1001 vs ORD-1011 / ORD-1010 score ~0.99 (found live); two
// rows that sound the same still are.
func TestDecide_ExactCodeBeatsNearCodes(t *testing.T) {
	cands := []Candidate{{Label: "ORD-1001", Value: "a"}, {Label: "ORD-1011", Value: "b"}, {Label: "ORD-1010", Value: "c"}, {Label: "ORD-1012", Value: "d"}}
	d := Decide(Match("ORD-1001", cands, nil))
	if d.Kind != "one" || d.Chosen.Label != "ORD-1001" {
		t.Fatalf("exact code must win: %+v", d)
	}
	same := []Candidate{{Label: "Ana Gómez", Value: "a"}, {Label: "Ana Gomes", Value: "b"}}
	if d := Decide(Match("Ana Gomez", same, nil)); d.Kind != "several" {
		t.Fatalf("two rows that sound the same stay a question: %+v", d)
	}
}
