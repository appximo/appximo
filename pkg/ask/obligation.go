package ask

import "strings"

// An OBLIGATION in the owner's own words (APP-AGENDA-S2, VOZ-17): «tengo que
// comprar pintura», «hay que llamar al banco», «acordate de pagar la luz»,
// «recordame renovar el seguro el viernes». There is no write verb in it, so
// the parser used to step aside and the model read it as "a personal note,
// not a data question" — the one sentence an agenda hears most answered «No
// entendí». The shape is settled here, US$ 0: the phrase, ONE to-do resource
// (creatable, no time range, a lifecycle — a state machine — and exactly one
// required title text), the title = every word left, an optional day («mañana»,
// «el viernes») into the resource's single time field, and «urgente» into a
// bool field of that name. Anything else stays the model's, with W1b of the
// system prompt now saying what an obligation is.
var obligationPhrases = []string{
	"que no se me olvide", "no me olvides de", "no te olvides de", "no me olvide de", "no me deje olvidar",
	"tengo pendiente", "tenemos que", "acordate de", "acordame de", "acuerdate de", "acuerdame de", "recuerdame de", "tengo que", "tengo q", "hay que",
	"no se me olvide de", "no se me olvide", "que no se me olvide de",
	"recordame", "recuerdame", "me falta", "recordarme",
}

// taskResource is the ONE resource an obligation lands on: creatable, without a
// time range (that is the agenda), with a state machine on some field (a to-do
// has a lifecycle) and exactly one required, default-less, non-auto text field
// (its title). Two candidates → nil: the sentence must name it, or the model.
func taskResource(v *Vocabulary) *Resource {
	if v == nil {
		return nil
	}
	var found *Resource
	for _, name := range v.order {
		r := v.resources[name]
		if !r.CanCreate || r.Range() != nil || titleField(r) == nil {
			continue
		}
		lifecycle := false
		for _, f := range r.Fields {
			if f.HasMachine {
				lifecycle = true
				break
			}
		}
		if !lifecycle {
			continue
		}
		if found != nil {
			return nil
		}
		found = r
	}
	return found
}

// titleField is the resource's single required, default-less, non-auto text
// field without an enum — what a create must carry and the parser fills with
// the words the owner said. nil when there is none or more than one.
func titleField(r *Resource) *Field {
	var tf *Field
	for _, f := range r.Fields {
		if f.Required && !f.HasDefault && !f.Auto && f.IsText() && len(f.Enum) == 0 {
			if tf != nil {
				return nil
			}
			tf = f
		}
	}
	return tf
}

// parseObligation settles «tengo que X» as a create of the to-do resource.
func parseObligation(question string, v *Vocabulary) ParseResult {
	if v == nil || !v.Writable() {
		return ParseResult{Reason: "obligation: read-only vocabulary"}
	}
	toks := tokenize(question)
	joined := joinedNorms(toks)
	phrase := ""
	for _, ph := range obligationPhrases {
		if strings.HasPrefix(joined, " "+ph+" ") {
			phrase = ph
			break
		}
	}
	if phrase == "" {
		return ParseResult{Reason: "no obligation phrase"}
	}
	res := taskResource(v)
	if res == nil {
		return ParseResult{Reason: "obligation: no single to-do resource"}
	}
	// A resource named in the sentence must be the to-do one («tengo que
	// agendar una cita» is the agenda's business, not a task) — a field
	// word («área salud») is not a resource mention.
	words := strings.Fields(question)
	rest := strings.Join(words[len(strings.Fields(phrase)):], " ") // the phrase opens the sentence
	for _, t := range tokenize(rest) {
		for _, name := range v.order {
			r := v.resources[name]
			if r != res && v.namesResource(t.norm, r) && fieldByWord(v, res, t.norm) == nil {
				return ParseResult{Reason: "obligation: names another resource (" + name + ")"}
			}
		}
	}
	// The rest is the fixed form without its verb: the title, the data in
	// any order (a day, «urgente», «área salud», «con Marta»…).
	pr := buildCreate(v, res, tokenizeKeep(rest), "")
	if !pr.Sure {
		return ParseResult{Reason: "obligation: " + pr.Reason}
	}
	return pr
}
