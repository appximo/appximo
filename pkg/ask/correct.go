package ask

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// A CORRECTION to a write that waits for its yes (AGENDA-ASISTENTE-S1, Part
// A «corregir»): «no, mejor el viernes», «mejor a las 5», «sí pero urgente»,
// «que sea con Marta». It used to cancel the pending and re-read the
// sentence as a new question — a paid «no entendí». Now, when the words
// after the lead are exactly the data the fixed form recognizes (a day, a
// clock, a bool by its name, a declared value, «con Nombre», a duration),
// they are applied to the pending and the confirmation is shown AGAIN; it is
// never executed by a «sí pero…» — the doctrine of ADR-037 stands: nothing
// is written without an exact yes to exactly what will be written. Words the
// form does not recognize keep the old behavior (cancel, re-read).

var correctionLeads = []string{
	"no mejor", "si pero mejor", "sí pero mejor", "pero mejor", "si pero", "sí pero", "mejor", "que sea", "que quede",
	"cambialo a", "cambiala a", "cambialo por", "cambiala por", "cambialo", "cambiala", "cambia a", "cambiá a", "cambia", "cambiá",
	"ponelo", "ponela", "ponele", "hacelo", "mas bien", "más bien", "en realidad", "corrijo", "corrige", "no",
}

// applyCorrection reads text as a change to pend. Returns (result, true)
// when it was one — the re-issued confirmation, or a question the change
// raised (a pick, a not-found); (Result{}, false) when the words are not a
// correction the form recognizes.
func applyCorrection(ctx context.Context, d Deps, pend *Pending, text string) (Result, bool) {
	n := strings.TrimSpace(normalize(text))
	rest := n
	for _, lead := range correctionLeads {
		l := normalize(lead)
		if n == l {
			return Result{}, false // a bare lead says nothing
		}
		if strings.HasPrefix(n, l+" ") {
			rest = strings.TrimSpace(strings.TrimPrefix(n, l))
			break
		}
	}
	if rest == "" {
		return Result{}, false
	}
	res := d.Vocab.Resource(pend.Resource)
	if res == nil {
		return Result{}, false
	}
	// The raw words after the lead, for names as said.
	rawRest := text
	if i := len(text) - len(rest); i > 0 && i <= len(text) {
		// normalize() may change lengths (accents): re-derive from words.
		rw := strings.Fields(text)
		nw := strings.Fields(rest)
		if len(rw) >= len(nw) {
			rawRest = strings.Join(rw[len(rw)-len(nw):], " ")
		}
	}
	toks := tokenizeKeep(rawRest)
	segs := segments(toks, d.Vocab, res)
	if len(segs) == 0 {
		return Result{}, false
	}
	data := map[string]any{}
	var refs []Ref
	var timeSegs [][]ctok
	for _, sg := range segs {
		// a day/clock is applied against the PENDING's own time (keep the
		// day when only a clock is said, keep the clock when only a day is)
		if isTimePhrase(sg, res) {
			timeSegs = append(timeSegs, sg)
			continue
		}
		if !classifySegment(sg, d.Vocab, res, data, &refs, false) {
			return Result{}, false
		}
	}
	// nothing but a bare name would be a stray word, not a correction
	if len(data) == 0 && len(timeSegs) == 0 && len(refs) == 0 {
		return Result{}, false
	}
	var changed []string
	loc := d.Now.Location()
	for _, sg := range timeSegs {
		f, ok := correctTime(pend, res, sg, d.Now, loc)
		if !ok {
			return Result{}, false
		}
		changed = append(changed, f)
	}
	for _, k := range sortedKeys(data) {
		fd := res.Field(k)
		if fd == nil {
			return Result{}, false
		}
		if fd.Relation != "" {
			name := data[k].(map[string]any)["match"].(string)
			if r, done := resolveRef(ctx, d, pend, fd, name); done {
				r.Text = "<i>Cambio " + esc(fieldWords(fd)) + ".</i>\n" + r.Text
				return r, true
			}
			changed = append(changed, fieldWords(fd))
			continue
		}
		val, label, err := resolveLiteral(fd, data[k], d.Now)
		if err != nil {
			return Result{}, false
		}
		pend.Data[k] = val
		if label != "" {
			pend.Labels[k] = label
		} else {
			delete(pend.Labels, k)
		}
		changed = append(changed, fieldWords(fd))
	}
	if len(refs) > 0 {
		if r, done := resolveRefs(ctx, d, pend, res, refs); done {
			return r, true
		}
		for _, rf := range refs {
			changed = append(changed, "«"+rf.Match+"»")
		}
	}
	pend.Stage, pend.Field = "confirm", ""
	r := finishPending(ctx, d, pend)
	note := "Cambié " + strings.Join(changed, " y ") + "."
	r.Text = "<i>" + esc(note) + "</i>\n" + r.Text
	if r.Speech != "" {
		r.Speech = note + " " + r.Speech
	}
	return r, true
}

