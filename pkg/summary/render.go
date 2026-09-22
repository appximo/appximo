package summary

// The digest as an IMAGE (VOZ-VISUAL-S1, Part A). Rendered ON THE SERVER,
// deterministically, from the same Report the text comes from — the numbers
// are the numbers /api/summary counted; nothing here is drawn or worded by a
// model. The image exists for one reason: on a phone, eighteen lines of text
// are read one by one and an image is read at a glance. So the layout is a
// HIERARCHY, not a table — what needs a human is on top and big (a traffic
// light and a headline you can read at arm's length in sunlight), what moved
// today is a short list, what is merely in flow is small and grey, and a wide
// app's tail is folded into "+N más". The text stays the accessibility
// fallback and always travels with the picture (never image-only).
//
// Pure Go, no browser, no cgo: image/png + golang.org/x/image/font (the
// opentype rasterizer) + the Go fonts (gofont/goregular, gofont/gobold —
// Latin coverage incl. accents and ñ). The binary delta is measured and
// written in the ADR (ADR-032). Emoji are not in the fonts, so the traffic
// light is a drawn disc, not a glyph.
//
// Rendering runs OFF the request hot path: only when the caller asks for
// ?format=png (the Telegram receiver and the scheduled consumer), never on a
// CRUD request. Cost measured in render_test.go (Benchmark) — single-digit
// milliseconds for the draw, the PNG encode dominates.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Geometry: the canvas is 800 px wide — 2× a 390 px phone's CSS width, so
// Telegram scales it down (never up) and a 36 px body renders ~17 CSS px, a
// 72 px number ~34 CSS px. Height is whatever the content needs, capped.
const (
	renderWidth  = 800
	renderPad    = 44
	renderMaxH   = 2400 // Telegram: width+height ≤ 10000, ratio ≤ 20; we stay far below
	maxAttention = 5    // individual rows before folding "+N más"
	maxMotion    = 6
	// maxSlots bounds the agenda lines per resource (text and picture, VOZ-16).
	maxSlots = 10
	maxFlow  = 4
)

// Palette — high contrast for a sunlit screen. Never the only channel: every
// tier also has a caption word, so colour-blind readers get the same reading.
var (
	inkColor   = color.RGBA{0x11, 0x18, 0x27, 0xff}
	mutedColor = color.RGBA{0x6b, 0x72, 0x80, 0xff}
	lineColor  = color.RGBA{0xe5, 0xe7, 0xeb, 0xff}
	whiteColor = color.RGBA{0xff, 0xff, 0xff, 0xff}

	redFg   = color.RGBA{0xb9, 0x1c, 0x1c, 0xff}
	redBg   = color.RGBA{0xfe, 0xe2, 0xe2, 0xff}
	amberFg = color.RGBA{0xb4, 0x53, 0x09, 0xff}
	amberBg = color.RGBA{0xfe, 0xf3, 0xc7, 0xff}
	greenFg = color.RGBA{0x15, 0x80, 0x3d, 0xff}
	greenBg = color.RGBA{0xdc, 0xfc, 0xe7, 0xff}
)

var (
	fontOnce    sync.Once
	fontBold    *opentype.Font
	fontRegular *opentype.Font
	fontErr     error
)

func loadFonts() error {
	fontOnce.Do(func() {
		fontBold, fontErr = opentype.Parse(gobold.TTF)
		if fontErr != nil {
			return
		}
		fontRegular, fontErr = opentype.Parse(goregular.TTF)
	})
	return fontErr
}

// faces are created per render (an opentype.Face carries a scratch buffer and
// is not safe for concurrent use; the receiver and the worker may render at
// the same moment). Creating one is cheap; parsing the font is done once.
type faces struct {
	title, headline, number, name, body, small, caption, footer font.Face
}

