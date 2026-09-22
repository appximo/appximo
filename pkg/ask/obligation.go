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
	"tengo pendiente", "tenemos que", "acordate de", "acordame de", "tengo que", "hay que",
	"recordame", "recuerdame", "me falta",
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
	if !consumePhrase(toks, phrase) {
		return ParseResult{Reason: "obligation: phrase not consumed"}
	}
	// A resource named in the sentence must be the to-do one («tengo que
	// agendar una cita» is the agenda's business, not a task).
	for _, t := range toks {
		if t.used {
			continue
		}
		for _, name := range v.order {
			r := v.resources[name]
			if r != res && v.namesResource(t.norm, r) {
				return ParseResult{Reason: "obligation: names another resource (" + name + ")"}
			}
		}
	}
	data := map[string]any{}
	// «urgente» → the resource's bool field of that name, and out of the title.
	var urgentField *Field
	for _, f := range res.Fields {
		if f.Type == "bool" && strings.Contains(normalize(f.Name), "urgent") {
			urgentField = f
			break
		}
	}
	if urgentField != nil {
		for i := range toks {
			if !toks[i].used && (toks[i].norm == "urgente" || toks[i].norm == "urgentemente" || toks[i].norm == "urgentisimo") {
				toks[i].used = true
				data[urgentField.Name] = true
			}
		}
	}
	// A day («mañana», «el viernes», «pasado mañana») → the resource's single
	// writable time field (its due date); with two, the model decides.
	var timeField *Field
	for _, f := range res.Fields {
		if f.Type == "time" && !f.Auto {
			if timeField != nil {
				timeField = nil
				break
			}
			timeField = f
		}
	}
	if timeField != nil {
		if day := consumeDay(toks); day != "" {
			data[timeField.Name] = day
		}
	}
	var title []string
	for _, t := range toks {
		if t.used {
			continue
		}
		title = append(title, t.raw)
	}
	for len(title) > 0 && stopwords[normalize(title[0])] && normalize(title[0]) != "que" {
		title = title[1:]
	}
	for len(title) > 0 {
		last := normalize(title[len(title)-1])
		if last == "por" || last == "favor" || last == "porfa" || last == "y" || last == "," {
			title = title[:len(title)-1]
			continue
		}
		break
	}
	if len(title) == 0 {
		return ParseResult{Reason: "obligation: no title"}
	}
	data[titleField(res).Name] = strings.Join(title, " ")
	p := Plan{Kind: "create", Resource: res.Name, Data: data}
	if err := p.Validate(v); err != nil {
		return ParseResult{Reason: "obligation invalid: " + err.Error()}
	}
	return ParseResult{Plan: p, Sure: true}
}
