package askspend

import (
	"context"
	"fmt"
	"strings"

	"github.com/appximo/appximo/pkg/summary"
)

// Digest is the `gasto` view for ONE tenant (VOZ-TRAZABILIDAD-S1): what the
// questions cost today and this month, how far the cap is, who answered how
// many, and the phrases that cost the most. Composed by the engine from the
// ledger and the history; rendered as text (Telegram HTML) and, through the
// digest's own census card, as a picture — no new renderer.
type Digest struct {
	AppName     string   `json:"app"`
	Tenant      string   `json:"tenant"`
	Day         string   `json:"day"`
	Today       Day      `json:"today"`
	Month       Day      `json:"month"`
	CapUSD      float64  `json:"daily_cap_usd"`
	UserCapUSD  float64  `json:"user_daily_cap_usd,omitempty"`
	Remaining   float64  `json:"remaining_usd"`
	Share       Share    `json:"share_30d"`
	TopCost     []Phrase `json:"top_cost"`
	Fallbacks   []Phrase `json:"model_fallbacks"`
	CacheHits   int      `json:"cache_hits"`
	CacheMisses int      `json:"cache_misses"`
	Text        string   `json:"text"`
	Headline    string   `json:"headline"`
}

// BuildDigest reads the ledger (today, month) and the history (share, top
// phrases) for tenant and composes the text.
func BuildDigest(ctx context.Context, l *Ledger, tenant, appName string) Digest {
	d := Digest{AppName: appName, Tenant: tenant, Day: l.today(), CapUSD: l.cfg.DailyUSD, UserCapUSD: l.cfg.UserDailyUSD}
	for _, t := range l.Today() {
		if t.Tenant == tenant {
			d.Today = t
		}
	}
	if d.Today.Tenant == "" {
		// Not asked since boot: today's row may still exist from before.
		l.mu.Lock()
		d.Today = *l.row(ctx, tenant)
		l.mu.Unlock()
	}
	if m, err := l.Month(ctx); err == nil {
		d.Month = m[tenant]
	}
	if d.CapUSD > 0 {
		d.Remaining = d.CapUSD - d.Today.USD
		if d.Remaining < 0 {
			d.Remaining = 0
		}
	}
	if h := l.hist; h != nil && h.Enabled() {
		d.Share, _ = h.ShareFor(ctx, tenant, 30)
		d.TopCost, _ = h.TopCost(ctx, tenant, 30, 5)
		d.Fallbacks, _ = h.ModelFallbacks(ctx, tenant, 30, 5)
	}
	d.compose()
	return d
}

func usd(v float64) string {
	return strings.Replace(fmt.Sprintf("US$ %.3f", v), ".", ",", 1)
}