func newFaces() (*faces, error) {
	if err := loadFonts(); err != nil {
		return nil, err
	}
	mk := func(f *opentype.Font, size float64) (font.Face, error) {
		return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	}
	var fs faces
	var err error
	if fs.title, err = mk(fontBold, 44); err != nil {
		return nil, err
	}
	if fs.headline, err = mk(fontBold, 40); err != nil {
		return nil, err
	}
	if fs.number, err = mk(fontBold, 72); err != nil {
		return nil, err
	}
	if fs.name, err = mk(fontBold, 34); err != nil {
		return nil, err
	}
	if fs.body, err = mk(fontRegular, 32); err != nil {
		return nil, err
	}
	if fs.small, err = mk(fontRegular, 28); err != nil {
		return nil, err
	}
	if fs.caption, err = mk(fontBold, 24); err != nil {
		return nil, err
	}
	if fs.footer, err = mk(fontRegular, 22); err != nil {
		return nil, err
	}
	return &fs, nil
}

// canvas is the drawing surface plus a cursor (y grows downward).
type canvas struct {
	img *image.RGBA
	fs  *faces
	y   int
}

func (c *canvas) text(f font.Face, x, baseline int, col color.Color, s string) int {
	d := &font.Drawer{Dst: c.img, Src: image.NewUniform(col), Face: f, Dot: fixed.P(x, baseline)}
	d.DrawString(s)
	return d.Dot.X.Ceil()
}

func measure(f font.Face, s string) int { return font.MeasureString(f, s).Ceil() }

// fit truncates s with an ellipsis so it measures at most maxW.
func fit(f font.Face, s string, maxW int) string {
	if measure(f, s) <= maxW {
		return s
	}
	r := []rune(s)
	for len(r) > 1 {
		r = r[:len(r)-1]
		if measure(f, string(r)+"…") <= maxW {
			return string(r) + "…"
		}
	}
	return "…"
}

func (c *canvas) fillRect(r image.Rectangle, col color.Color) {
	draw.Draw(c.img, r.Intersect(c.img.Bounds()), image.NewUniform(col), image.Point{}, draw.Src)
}

// fillRoundRect paints a rounded rectangle (corner radius rad) by clipping the
// four corners against a circle — a few hundred thousand pixel tests at most.
func (c *canvas) fillRoundRect(r image.Rectangle, rad int, col color.Color) {
	r = r.Intersect(c.img.Bounds())
	rgba := color.RGBAModel.Convert(col).(color.RGBA)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if rad > 0 {
				cx, cy := x, y
				switch {
				case x < r.Min.X+rad && y < r.Min.Y+rad:
					cx, cy = r.Min.X+rad, r.Min.Y+rad
				case x >= r.Max.X-rad && y < r.Min.Y+rad:
					cx, cy = r.Max.X-rad-1, r.Min.Y+rad
				case x < r.Min.X+rad && y >= r.Max.Y-rad:
					cx, cy = r.Min.X+rad, r.Max.Y-rad-1
				case x >= r.Max.X-rad && y >= r.Max.Y-rad:
					cx, cy = r.Max.X-rad-1, r.Max.Y-rad-1
				}
				if cx != x || cy != y {
					dx, dy := x-cx, y-cy
					if dx*dx+dy*dy > rad*rad {
						continue
					}
				}
			}
			c.img.SetRGBA(x, y, rgba)
		}
	}
}

func (c *canvas) fillCircle(cx, cy, rad int, col color.Color) {
	rgba := color.RGBAModel.Convert(col).(color.RGBA)
	for y := cy - rad; y <= cy+rad; y++ {
		for x := cx - rad; x <= cx+rad; x++ {
			dx, dy := x-cx, y-cy
			if dx*dx+dy*dy <= rad*rad {
				c.img.SetRGBA(x, y, rgba)
			}
		}
	}
}

func (c *canvas) hline(y int) {
	c.fillRect(image.Rect(renderPad, y, renderWidth-renderPad, y+2), lineColor)
}

func levelColors(level string) (fg, bg color.RGBA) {
	switch level {
	case LevelRed:
		return redFg, redBg
	case LevelAmber:
		return amberFg, amberBg
	case LevelGreen:
		return greenFg, greenBg
	default: // "" — a neutral card (a question answer has no traffic light)
		return inkColor, lineColor
	}
}

