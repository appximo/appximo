package ask

// Dates and past weekdays in a question (AGENDA-ASISTENTE-S1 addendum,
// 2026-09-25). A LOG looks back: «registros del martes» is the past
// Tuesday, «registros del 23 de septiembre» is that day — the year is
// optional: the current one, or the previous one when that date has not come
// yet — and «registros de septiembre» is the month. An AGENDA keeps looking
// forward («compromisos del martes» is the coming Tuesday). No domain word
// decides it: the direction comes from the schema. A range without
// no_overlap or the creation timestamp records what HAPPENED; a blocking
// range or a plain time field points at what is to come.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// date:YYYY-MM-DD (explicit), date:MM-DD (the next occurrence, today
	// included), past_date:MM-DD (the most recent one, today included);
	// month:YYYY-MM, month:MM, past_month:MM (the same, the month in
	// progress included).
	dateTokenRe  = regexp.MustCompile(`^(past_)?date:(?:(\d{4})-)?(\d{2})-(\d{2})$`)
	monthTokenRe = regexp.MustCompile(`^(past_)?month:(?:(\d{4})-)?(\d{2})$`)
	slashDateRe  = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})(?:/(\d{2}|\d{4}))?$`)
	yearRe       = regexp.MustCompile(`^(?:19|20)\d{2}$`)
	dayDigitsRe  = regexp.MustCompile(`^\d{1,2}$`)
)

// monthNumbers are the months as a person says them (no abbreviations a
// dictation never writes; «sept»/«setiembre» are said).
var monthNumbers = map[string]int{
	"enero": 1, "febrero": 2, "marzo": 3, "abril": 4, "mayo": 5, "junio": 6, "julio": 7, "agosto": 8,
	"septiembre": 9, "setiembre": 9, "sept": 9, "octubre": 10, "noviembre": 11, "diciembre": 12,
}

// dayNumberWords: «primero», «uno» … «veintinueve», «treinta» («treinta y
// uno» is read as three tokens).
var dayNumberWords = func() map[string]int {
	m := map[string]int{"primero": 1, "un": 1, "veintiun": 21}
	for i := 1; i < len(unitsES) && i <= 29; i++ {
		m[normalize(unitsES[i])] = i
	}
	m["treinta"] = 30
	return m
}()

// readDayNumber reads a day of the month at toks[i]: digits 1–31, a number
// word, or «treinta y uno». Returns the day and the tokens it took (0 = none).
func readDayNumber(toks []token, i int) (int, int) {
	if i >= len(toks) || toks[i].used {
		return 0, 0
	}
	n := toks[i].norm
	if dayDigitsRe.MatchString(n) {
		d, _ := strconv.Atoi(n)
		if d >= 1 && d <= 31 {
			return d, 1
		}
		return 0, 0
	}
	d, ok := dayNumberWords[n]
	if !ok {
		return 0, 0
	}
	if d == 30 && i+2 < len(toks) && !toks[i+1].used && !toks[i+2].used && toks[i+1].norm == "y" && (toks[i+2].norm == "uno" || toks[i+2].norm == "una") {
		return 31, 3
	}
	return d, 1
}

// validDay reports whether day/month (and year, when given) is a real date.
func validDay(y, mo, d int) bool {
	if mo < 1 || mo > 12 || d < 1 || d > 31 {
		return false
	}
	if y == 0 {
		y = 2024 // a leap year: February 29 stays sayable without a year
	}
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	return t.Month() == time.Month(mo) && t.Day() == d
}

func dateToken(y, mo, d int) string {
	if y == 0 {
		return fmt.Sprintf("date:%02d-%02d", mo, d)
	}
	return fmt.Sprintf("date:%04d-%02d-%02d", y, mo, d)
}

func monthToken(y, mo int) string {
	if y == 0 {
		return fmt.Sprintf("month:%02d", mo)
	}
	return fmt.Sprintf("month:%04d-%02d", y, mo)
}

// scanDate finds ONE date said as a person says it — «el 23 de septiembre»,
// «del 23 de septiembre de 2025», «23 septiembre», «veintitrés de
// septiembre», «23/09», «23/9/2026» — consumes its tokens (with a leading
// «el»/«del») and returns the token. A «<day> de <month>» that is not a
// date («31 de febrero», «29 de febrero de 2026») comes back in bad, so the
// reply names it instead of reading «31» as a name. A bare «del 23» is NOT
// a date (it can be a code or a name). A four-digit year after the month
// (with or without «de»/«del») is taken; «de 8» is left alone.
func scanDate(toks []token) (tok, bad string) {
	for i := 0; i < len(toks); i++ {
		if toks[i].used {
			continue
		}
		if m := slashDateRe.FindStringSubmatch(strings.ToLower(toks[i].raw)); m != nil {
			d, _ := strconv.Atoi(m[1])
			mo, _ := strconv.Atoi(m[2])
			y := 0
			if m[3] != "" {
				y, _ = strconv.Atoi(m[3])
				if y < 100 {
					y += 2000
				}
			}
			if !validDay(y, mo, d) {
				return "", toks[i].raw
			}
			markDate(toks, i, i)
			return dateToken(y, mo, d), ""
		}
		d, n := readDayNumber(toks, i)
		if n == 0 {
			continue
		}
		j := i + n
		if j < len(toks) && !toks[j].used && toks[j].norm == "de" {
			j++
		}
		if j >= len(toks) || toks[j].used {
			continue
		}
		mo, ok := monthNumbers[toks[j].norm]
		if !ok {
			continue
		}
		end, y := j, 0
		k := j + 1
		if k < len(toks) && !toks[k].used && (toks[k].norm == "de" || toks[k].norm == "del") {
			k++
		}
		if k < len(toks) && !toks[k].used && yearRe.MatchString(toks[k].norm) {
			y, _ = strconv.Atoi(toks[k].norm)
			end = k
		}
		if !validDay(y, mo, d) {
			var raws []string
			for k := i; k <= end; k++ {
				raws = append(raws, toks[k].raw)
			}
			return "", strings.Join(raws, " ")
		}
		markDate(toks, i, end)
		return dateToken(y, mo, d), ""
	}
	return "", ""
}

// markDate consumes toks[i..end] and a leading «el»/«del»/«desde el».
func markDate(toks []token, i, end int) {
	if i > 0 && !toks[i-1].used && (toks[i-1].norm == "el" || toks[i-1].norm == "del") {
		i--
	}
	for k := i; k <= end; k++ {
		toks[k].used = true
	}
}

// scanMonth finds a month said alone — «de septiembre», «en septiembre»,
// «del mes de septiembre», «septiembre de 2025» — when the word is not one
// of the schema's, consumes it and returns the token.
func scanMonth(toks []token, v *Vocabulary) (string, bool) {
	for i := 0; i < len(toks); i++ {
		if toks[i].used || (v != nil && v.knownWord(toks[i].norm)) {
			continue
		}
		mo, ok := monthNumbers[toks[i].norm]
		if !ok {
			continue
		}
		start, end, y := i, i, 0
		k := i + 1
		if k < len(toks) && !toks[k].used && (toks[k].norm == "de" || toks[k].norm == "del") {
			k++
		}
		if k < len(toks) && !toks[k].used && yearRe.MatchString(toks[k].norm) {
			y, _ = strconv.Atoi(toks[k].norm)
			end = k
		}
		// «de septiembre», «en septiembre», «del mes de septiembre», «el mes de septiembre»
		if start > 0 && !toks[start-1].used && (toks[start-1].norm == "de" || toks[start-1].norm == "en" || toks[start-1].norm == "durante") {
			start--
			if start > 1 && !toks[start-1].used && !toks[start-2].used && toks[start-1].norm == "mes" && (toks[start-2].norm == "del" || toks[start-2].norm == "el") {
				start -= 2
			}
		}
		for k := start; k <= end; k++ {
			toks[k].used = true
		}
		return monthToken(y, mo), true
	}
	return "", false
}

// validRange reports whether a period token is one the engine resolves: a
// listed range, or a date / month token.
func validRange(rng string) bool {
	if contains(Ranges, rng) {
		return true
	}
	if m := dateTokenRe.FindStringSubmatch(rng); m != nil {
		y, mo, d := 0, atoi(m[3]), atoi(m[4])
		if m[2] != "" {
			y = atoi(m[2])
		}
		return !(m[1] != "" && m[2] != "") && validDay(y, mo, d)
	}
	if m := monthTokenRe.FindStringSubmatch(rng); m != nil {
		mo := atoi(m[3])
		return !(m[1] != "" && m[2] != "") && mo >= 1 && mo <= 12
	}
	return false
}

// resolveDateToken resolves date:/past_date:/month:/past_month: tokens.
func resolveDateToken(rng string, now time.Time) (Window, bool) {
	loc := now.Location()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if m := dateTokenRe.FindStringSubmatch(rng); m != nil {
		past, mo, d := m[1] != "", atoi(m[3]), atoi(m[4])
		y := now.Year()
		if m[2] != "" {
			y = atoi(m[2])
		} else if !past {
			// the NEXT occurrence, today included: «agenda X el 3 de enero»
			// said in September is the coming January
			if time.Date(y, time.Month(mo), d, 0, 0, 0, 0, loc).Before(day) {
				y++
			}
		} else {
			// «del 30 de diciembre» said in September: the one that happened;
			// «del 29 de febrero»: the last leap year that had it
			for tries := 0; tries < 8; tries++ {
				t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, loc)
				if t.Month() == time.Month(mo) && t.Day() == d && !t.After(day) {
					break
				}
				y--
			}
		}
		t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, loc)
		if t.Month() != time.Month(mo) || t.Day() != d {
			return Window{}, false
		}
		words := "el " + weekdayES(t.Weekday()) + " " + strconv.Itoa(d) + " de " + monthsLongES[mo]
		if t.Year() != now.Year() {
			words += " de " + strconv.Itoa(t.Year())
		}
		return Window{t, t.AddDate(0, 0, 1), words}, true
	}
	if m := monthTokenRe.FindStringSubmatch(rng); m != nil {
		past, mo := m[1] != "", atoi(m[3])
		y := now.Year()
		if m[2] != "" {
			y = atoi(m[2])
		} else if past && mo > int(now.Month()) {
			y-- // the most recent one, the month in progress included
		} else if !past && mo < int(now.Month()) {
			y++ // the coming one, the month in progress included
		}
		t := time.Date(y, time.Month(mo), 1, 0, 0, 0, 0, loc)
		words := "en " + monthsLongES[mo]
		if t.Year() != now.Year() {
			words += " de " + strconv.Itoa(t.Year())
		}
		return Window{t, t.AddDate(0, 1, 0), words}, true
	}
	return Window{}, false
}

// tokenDateWords says a date/month token for a reply («29 de febrero»).
func tokenDateWords(rng string) string {
	if m := dateTokenRe.FindStringSubmatch(rng); m != nil {
		s := strconv.Itoa(atoi(m[4])) + " de " + monthsLongES[atoi(m[3])]
		if m[2] != "" {
			s += " de " + m[2]
		}
		return s
	}
	if m := monthTokenRe.FindStringSubmatch(rng); m != nil {
		s := monthsLongES[atoi(m[3])]
		if m[2] != "" {
			s += " de " + m[2]
		}
		return s
	}
	return rng
}

// looksBack reports whether a period over field (bare = the resource's
// period target) records what HAPPENED: a range that does not block (a log
// of blocks of time) or the creation stamp. A blocking range (an agenda) or
// a plain time field (a deadline, an appointment date) looks forward.
func looksBack(res *Resource, field string) bool {
	if res == nil {
		return false
	}
	if field == "" {
		field, _ = res.PeriodTarget()
	}
	if rg := res.RangeNamed(field); rg != nil {
		return !rg.NoOverlap
	}
	if f := res.Field(field); f != nil {
		return f.IsCreated || f.Auto
	}
	return false
}

// lookBack turns a period that a look-back resource cannot have — a coming
// weekday («el martes», «del martes»), a date or a month with no year — into
// its past reading (the reply names the day it read). An explicit year is
// left as said.
func lookBack(res *Resource, p *Period) {
	if p == nil || !looksBack(res, p.Field) {
		return
	}
	switch {
	case strings.HasPrefix(p.Range, "next_") && p.Range != "next_week":
		p.Range = "last_" + strings.TrimPrefix(p.Range, "next_")
	case strings.HasPrefix(p.Range, "date:") && len(p.Range) == len("date:MM-DD"):
		p.Range = "past_" + p.Range
	case strings.HasPrefix(p.Range, "month:") && len(p.Range) == len("month:MM"):
		p.Range = "past_" + p.Range
	}
}
