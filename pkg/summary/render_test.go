package summary

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"testing"
	"time"
)

// twenty builds the case that motivated VOZ-VISUAL-S1: an app with twenty
// resources (VecinGo has eighteen), several waiting, several moving, several
// merely in flow, several silent — the digest must still read at a glance.
func twenty() []Facts {
	var facts []Facts
	for i := 1; i <= 20; i++ {
		f := Facts{Resource: fmt.Sprintf("recurso_%02d", i)}
		switch {
		case i <= 3: // declared attention
			f.HasState = true
			f.Attention = map[string]int64{"pendiente": int64(7 - i), "revision": 1}
			f.AttentionTotal = int64(8 - i)
			f.CreatedToday, f.HasCreated = int64(i), true
		case i <= 5: // inferred attention
			f.HasState = true
			f.AttentionInferred = true
			f.Attention = map[string]int64{"nueva": int64(i)}
			f.AttentionTotal = int64(i)
		case i <= 12: // motion today
			f.CreatedToday, f.HasCreated = int64(i), true
			f.UpdatedToday, f.HasUpdated = 1, true
		case i <= 17: // flow only
			f.HasState = true
			f.Flow = map[string]int64{"activo": int64(i * 3)}
			f.FlowTotal = int64(i * 3)
		default: // silent
			f.HasCreated = true
		}
		facts = append(facts, f)
	}
	return facts
}

func decode(t *testing.T, b []byte) (w, h int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("png decode: %v", err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func TestRender_Deterministic(t *testing.T) {
	r := Compose("La Tiendita", "t", "2026-09-18", Order(nil, twenty()), nil)
	a, err := Render(r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Render(r)
	if !bytes.Equal(a, b) {
		t.Fatal("the same report must render to the same bytes")
	}
	w, h := decode(t, a)
	if w != renderWidth || h < 400 || h > renderMaxH {
		t.Fatalf("geometry: %dx%d", w, h)
	}
	if len(a) > 300<<10 {
		t.Fatalf("png too large: %d bytes", len(a))
	}
}

// Twenty resources must fold, not stretch: the image height stays bounded
// and does not grow linearly with the resource count.
func TestRender_TwentyResourcesFold(t *testing.T) {
	five := Compose("A", "t", "2026-09-18", Order(nil, twenty()[:5]), nil)
	big := Compose("A", "t", "2026-09-18", Order(nil, twenty()), nil)
	b5, _ := Render(five)
	b20, _ := Render(big)
	_, h5 := decode(t, b5)
	_, h20 := decode(t, b20)
	if h20 > 2*h5+200 {
		t.Fatalf("20 resources must fold: h5=%d h20=%d", h5, h20)
	}
	// One phone screen at 2× is ~1 700 px; the first-digest note (a one-day
	// artifact) adds a line, so the guard is 1 760.
	if h20 > 1760 {
		t.Fatalf("20 resources render taller than a phone can take in: %d px", h20)
	}
}

func TestRender_EmptyDayAndCensus(t *testing.T) {
	empty := Compose("Vet", "t", "2026-09-18", []Facts{{Resource: "pets", HasCreated: true}}, nil)
	b, err := Render(empty)
	if err != nil {
		t.Fatal(err)
	}
	_, h := decode(t, b)
	if h < 300 || h > 900 {
		t.Fatalf("empty day card height: %d", h)
	}
	census := ComposeCensus("Vet", "t", []Facts{{Resource: "pets", Total: 12, HasTotal: true}}, nil)
	if _, err := Render(census); err != nil {
		t.Fatal(err)
	}
}

// Writes the rendered cases to SUMMARY_RENDER_OUT (a directory) so a human can
// look at them — the test itself only checks they render.
func TestRender_WriteSamples(t *testing.T) {
	dir := os.Getenv("SUMMARY_RENDER_OUT")
	if dir == "" {
		t.Skip("set SUMMARY_RENDER_OUT=<dir> to write sample PNGs")
	}
	cases := map[string]Report{
		"twenty": Compose("VecinGo", "vecingo", "2026-09-18", Order(nil, twenty()), nil),
		"five": Compose("La Tiendita", "tiendita", "2026-09-18", Order(nil, []Facts{
			{Resource: "ordenes", CreatedToday: 4, HasCreated: true, UpdatedToday: 2, HasUpdated: true, HasState: true,
				Attention: map[string]int64{"pagada": 2, "preparando": 1}, AttentionTotal: 3, Flow: map[string]int64{"enviada": 5}, FlowTotal: 5},
			{Resource: "pagos", CreatedToday: 4, HasCreated: true, HasState: true, Attention: map[string]int64{"pendiente": 1}, AttentionTotal: 1},
			{Resource: "clientes", CreatedToday: 2, HasCreated: true},
			{Resource: "productos", HasState: true, Flow: map[string]int64{"activo": 9, "borrador": 2}, FlowTotal: 11},
			{Resource: "cupones", HasCreated: true},
		}), nil),
		"empty": Compose("PetFriendly", "pet", "2026-09-18", []Facts{{Resource: "pets", HasCreated: true}}, nil),
	}
	for name, rep := range cases {
		b, err := Render(rep)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir+"/"+name+".png", b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkRender_Five(b *testing.B) {
	r := Compose("La Tiendita", "t", "2026-09-18", Order(nil, twenty()[:5]), nil)
	for i := 0; i < b.N; i++ {
		if _, err := Render(r); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRender_Twenty(b *testing.B) {
	r := Compose("VecinGo", "t", "2026-09-18", Order(nil, twenty()), nil)
	for i := 0; i < b.N; i++ {
		if _, err := Render(r); err != nil {
			b.Fatal(err)
		}
	}
}

// The picture carries today's agenda as its first strip (VOZ-16) and stays
// deterministic and bounded.
func TestRender_TodayAgendaStrip(t *testing.T) {
	bog, _ := time.LoadLocation("America/Bogota")
	day := time.Date(2026, 9, 22, 0, 0, 0, 0, bog)
	var slots []Slot
	for i := 0; i < 12; i++ {
		slots = append(slots, Slot{Start: time.Date(2026, 9, 22, 12+i, 0, 0, 0, time.UTC), End: time.Date(2026, 9, 22, 13+i, 0, 0, 0, time.UTC), Title: "compromiso número " + string(rune('A'+i))})
	}
	facts := append([]Facts{{Resource: "compromisos", HasToday: true, DayStart: day, Today: slots}}, twenty()[:3]...)
	with := Compose("Agenda", "t", "2026-09-22", Order(nil, facts), nil)
	without := Compose("Agenda", "t", "2026-09-22", Order(nil, twenty()[:3]), nil)
	a, err := Render(with)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Render(with)
	if !bytes.Equal(a, b) {
		t.Fatal("deterministic")
	}
	c, _ := Render(without)
	_, hWith := decode(t, a)
	_, hWithout := decode(t, c)
	if hWith <= hWithout || hWith-hWithout > 12*38+120 {
		t.Fatalf("the strip must add bounded height: with=%d without=%d", hWith, hWithout)
	}
	// An agenda-only day is not painted as an empty day.
	only := Compose("Agenda", "t", "2026-09-22", []Facts{{Resource: "compromisos", HasToday: true, DayStart: day, Today: slots[:2]}}, nil)
	img, err := RenderImage(only)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dy() < 300 {
		t.Fatalf("too short: %d", img.Bounds().Dy())
	}
}