// Render paints the report as a PNG. Deterministic: the same Report yields the
// same bytes (pinned by TestRender_Deterministic). Census reports render the
// same card with totals.
func Render(r Report) ([]byte, error) {
	img, err := RenderImage(r)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("summary: encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderImage is Render before encoding (tests inspect the geometry).
func RenderImage(r Report) (*image.RGBA, error) {
	fs, err := newFaces()
	if err != nil {
		return nil, fmt.Errorf("summary: fonts: %w", err)
	}
	// Draw on a tall scratch canvas, then crop to the used height.
	img := image.NewRGBA(image.Rect(0, 0, renderWidth, renderMaxH))
	c := &canvas{img: img, fs: fs}
	c.fillRect(img.Bounds(), whiteColor)
	contentW := renderWidth - 2*renderPad

	// ── Header: the app's name, then what this card is and the day ───────
	c.y = renderPad + 44
	app := r.AppName
	if app == "" {
		app = "tu app"
	}
	c.text(fs.title, renderPad, c.y, inkColor, fit(fs.title, app, contentW))
	c.y += 36
	sub := "Resumen del día"
	if r.Census {
		sub = "Estado ahora"
	}
	if r.Subtitle != "" {
		sub = r.Subtitle
	}
	if r.Day != "" {
		sub += " · " + r.Day
	}
	c.text(fs.small, renderPad, c.y, mutedColor, sub)
	c.y += 26

	// ── Traffic light + headline ─────────────────────────────────────────
	fg, bg := levelColors(r.Level)
	c.y += 26
	c.fillCircle(renderPad+22, c.y-14, 22, fg)
	// The headline is "<what> · <the comparison>": when it does not fit on one
	// line the comparison — the part worth opening the picture for — moves to
	// its own line instead of being cut off with an ellipsis.
	head, tail := r.Headline, ""
	if measure(fs.headline, head) > contentW-64 {
		if i := strings.Index(head, " · "); i > 0 {
			head, tail = head[:i], head[i+len(" · "):]
		}
	}
	c.text(fs.headline, renderPad+64, c.y, fg, fit(fs.headline, head, contentW-64))
	if tail != "" {
		c.y += 34
		c.text(fs.body, renderPad+64, c.y, fg, fit(fs.body, tail, contentW-64))
	}
	c.y += 36
	c.hline(c.y)
	c.y += 16

	if r.Census {
		renderCensus(c, r)
		return crop(img, c.y), nil
	}

	// ── Today's agenda (VOZ-16): the hour and the title of every block that
	// touches the day, before anything else — what a morning digest of an
	// agenda is FOR. One strip per range resource that has something today.
	agendaN := 0
	for _, f := range r.Facts {
		if f.HasToday && len(f.Today) > 0 {
			agendaN++
		}
	}
	for _, f := range r.Facts {
		if !f.HasToday || len(f.Today) == 0 {
			continue
		}
		caption := "HOY EN AGENDA"
		if agendaN > 1 {
			caption = "HOY · " + strings.ToUpper(f.Resource)
		}
		c.y += 6
		c.text(fs.caption, renderPad, c.y+20, mutedColor, fmt.Sprintf("%s (%d)", caption, len(f.Today)))
		c.y += 34
		timeW := 250
		for i, sl := range f.Today {
			if i == maxSlots {
				c.y += 30
				c.text(fs.small, renderPad, c.y, mutedColor, fmt.Sprintf("+%d más", len(f.Today)-maxSlots))
				c.y += 6
				break
			}
			line := SlotLine(sl, f.DayStart)
			hours, title := line, ""
			if sp := strings.IndexByte(line, ' '); sp > 0 {
				hours, title = line[:sp], line[sp+1:]
			}
			c.y += 38
			c.text(fs.name, renderPad, c.y, inkColor, fit(fs.name, hours, timeW-16))
			c.text(fs.body, renderPad+timeW, c.y, inkColor, fit(fs.body, title, contentW-timeW))
		}
		c.y += 18
		c.hline(c.y)
		c.y += 10
	}

	// ── Attention bands: declared (red) then inferred (amber) ────────────
	// Since VOZ-DELTA-S1 a band holds only what has NEWS or moved; rows that
	// are exactly as yesterday are folded below in one small grey line —
	// the picture stops repeating the same red every morning.
	declared := filter(r.Facts, func(f Facts) bool { return f.AttentionTotal > 0 && !f.AttentionInferred && !f.stale() })
	inferred := filter(r.Facts, func(f Facts) bool { return f.AttentionTotal > 0 && f.AttentionInferred && !f.stale() })
	staleRows := filter(r.Facts, func(f Facts) bool { return f.stale() })
	if r.Baseline == "" && r.AttentionTotal > 0 {
		c.y += 22
		c.text(fs.small, renderPad, c.y, mutedColor, "Primer resumen: sin comparación todavía.")
		c.y += 10
	}
	if len(declared) > 0 {
		bandFg, bandBg := redFg, redBg
		if r.Level != LevelRed {
			bandFg, bandBg = amberFg, amberBg // stock without news
		}
		renderAttentionBand(c, declared, "ESPERAN ACCIÓN", "", bandFg, bandBg)
	}
	if len(inferred) > 0 {
		renderAttentionBand(c, inferred, "SIN AVANZAR", "recién creados, nadie los movió", amberFg, amberBg)
	}
	if len(staleRows) > 0 {
		c.y += 6
		c.text(fs.caption, renderPad, c.y+20, mutedColor, "IGUAL QUE AYER")
		c.y += 34
		// Wrap into as many lines as needed (a wide app can have ten): the
		// line is small and grey, but it must not be cut off — it is the
		// reader's proof that the rest is accounted for.
		line := ""
		for _, f := range staleRows {
			item := fmt.Sprintf("%s %d", f.Resource, f.AttentionTotal)
			next := item
			if line != "" {
				next = line + " · " + item
			}
			if measure(fs.small, next) > contentW && line != "" {
				c.y += 30
				c.text(fs.small, renderPad, c.y, mutedColor, line)
				line = item
				continue
			}
			line = next
		}
		if line != "" {
			c.y += 30
			c.text(fs.small, renderPad, c.y, mutedColor, fit(fs.small, line, contentW))
		}
		c.y += 16
	}

	// ── Today's motion ───────────────────────────────────────────────────
	// Resources already in a band carry their motion on the band row, so HOY
	// lists only the rest — no resource is painted twice.
	moved := filter(r.Facts, func(f Facts) bool { return f.hasMotion() && f.AttentionTotal == 0 })
	if len(moved) > 0 {
		c.y += 10
		c.text(fs.caption, renderPad, c.y+20, mutedColor, "HOY")
		c.y += 34
		for i, f := range moved {
			if i == maxMotion {
				c.y += 30
				c.text(fs.small, renderPad, c.y, mutedColor, fmt.Sprintf("+%d recursos más con movimiento", len(moved)-maxMotion))
				c.y += 12
				break
			}
			var parts []string
			if f.HasCreated && f.CreatedToday > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", f.CreatedToday, nuevos(f.CreatedToday)))
			}
			if f.HasUpdated && f.UpdatedToday > 0 {
				parts = append(parts, fmt.Sprintf("%d actualizad%s", f.UpdatedToday, oS(f.UpdatedToday)))
			}
			c.y += 38
			nameW := 300
			c.text(fs.name, renderPad, c.y, inkColor, fit(fs.name, f.Resource, nameW-16))
			detail := strings.Join(parts, " · ")
			c.text(fs.body, renderPad+nameW, c.y, inkColor, fit(fs.body, detail, contentW-nameW))
			if f.FlowTotal > 0 {
				c.y += 32
				c.text(fs.small, renderPad+nameW, c.y, mutedColor, fit(fs.small, "en curso: "+plainDetail(f.Flow), contentW-nameW))
			}
			c.y += 8
		}
		c.y += 16
	}

	// ── Flow only (small, grey, folded) ──────────────────────────────────
	flowOnly := filter(r.Facts, func(f Facts) bool { return f.AttentionTotal == 0 && !f.hasMotion() && f.FlowTotal > 0 })
	if len(flowOnly) > 0 {
		c.hline(c.y)
		c.y += 14
		c.text(fs.caption, renderPad, c.y+20, mutedColor, "SIN NOVEDAD HOY")
		c.y += 34
		for i, f := range flowOnly {
			if i == maxFlow {
				c.y += 30
				c.text(fs.small, renderPad, c.y, mutedColor, fmt.Sprintf("+%d recursos más en curso", len(flowOnly)-maxFlow))
				break
			}
			c.y += 34
			line := f.Resource + " · " + plainDetail(f.Flow)
			c.text(fs.small, renderPad, c.y, mutedColor, fit(fs.small, line, contentW))
		}
		c.y += 12
	}

	// ── Empty day, with dignity ──────────────────────────────────────────
	if len(declared)+len(inferred)+len(moved)+agendaN == 0 {
		c.y += 40
		c.fillRoundRect(image.Rect(renderPad, c.y, renderWidth-renderPad, c.y+150), 20, bg)
		c.fillCircle(renderWidth/2, c.y+50, 24, fg)
		c.text(fs.headline, (renderWidth-measure(fs.headline, "Sin movimiento hoy"))/2, c.y+118, inkColor, "Sin movimiento hoy")
		c.y += 150 + 10
	}

	// ── Footer ───────────────────────────────────────────────────────────
	c.y += 28
	c.text(fs.footer, renderPad, c.y+10, mutedColor, "Contado por el motor sobre los datos reales · sin IA")
	c.y += 10 + renderPad
	return crop(img, c.y), nil
}

// renderAttentionBand paints one tier band: a caption, then one row per
// resource — the big number, the resource name, and the per-state detail —
// folding past maxAttention rows.
func renderAttentionBand(c *canvas, facts []Facts, caption, subcaption string, fg, bg color.RGBA) {
	fs := c.fs
	contentW := renderWidth - 2*renderPad
	// A row is two lines (number+name / states) and a third when the resource
	// also moved today — the motion is not squeezed into the state line.
	bandH := 62
	for i, f := range facts {
		if i == maxAttention {
			break
		}
		bandH += 98
		if f.hasMotion() || (f.HasNewToday && f.NewToday > 0) {
			bandH += 28
		}
	}
	if len(facts) > maxAttention {
		bandH += 40
	}
	if subcaption != "" {
		bandH += 30
	}
	top := c.y
	c.fillRoundRect(image.Rect(renderPad, top, renderWidth-renderPad, top+bandH), 20, bg)
	c.y += 40
	c.text(fs.caption, renderPad+24, c.y, fg, caption)
	if subcaption != "" {
		c.y += 30
		c.text(fs.small, renderPad+24, c.y, fg, fit(fs.small, subcaption, contentW-48))
	}
	c.y += 12
	// Biggest first inside the band — the number that matters most is on top.
	sorted := make([]Facts, len(facts))
	copy(sorted, facts)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].AttentionTotal > sorted[j].AttentionTotal })
	for i, f := range sorted {
		if i == maxAttention {
			c.y += 36
			c.text(fs.small, renderPad+24, c.y, fg, fmt.Sprintf("+%d recursos más esperan", len(sorted)-maxAttention))
			break
		}
		c.y += 74
		num := fmt.Sprintf("%d", f.AttentionTotal)
		numW := measure(fs.number, num)
		c.text(fs.number, renderPad+24, c.y, fg, num)
		x := renderPad + 24 + numW + 24
		if x < renderPad+170 {
			x = renderPad + 170
		}
		// The delta chip next to the name: "+3" / "−2" / "nuevo" — the change is
		// the news; the total is context.
		nameW := renderWidth - renderPad - 24 - x
		if chip := deltaChip(f); chip != "" {
			cw := measure(fs.name, chip) + 12
			c.text(fs.name, renderWidth-renderPad-24-cw+12, c.y-32, fg, chip)
			nameW -= cw + 12
		}
		c.text(fs.name, x, c.y-32, inkColor, fit(fs.name, f.Resource, nameW))
		c.text(fs.small, x, c.y+2, mutedColor, fit(fs.small, plainDetail(f.Attention), renderWidth-renderPad-24-x))
		if f.hasMotion() || (f.HasNewToday && f.NewToday > 0) {
			var m []string
			if f.HasNewToday && f.NewToday > 0 {
				m = append(m, fmt.Sprintf("%d %s hoy", f.NewToday, llegaron(f.NewToday)))
			}
			if f.HasCreated && f.CreatedToday > 0 {
				m = append(m, fmt.Sprintf("%d %s", f.CreatedToday, nuevos(f.CreatedToday)))
			}
			if f.HasUpdated && f.UpdatedToday > 0 {
				m = append(m, fmt.Sprintf("%d actualizad%s", f.UpdatedToday, oS(f.UpdatedToday)))
			}
			c.y += 28
			c.text(fs.small, x, c.y+2, mutedColor, fit(fs.small, strings.Join(m, " · "), renderWidth-renderPad-24-x))
		}
		c.y += 24
	}
	c.y = top + bandH + 20
}

