package ask

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The reply is a TEMPLATE over the engine's JSON — the number first, then
// what was understood, small. Spanish, phone-first Telegram HTML. The model
// never touches this text.

func composeAggregate(d Deps, p Plan, res *Resource, rows []map[string]any, understood string) Result {
	out := Result{Kind: "answer", Understood: understood}
	fd := res.Field(p.Field)
	format := func(v float64) string {
		if p.Kind == "count" {
			return Integer(v)
		}
		if fd != nil && fd.Money {
			return Money(v)
		}
		if fd != nil && fd.Type == "time" {
			return ""
		}
		return Number(v)
	}
	if p.GroupBy != "" {
		var total float64
		for _, row := range rows {
			label := fmt.Sprint(row[p.GroupBy])
			if row[p.GroupBy] == nil {
				label = "(sin " + p.GroupBy + ")"
			}
			v := metric(row, p)
			total += v
			out.Groups = append(out.Groups, Group{Label: label, Value: v, Text: format(v)})
		}
		sort.SliceStable(out.Groups, func(i, j int) bool { return out.Groups[i].Value > out.Groups[j].Value })
		if p.Kind == "count" || p.Kind == "sum" {
			out.Number = &total
			out.Headline = fmt.Sprintf("%s %s", format(total), res.Name)
		} else {
			out.Headline = fmt.Sprintf("%s de %s por %s", fnWords(p.Kind), p.Field, p.GroupBy)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "<b>%s</b>", esc(out.Headline))
		if p.Kind != "count" {
			fmt.Fprintf(&b, " · %s de %s", fnWords(p.Kind), esc(p.Field))
		}
		fmt.Fprintf(&b, " por %s\n", esc(p.GroupBy))
		if len(out.Groups) == 0 {
			b.WriteString("Ningún registro coincide.\n")
		}
		for _, g := range out.Groups {
			fmt.Fprintf(&b, "• %s: <b>%s</b>\n", esc(g.Label), esc(g.Text))
		}
		fmt.Fprintf(&b, "\n<i>%s</i>", esc(understood))
		out.Text = b.String()
		return out
	}
	if len(rows) == 0 {
		out.Headline = "Sin resultado"
		out.Text = "Ningún registro coincide.\n<i>" + esc(understood) + "</i>"
		return out
	}
	row := rows[0]
	count := toFloat(row["agg_count"])
	if p.Kind == "count" {
		out.Number = &count
		out.Headline = fmt.Sprintf("%s %s", Integer(count), phrase(res.Name, count))
		out.Text = fmt.Sprintf("<b>%s</b> %s\n<i>%s</i>", Integer(count), esc(phrase(res.Name, count)), esc(understood))
		return out
	}
	v := row["agg_"+p.Kind+"_"+p.Field]
	if v == nil || count == 0 {
		// No row matched: say the filter, not a mysterious "sin suma" — the
		// owner sees at once that "vendimos" was read as a state filter.
		zero := 0.0
		out.Number = &zero
		out.Headline = fmt.Sprintf("0 %s · %s", res.Name, understood)
		out.Text = fmt.Sprintf("<b>0</b> %s coinciden — nada que %s.\n<i>%s</i>", esc(res.Name), fnWords(p.Kind)+"r", esc(understood))
		return out
	}
	var shown string
	if fd != nil && fd.Type == "time" {
		if t, ok := v.(time.Time); ok {
			shown = t.In(d.Now.Location()).Format("2006-01-02 15:04")
		} else {
			shown = fmt.Sprint(v)
		}
	} else {
		fv := toFloat(v)
		out.Number = &fv
		shown = format(fv)
	}
	out.Headline = fmt.Sprintf("%s · %s de %s de %s %s", shown, fnWords(p.Kind), p.Field, Integer(count), res.Name)
	out.Text = fmt.Sprintf("<b>%s</b>\n%s de %s · %s %s\n<i>%s</i>", esc(shown), fnWords(p.Kind), esc(p.Field), Integer(count), esc(res.Name), esc(understood))
	return out
}

func composeList(d Deps, p Plan, res *Resource, rows []map[string]any, total int64, cols []string, understood string) Result {
	out := Result{Kind: "answer", Understood: understood}
	n := float64(total)
	out.Number = &n
	out.Headline = fmt.Sprintf("%d %s", total, phrase(res.Name, n))
	var b strings.Builder
	fmt.Fprintf(&b, "<b>%d</b> %s", total, esc(phrase(res.Name, n)))
	if total == 0 {
		b.WriteString("\nNingún registro coincide.")
	} else {
		b.WriteString(":\n")
	}
	labelFields := res.LabelFields()
	for _, row := range rows {
		var parts []string
		if l := labelOf(row, firstN(labelFields, 2)); l != "" {
			parts = append(parts, esc(l))
		}
		for _, c := range cols[1:] {
			if containsStr(labelFields, c) || row[c] == nil {
				continue
			}
			fd := res.Field(c)
			parts = append(parts, esc(formatValue(fd, row[c], d.Now.Location())))
		}
		if len(parts) == 0 {
			parts = append(parts, esc(fmt.Sprint(row["id"])))
		}
		fmt.Fprintf(&b, "• %s\n", strings.Join(parts, " · "))
	}
	if int64(len(rows)) < total {
		fmt.Fprintf(&b, "… y %d más\n", total-int64(len(rows)))
	}
	fmt.Fprintf(&b, "\n<i>%s</i>", esc(understood))
	out.Text = strings.TrimRight(b.String(), "\n")
	return out
}