func (d *Digest) compose() {
	app := esc(d.AppName)
	if app == "" {
		app = "tu app"
	}
	d.Headline = fmt.Sprintf("%s hoy · %s este mes", usd(d.Today.USD), usd(d.Month.USD))
	var b strings.Builder
	fmt.Fprintf(&b, "🧾 <b>Gasto del modelo · %s</b>\n<b>%s</b>\n", app, esc(d.Headline))
	if d.CapUSD > 0 {
		pct := 0.0
		if d.CapUSD > 0 {
			pct = 100 * d.Today.USD / d.CapUSD
		}
		state := fmt.Sprintf("Techo diario %s · va el %.0f %% · faltan %s", usd(d.CapUSD), pct, usd(d.Remaining))
		if d.Today.Capped {
			state = fmt.Sprintf("Techo diario %s ALCANZADO — el modelo está apagado hasta mañana", usd(d.CapUSD))
		}
		b.WriteString(state + "\n")
	} else {
		b.WriteString("Sin techo diario (APPXIMO_ASK_DAILY_USD=0)\n")
	}
	if d.UserCapUSD > 0 {
		fmt.Fprintf(&b, "Techo por usuario %s\n", usd(d.UserCapUSD))
	}
	model := d.Today.Questions - d.Today.Parser - d.Today.Cache
	fmt.Fprintf(&b, "\n<b>Hoy: %d preguntas</b> · parser %d · caché %d · modelo %d (%d llamadas)\n", d.Today.Questions, d.Today.Parser, d.Today.Cache, model, d.Today.ModelCalls)
	if d.Share.Questions > 0 {
		fmt.Fprintf(&b, "<b>Últimos 30 días: %d</b> · parser %d (%.0f %%) · caché %d · modelo %d · %s\n", d.Share.Questions, d.Share.Parser, d.Share.ParserPct, d.Share.Cache, d.Share.Model, usd(d.Share.CostUSD))
		// Useful vs wasted (VOZ-AHORRO-S2 Part C): what bought an answer and
		// what bought a «no entendí» are not the same money.
		if d.Share.CostUSD > 0 {
			fmt.Fprintf(&b, "Gasto útil %s · desperdiciado %s (%d que no sirvieron)\n", usd(d.Share.UsefulUSD), usd(d.Share.WastedUSD), d.Share.Wasted)
		}
	}
	if len(d.TopCost) > 0 {
		b.WriteString("\n<b>Las que más cuestan</b>\n")
		for _, p := range d.TopCost {
			mark := ""
			if p.Wasted {
				mark = " ✗ no sirvió"
			} else if p.WastedUSD > 0 {
				mark = fmt.Sprintf(" · %s no sirvió", usd(p.WastedUSD))
			}
			fmt.Fprintf(&b, "• %s — %s (%d)%s\n", esc(short(p.Question, 48)), usd(p.CostUSD), p.Count, mark)
		}
	}
	if len(d.Fallbacks) > 0 {
		b.WriteString("\n<b>Van al modelo porque…</b>\n")
		for _, p := range d.Fallbacks {
			why := p.Fallback
			if why == "" {
				why = "—"
			}
			fmt.Fprintf(&b, "• %s — %s (%d)\n", esc(short(p.Question, 40)), esc(short(why, 40)), p.Count)
		}
	}
	if d.CacheHits+d.CacheMisses > 0 {
		fmt.Fprintf(&b, "\nCaché de planes desde el arranque: %d aciertos / %d fallos\n", d.CacheHits, d.CacheMisses)
	}
	d.Text = strings.TrimRight(b.String(), "\n")
}

// Report shapes the digest as the census card the summary renderer paints:
// the headline on top, one row per figure, the top phrases as rows with
// their cost as the number.
func (d *Digest) Report() summary.Report {
	level := summary.LevelGreen
	if d.CapUSD > 0 {
		switch {
		case d.Today.Capped:
			level = summary.LevelRed
		case d.Today.USD >= 0.8*d.CapUSD:
			level = summary.LevelAmber
		}
	}
	rep := summary.Report{AppName: d.AppName, Census: true, Subtitle: "Gasto del modelo · " + d.Day, Headline: d.Headline, Level: level}
	row := func(name, text string, n int64) {
		rep.Facts = append(rep.Facts, summary.Facts{Resource: name, Total: n, HasTotal: true, TotalText: text})
	}
	if d.CapUSD > 0 {
		row("techo diario", usd(d.CapUSD), 0)
		row("faltan para el techo", usd(d.Remaining), 0)
	}
	model := d.Today.Questions - d.Today.Parser - d.Today.Cache
	row("hoy · parser", "", int64(d.Today.Parser))
	row("hoy · caché", "", int64(d.Today.Cache))
	row("hoy · modelo", "", int64(model))
	if d.Share.Questions > 0 {
		row(fmt.Sprintf("30 días · parser %.0f %%", d.Share.ParserPct), "", int64(d.Share.Parser))
		row("30 días · modelo", usd(d.Share.CostUSD), int64(d.Share.Model))
		if d.Share.CostUSD > 0 {
			row("30 días · útil", usd(d.Share.UsefulUSD), int64(d.Share.Model-d.Share.Wasted))
			row("30 días · desperdiciado", usd(d.Share.WastedUSD), int64(d.Share.Wasted))
		}
	}
	for i, p := range d.TopCost {
		if i >= 4 {
			break
		}
		row("«"+short(p.Question, 26)+"»", usd(p.CostUSD), 0)
	}
	return rep
}

func short(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