func renderCensus(c *canvas, r Report) {
	fs := c.fs
	contentW := renderWidth - 2*renderPad
	listed := filter(r.Facts, func(f Facts) bool { return f.HasTotal })
	if len(listed) == 0 {
		c.y += 40
		c.text(fs.headline, renderPad, c.y, mutedColor, "No hay datos todavía")
		c.y += 20
	}
	for i, f := range listed {
		if i == 12 {
			c.y += 36
			c.text(fs.small, renderPad, c.y, mutedColor, fmt.Sprintf("+%d recursos más", len(listed)-12))
			break
		}
		c.y += 50
		num := fmt.Sprintf("%d", f.Total)
		if f.TotalText != "" {
			num = f.TotalText
		}
		numW := measure(fs.name, num)
		c.text(fs.name, renderWidth-renderPad-numW, c.y, inkColor, num)
		c.text(fs.body, renderPad, c.y, inkColor, fit(fs.body, f.Resource, contentW-numW-24))
	}
	c.y += 28
	c.text(fs.footer, renderPad, c.y+10, mutedColor, "Contado por el motor sobre los datos reales · sin IA")
	c.y += 10 + renderPad
}

// plainDetail is stateDetail without HTML escaping (an image needs none),
// "estado 3 · otro 1" style for a narrow line.
func plainDetail(m map[string]int64) string {
	type kv struct {
		k string
		v int64
	}
	kvs := make([]kv, 0, len(m))
	for k, v := range m {
		if v > 0 {
			kvs = append(kvs, kv{k, v})
		}
	}
	sort.Slice(kvs, func(i, j int) bool {
		if kvs[i].v != kvs[j].v {
			return kvs[i].v > kvs[j].v
		}
		return kvs[i].k < kvs[j].k
	})
	parts := make([]string, 0, len(kvs))
	for _, e := range kvs {
		parts = append(parts, fmt.Sprintf("%s %d", e.k, e.v))
	}
	return strings.Join(parts, " · ")
}

// deltaChip is the short change label painted beside a band row.
func deltaChip(f Facts) string {
	if !f.HasPrev {
		return ""
	}
	if f.PrevTotal == 0 && len(f.Prev) == 0 {
		return "nuevo"
	}
	switch d := f.Delta(); {
	case d > 0:
		return fmt.Sprintf("+%d", d)
	case d < 0:
		return fmt.Sprintf("−%d", -d)
	default:
		return "="
	}
}

func filter(facts []Facts, keep func(Facts) bool) []Facts {
	var out []Facts
	for _, f := range facts {
		if keep(f) {
			out = append(out, f)
		}
	}
	return out
}

func crop(img *image.RGBA, h int) *image.RGBA {
	if h > renderMaxH {
		h = renderMaxH
	}
	if h < 200 {
		h = 200
	}
	return img.SubImage(image.Rect(0, 0, renderWidth, h)).(*image.RGBA)
}
