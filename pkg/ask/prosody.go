package ask

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// PROSODY (AGENDA-ASISTENTE-S1, Part D): what a voice assistant reads must
// sound like a person. A line break is not a pause; a bullet is not a
// sentence; «16:00» is not «las cuatro de la tarde». The rules, all
// measurable (SpeechMetrics pins them):
//   - short sentences (a full stop between items; ≤ 22 words per sentence,
//     ≤ 14 on average), nothing read as a list;
//   - no symbol, emoji, guillemet or raw digit: hours and dates in words,
//     small numbers in words («dos tareas»), a code («ORD-1003») is kept;
//   - at most 5 items spoken, then «y N más; mirá el panel»;
//   - a long guide in parts, offering «más».
// The screen keeps the whole list (text); the voice keeps the shape.

// spokenMax is how many list items a voice reads before «y N más».
const spokenMax = 5

var unitsES = [...]string{"cero", "uno", "dos", "tres", "cuatro", "cinco", "seis", "siete", "ocho", "nueve", "diez", "once", "doce", "trece", "catorce", "quince", "dieciséis", "diecisiete", "dieciocho", "diecinueve", "veinte", "veintiuno", "veintidós", "veintitrés", "veinticuatro", "veinticinco", "veintiséis", "veintisiete", "veintiocho", "veintinueve"}
var tensES = [...]string{"", "", "veinte", "treinta", "cuarenta", "cincuenta", "sesenta", "setenta", "ochenta", "noventa"}
var monthsLongES = [...]string{"", "enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
var weekdaysLongES = [...]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

// NumberWords says an integer 0–999 in Spanish; larger ones stay digits
// (a voice reads «1.234» acceptably; a long spelled-out number does not help).
func NumberWords(n int) string {
	if n < 0 {
		return "menos " + NumberWords(-n)
	}
	if n < 30 {
		return unitsES[n]
	}
	if n < 100 {
		if n%10 == 0 {
			return tensES[n/10]
		}
		return tensES[n/10] + " y " + unitsES[n%10]
	}
	if n == 100 {
		return "cien"
	}
	if n < 1000 {
		h := [...]string{"", "ciento", "doscientos", "trescientos", "cuatrocientos", "quinientos", "seiscientos", "setecientos", "ochocientos", "novecientos"}[n/100]
		if n%100 == 0 {
			return h
		}
		return h + " " + NumberWords(n%100)
	}
	return Integer(float64(n))
}

// numberPhrase says «dos tareas» / «una tarea» / «ninguna tarea».
func numberPhrase(n int, resource string) string {
	s := singular(resource)
	fem := feminineWord(s)
	switch n {
	case 0:
		if fem {
			return "ninguna " + s
		}
		return "ningún " + s
	case 1:
		if fem {
			return "una " + s
		}
		return "un " + s
	}
	return NumberWords(n) + " " + phrase(resource, float64(n))
}

// feminineWord guesses the gender of a Spanish noun from its ending — the
// article of a spoken count («una tarea», «un compromiso», «una orden»).
func feminineWord(w string) bool {
	w = normalize(w)
	for _, suf := range []string{"a", "cion", "sion", "dad", "tad", "tud", "umbre", "ie", "orden", "ez", "triz"} {
		if strings.HasSuffix(w, suf) {
			return !(suf == "a" && (strings.HasSuffix(w, "ema") || strings.HasSuffix(w, "oma") || strings.HasSuffix(w, "dia") || w == "mapa" || w == "planeta"))
		}
	}
	return false
}

// ClockWords says a clock in Spanish: «las cuatro de la tarde», «las nueve
// y media de la mañana», «la una», «las doce del mediodía», «las ocho de la
// noche».
// ClockRangeWords says a span the way a person does: the part of the day
// once, at the end, when both ends share it — «de las diez a las once de la
// mañana», «de las once de la mañana a las dos de la tarde».
func ClockRangeWords(h1, m1, h2, m2 int) string {
	a, b := ClockWords(h1, m1), ClockWords(h2, m2)
	for _, part := range []string{" de la mañana", " de la tarde", " de la noche"} {
		if strings.HasSuffix(a, part) && strings.HasSuffix(b, part) {
			return "de " + strings.TrimSuffix(a, part) + " a " + b
		}
	}
	return "de " + a + " a " + b
}

func ClockWords(h, m int) string {
	hour12 := h % 12
	if hour12 == 0 {
		hour12 = 12
	}
	art := "las"
	if hour12 == 1 {
		art = "la"
	}
	var part string
	switch {
	case h == 12 && m == 0:
		return "las doce del mediodía"
	case h == 0 && m == 0:
		return "las doce de la noche"
	case h < 12:
		part = "de la mañana"
	case h < 19:
		part = "de la tarde"
	default:
		part = "de la noche"
	}
	unit := unitsES[hour12]
	if hour12 == 1 {
		unit = "una" // «la una», never «la uno»
	}
	base := art + " " + unit
	switch m {
	case 0:
		return base + " " + part
	case 15:
		return base + " y cuarto " + part
	case 30:
		return base + " y media " + part
	case 45:
		next := hour12 + 1
		if next == 13 {
			next = 1
		}
		a2 := "las"
		if next == 1 {
			a2 = "la"
		}
		return a2 + " " + unitsES[next] + " menos cuarto " + part
	}
	return base + " y " + NumberWords(m) + " " + part
}

// DateWordsLong says a date in Spanish: «el lunes veintiuno de septiembre».
func DateWordsLong(t time.Time) string {
	return "el " + weekdaysLongES[t.Weekday()] + " " + NumberWords(t.Day()) + " de " + monthsLongES[t.Month()]
}

// DateShortWords says a date without the weekday: «veintitrés de septiembre».
func DateShortWords(t time.Time) string {
	return NumberWords(t.Day()) + " de " + monthsLongES[t.Month()]
}

var (
	clockRangeRe = regexp.MustCompile(`(?:\bde\s+)?\b(\d{1,2}):(\d{2})\s*(?:[–-]|\sa\s)\s*(\d{1,2}):(\d{2})\b`)
	clockOnlyRe  = regexp.MustCompile(`\b(\d{1,2}):(\d{2})\b`)
	// the composers' own date shapes: «23 Sep», «23 Sep 16:23», «lun 21 sep», «dom 20 sep»
	dateShortRe     = regexp.MustCompile(`(?i)\b(?:(dom|lun|mar|mié|mie|jue|vie|sáb|sab)\s+)?(\d{1,2})\s+(ene|feb|mar|abr|may|jun|jul|ago|sep|oct|nov|dic|jan|apr|aug|dec)\b`)
	countRe         = regexp.MustCompile(`\b(\d{1,3})\s+([a-záéíóúñ]+)`)
	spokenISODateRe = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)
	signedRe        = regexp.MustCompile(`(^|[\s,(])([+\-−])(\d{1,3})\b`)
	colonNumRe      = regexp.MustCompile(`(?m):\s(\d{1,3})\b(?:[.,;]|$)`)
)