// correctTime moves the pending's time by what the segment says: only a
// clock → same day, new hour; only a day → same hour, new day; both → both.
// A range keeps its length. Returns the field word changed.
func correctTime(pend *Pending, res *Resource, sg []ctok, now time.Time, loc *time.Location) (string, bool) {
	tt := make([]token, len(sg))
	for i, t := range sg {
		tt[i] = token{raw: t.raw, norm: t.norm}
	}
	day, hint := consumeDayPart(tt)
	span, hasSpan := consumeTimeSpanHint(tt, hint)
	field := ""
	endField := ""
	if rg := res.Range(); rg != nil {
		field, endField = rg.Start, rg.End
	} else if f := res.DueTimeField(); f != nil {
		field = f.Name
	}
	if field == "" {
		return "", false
	}
	// the current value (or today at the said clock when there is none)
	cur := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if s, ok := pend.Data[field].(string); ok {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			cur = t.In(loc)
		}
	}
	var length time.Duration
	if endField != "" {
		if s, ok := pend.Data[endField].(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				length = t.Sub(cur)
			}
		}
	}
	newDay := cur
	if day != "" {
		t, _, ok := ResolveTimeValue(day, now)
		if !ok {
			return "", false
		}
		newDay = t.In(loc)
	}
	h, m := cur.Hour(), cur.Minute()
	if hasSpan {
		h, m = span.start/60, span.start%60
	}
	nt := time.Date(newDay.Year(), newDay.Month(), newDay.Day(), h, m, 0, 0, loc)
	pend.Data[field] = nt.UTC().Format(time.RFC3339)
	words := ""
	withClock := hasSpan || h != 0 || m != 0
	if tok := dayToken(day, nt, now); tok != "" {
		if withClock {
			tok += " " + fmt.Sprintf("%02d:%02d", h, m)
		}
		if _, w, ok := ResolveTimeValue(tok, now); ok {
			words = w
		}
	}
	if words == "" {
		words = "el " + dateWords(nt)
		if withClock {
			words += " a las " + nt.Format("15:04")
		}
	}
	pend.Labels[field] = words
	if endField != "" {
		var end time.Time
		if hasSpan && span.end >= 0 {
			end = time.Date(newDay.Year(), newDay.Month(), newDay.Day(), span.end/60, span.end%60, 0, 0, loc)
		} else if length > 0 {
			end = nt.Add(length)
		}
		if !end.IsZero() {
			pend.Data[endField] = end.UTC().Format(time.RFC3339)
			pend.Labels[endField] = end.Format("15:04")
		} else {
			delete(pend.Data, endField)
			delete(pend.Labels, endField)
		}
	}
	return fieldWords(res.Field(field)), true
}

// dayToken returns the token for the day words said, or the one that names
// the pending's own day relative to now ("today", "tomorrow", …) when no day
// was said — for the wording of the confirmation.
func dayToken(said string, t, now time.Time) string {
	if said != "" {
		return said
	}
	loc := now.Location()
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	d1 := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	switch int(d1.Sub(d0).Hours() / 24) {
	case 0:
		return "today"
	case 1:
		return "tomorrow"
	case 2:
		return "day_after_tomorrow"
	}
	return ""
}