func formatValue(fd *Field, v any, loc *time.Location) string {
	switch x := v.(type) {
	case time.Time:
		return x.In(loc).Format("02 Jan 15:04")
	case bool:
		if x {
			return "sí"
		}
		return "no"
	}
	if fd != nil && fd.Money {
		return Money(toFloat(v))
	}
	if fd != nil && fd.IsNumeric() {
		return Number(toFloat(v))
	}
	s := fmt.Sprint(v)
	if rs := []rune(s); len(rs) > 60 {
		s = string(rs[:60]) + "…"
	}
	return s
}

func metric(row map[string]any, p Plan) float64 {
	if p.Kind == "count" {
		return toFloat(row["agg_count"])
	}
	return toFloat(row["agg_"+p.Kind+"_"+p.Field])
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case int:
		return float64(n)
	case float64:
		return n
	case float32:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	case fmt.Stringer:
		f, _ := strconv.ParseFloat(n.String(), 64)
		return f
	}
	// A Postgres numeric (a SUM over int64, an AVG) arrives as a driver type;
	// its JSON form is the number, which keeps this package free of the driver.
	if b, err := json.Marshal(v); err == nil {
		f, perr := strconv.ParseFloat(strings.Trim(string(b), `"`), 64)
		if perr == nil {
			return f
		}
	}
	return 0
}

func fnWords(kind string) string {
	switch kind {
	case "sum":
		return "suma"
	case "avg":
		return "promedio"
	case "min":
		return "mínimo"
	case "max":
		return "máximo"
	}
	return kind
}

// phrase words "N <resource>" — the schema's own word, never translated; the
// singular is only attempted for a count of one by trimming a trailing s/es.
func phrase(resource string, n float64) string {
	if n == 1 {
		return singular(resource)
	}
	return resource
}

func singular(resource string) string {
	switch {
	case strings.HasSuffix(resource, "ones"), strings.HasSuffix(resource, "enes"), strings.HasSuffix(resource, "ores"), strings.HasSuffix(resource, "ales"), strings.HasSuffix(resource, "iles"):
		return strings.TrimSuffix(resource, "es")
	case strings.HasSuffix(resource, "s") && !strings.HasSuffix(resource, "ss"):
		return strings.TrimSuffix(resource, "s")
	}
	return resource
}

// Money formats cents as Colombian-style pesos: "$ 1.234.500" (decimals only
// when the cents are not whole).
func Money(cents float64) string {
	whole := math.Trunc(cents / 100)
	rem := math.Abs(cents - whole*100)
	s := "$ " + Integer(whole)
	if rem >= 0.5 {
		s += "," + fmt.Sprintf("%02d", int(math.Round(rem)))
	}
	return s
}

// Integer formats with thousands separators (dots).
func Integer(v float64) string {
	neg := v < 0
	s := strconv.FormatInt(int64(math.Abs(math.Round(v))), 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// Number formats a float with at most two decimals and dot thousands.
func Number(v float64) string {
	if v == math.Trunc(v) {
		return Integer(v)
	}
	s := strconv.FormatFloat(v, 'f', 2, 64)
	i := strings.IndexByte(s, '.')
	return Integer(math.Trunc(v)) + "," + s[i+1:]
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

// Speech renders a reply for a VOICE: tags out, entities back, bullets,
// guillemets and pictographs gone (a voice assistant reads «✅» as "check mark
// button" and «•» as "bullet"), the middle dot a comma, one pause per line.
func Speech(html string) string {
	s := tagRe.ReplaceAllString(html, "")
	s = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&", "• ", "", "…", "...", "«", "", "»", "", " · ", ", ").Replace(s)
	s = strings.Map(func(r rune) rune {
		if isPictograph(r) {
			return -1
		}
		return r
	}, s)
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if b.Len() > 0 {
			// a line that ends in ":" introduces the next («1 tarea: pagar la
			// luz») — a space, not a full stop; any other line is a pause.
			if strings.HasSuffix(b.String(), ":") {
				b.WriteString(" ")
			} else {
				b.WriteString(". ")
			}
		}
		b.WriteString(l)
	}
	return strings.TrimSpace(b.String())
}

// isPictograph is true for the emoji / symbol blocks a reply decorates
// itself with (traffic lights, check marks, the gear of the trace, the
// variation selector that follows them) — never for letters, digits or
// punctuation a sentence needs.
func isPictograph(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF: // emoji, pictographs, symbols
		return true
	case r >= 0x2600 && r <= 0x27BF: // misc symbols, dingbats (✅ ⚙ ☎ ✗)
		return true
	case r >= 0x2B00 && r <= 0x2BFF: // arrows / geometric (⬆ ⭐)
		return true
	case r == 0xFE0F || r == 0x200D: // variation selector, zero-width joiner
		return true
	case r == 0x2139 || r == 0x203C || r == 0x2049 || r == 0x2122 || r == 0x2194 || r == 0x21A9 || r == 0x231A || r == 0x231B: // ℹ ‼ ⁉ ™ ↔ ↩ ⌚ ⌛
		return true
	}
	return false
}

// SpokenTrace is the trace line as a voice reads it — the same datum as
// TraceLine (who answered, how long, what it cost) without the gear or the
// middle dots: «Costo: parser, 5 ms, US$ 0».
func SpokenTrace(r Result) string {
	s := tagRe.ReplaceAllString(TraceLine(r), "")
	s = strings.NewReplacer("⚙︎ ", "Costo: ", "⚙ ", "Costo: ", " · ", ", ", "&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(s)
	return strings.TrimSpace(s)
}

// Plain renders Telegram HTML as plain text for a screen that is not
// Telegram: tags out, entities back, line breaks and bullets kept.
func Plain(html string) string {
	s := tagRe.ReplaceAllString(html, "")
	s = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(s)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func containsStr(set []string, s string) bool { return contains(set, s) }