var monthAbbrevES = map[string]int{"ene": 1, "jan": 1, "feb": 2, "mar": 3, "abr": 4, "apr": 4, "may": 5, "jun": 6, "jul": 7, "ago": 8, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dic": 12, "dec": 12}
var weekdayAbbrevES = map[string]string{"dom": "domingo", "lun": "lunes", "mar": "martes", "mie": "miércoles", "mié": "miércoles", "jue": "jueves", "vie": "viernes", "sab": "sábado", "sáb": "sábado"}

// SpokenNumbers rewrites the clocks, dates and small counts a composed line
// carries into words — the last pass of Speech().
func SpokenNumbers(s string) string {
	s = clockRangeRe.ReplaceAllStringFunc(s, func(m string) string {
		g := clockRangeRe.FindStringSubmatch(m)
		h1, m1, h2, m2 := atoi(g[1]), atoi(g[2]), atoi(g[3]), atoi(g[4])
		return ClockRangeWords(h1, m1, h2, m2)
	})
	s = dateShortRe.ReplaceAllStringFunc(s, func(m string) string {
		g := dateShortRe.FindStringSubmatch(m)
		mo, ok := monthAbbrevES[strings.ToLower(g[3])]
		if !ok {
			return m
		}
		day := atoi(g[2])
		if day < 1 || day > 31 {
			return m
		}
		out := NumberWords(day) + " de " + monthsLongES[mo]
		if wd, ok := weekdayAbbrevES[strings.ToLower(g[1])]; ok {
			out = wd + " " + out
		}
		return out
	})
	// an ISO date («2026-09-23», the digest's header) → the day in words
	s = spokenISODateRe.ReplaceAllStringFunc(s, func(m string) string {
		g := spokenISODateRe.FindStringSubmatch(m)
		mo, day := atoi(g[2]), atoi(g[3])
		if mo < 1 || mo > 12 || day < 1 || day > 31 {
			return m
		}
		return NumberWords(day) + " de " + monthsLongES[time.Month(mo)]
	})
	// a signed delta («+5 desde ayer», «−2») → «más cinco», «menos dos»
	s = signedRe.ReplaceAllStringFunc(s, func(m string) string {
		g := signedRe.FindStringSubmatch(m)
		word := "más"
		if g[2] != "+" {
			word = "menos"
		}
		return g[1] + word + " " + NumberWords(atoi(g[3]))
	})
	// a count after a colon at the end of a phrase («pendiente: 9.»)
	s = colonNumRe.ReplaceAllStringFunc(s, func(m string) string {
		g := colonNumRe.FindStringSubmatch(m)
		return strings.Replace(m, g[1], NumberWords(atoi(g[1])), 1)
	})
	s = clockOnlyRe.ReplaceAllStringFunc(s, func(m string) string {
		g := clockOnlyRe.FindStringSubmatch(m)
		h, mi := atoi(g[1]), atoi(g[2])
		if h > 23 || mi > 59 {
			return m
		}
		return ClockWords(h, mi)
	})
	// «2 tareas», «3 más», «1 compromiso» → words: a bare count before a
	// word — never the tail of a money amount («$ 120.000») or a code.
	var b strings.Builder
	last := 0
	for _, loc := range countRe.FindAllStringSubmatchIndex(s, -1) {
		start := loc[0]
		if start > 0 {
			prev := s[start-1]
			if prev == '.' || prev == ',' || prev == '-' || prev == '$' || (prev >= '0' && prev <= '9') {
				continue
			}
		}
		n := atoi(s[loc[2]:loc[3]])
		if n < 0 || n > 999 {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(NumberWords(n) + " " + s[loc[4]:loc[5]])
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// Metrics is what SpeechMetrics measures on a spoken reply.
type Metrics struct {
	Sentences        int
	Words            int
	MaxWordsSentence int
	AvgWordsSentence float64
	Digits           int // raw digits (a code keeps its digits; a clock must not)
	Symbols          int // bullets, guillemets, pictographs, tags
}

// SpeechMetrics measures a spoken text against the prosody rules.
func SpeechMetrics(s string) Metrics {
	var m Metrics
	sentences := 0
	words := 0
	cur := 0
	flush := func() {
		if cur > 0 {
			sentences++
			if cur > m.MaxWordsSentence {
				m.MaxWordsSentence = cur
			}
			cur = 0
		}
	}
	for _, f := range strings.Fields(s) {
		cur++
		words++
		last := f[len(f)-1]
		if last == '.' || last == '?' || last == '!' || last == ':' {
			flush()
		}
	}
	flush()
	m.Sentences, m.Words = sentences, words
	if sentences > 0 {
		m.AvgWordsSentence = float64(words) / float64(sentences)
	}
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			m.Digits++
		case r == '•' || r == '«' || r == '»' || r == '<' || r == '>' || r == '⚙' || isPictograph(r):
			m.Symbols++
		}
	}
	return m
}

// spokenList words a list of items for a voice: at most spokenMax, then
// «y N más».
func spokenList(items []string, total int, more string) string {
	if len(items) == 0 {
		return ""
	}
	shown := items
	if len(shown) > spokenMax {
		shown = shown[:spokenMax]
	}
	out := strings.Join(shown, ". ")
	if rest := total - len(shown); rest > 0 {
		out += fmt.Sprintf(". Y %s más", NumberWords(rest))
		if more != "" {
			out += "; " + more
		}
	}
	return out + "."
}
