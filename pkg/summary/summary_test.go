package summary

import (
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

func smOrders(pending []string) *schema.StateMachine {
	return &schema.StateMachine{
		Initial: []string{"creada"},
		Transitions: map[string][]string{
			"creada":     {"pagada", "cancelada"},
			"pagada":     {"preparando"},
			"preparando": {"enviada"},
			"enviada":    {"entregada"},
			"entregada":  {"cerrada"},
			"cerrada":    {},
			"cancelada":  {},
		},
		Pending: pending,
	}
}

func TestPlanFor_DerivesFromSchemaNotNames(t *testing.T) {
	res := schema.ResourceSchema{Fields: map[string]schema.FieldDef{
		"titulo":        {Type: "string"},
		"creado_en":     {Type: "time", Auto: schema.AutoCreate},
		"modificado_en": {Type: "time", Auto: schema.AutoUpdate},
		"estado": {Type: "string", Enum: []string{"pendiente", "pagado", "cancelado"},
			StateMachine: &schema.StateMachine{
				Initial: []string{"pendiente"},
				Transitions: map[string][]string{
					"pendiente": {"pagado", "cancelado"},
					"pagado":    {},
					"cancelado": {},
				},
			}},
	}}
	p := PlanFor("pedidos", &res)
	if p.CreatedTsField != "creado_en" {
		t.Errorf("created ts: want creado_en, got %q", p.CreatedTsField)
	}
	if p.UpdatedTsField != "modificado_en" {
		t.Errorf("updated ts: want modificado_en, got %q", p.UpdatedTsField)
	}
	if p.StateField != "estado" {
		t.Errorf("state field: want estado, got %q", p.StateField)
	}
	// Nothing declared: pendiente is the initial non-terminal state → INFERRED
	// attention; pagado/cancelado are terminal → never anything.
	if len(p.Attention) != 1 || p.Attention[0] != "pendiente" || !p.AttentionInferred {
		t.Errorf("inferred attention: want [pendiente] inferred, got %v inferred=%v", p.Attention, p.AttentionInferred)
	}
	if len(p.Flow) != 0 {
		t.Errorf("flow: want none, got %v", p.Flow)
	}
}

// VOZ-2: the three declarations of `pending` and what each means.
func TestPlanFor_PendingTiers(t *testing.T) {
	mk := func(pending []string) Plan {
		res := schema.ResourceSchema{Fields: map[string]schema.FieldDef{
			"estado": {Type: "string", StateMachine: smOrders(pending)},
		}}
		return PlanFor("ordenes", &res)
	}
	// Declared: exactly those, in declaration order; the rest of the
	// non-terminal states are flow; terminal states appear nowhere.
	p := mk([]string{"preparando", "pagada"})
	if strings.Join(p.Attention, ",") != "preparando,pagada" || p.AttentionInferred {
		t.Errorf("declared: want [preparando pagada] not inferred, got %v inferred=%v", p.Attention, p.AttentionInferred)
	}
	if strings.Join(p.Flow, ",") != "creada,entregada,enviada" {
		t.Errorf("declared flow: got %v", p.Flow)
	}
	// Absent: the initial state is inferred; everything else is flow.
	p = mk(nil)
	if strings.Join(p.Attention, ",") != "creada" || !p.AttentionInferred {
		t.Errorf("inferred: want [creada] inferred, got %v inferred=%v", p.Attention, p.AttentionInferred)
	}
	if strings.Join(p.Flow, ",") != "entregada,enviada,pagada,preparando" {
		t.Errorf("inferred flow: got %v", p.Flow)
	}
	// Explicit []: nothing waits; every non-terminal state is flow.
	p = mk([]string{})
	if len(p.Attention) != 0 || p.AttentionInferred {
		t.Errorf("explicit none: want no attention, got %v inferred=%v", p.Attention, p.AttentionInferred)
	}
	if strings.Join(p.Flow, ",") != "creada,entregada,enviada,pagada,preparando" {
		t.Errorf("explicit none flow: got %v", p.Flow)
	}
	for _, tier := range [][]string{p.Attention, p.Flow} {
		for _, s := range tier {
			if s == "cerrada" || s == "cancelada" {
				t.Errorf("a terminal state must never be counted, got %q in %v", s, tier)
			}
		}
	}
}

func TestOrder_DeclaredListWinsElseAttentionFirst(t *testing.T) {
	facts := []Facts{
		{Resource: "productos", FlowTotal: 9, Flow: map[string]int64{"activo": 9}, HasState: true},
		{Resource: "clientes", CreatedToday: 2, HasCreated: true},
		{Resource: "ordenes", AttentionTotal: 3, Attention: map[string]int64{"pagada": 3}, HasState: true},
		{Resource: "reservas", AttentionTotal: 1, AttentionInferred: true, Attention: map[string]int64{"activa": 1}, HasState: true},
		{Resource: "zzz"},
	}
	got := Order(nil, facts)
	names := make([]string, 0, len(got))
	for _, f := range got {
		names = append(names, f.Resource)
	}
	if strings.Join(names, ",") != "ordenes,reservas,clientes,productos,zzz" {
		t.Errorf("default order: got %v", names)
	}
	got = Order(&schema.SummaryConfig{Resources: []string{"clientes", "ordenes", "ghost"}}, facts)
	names = names[:0]
	for _, f := range got {
		names = append(names, f.Resource)
	}
	if strings.Join(names, ",") != "clientes,ordenes" {
		t.Errorf("declared order must be verbatim, unknown/unreadable skipped: got %v", names)
	}
}

func TestCompose_VocabularyByTier(t *testing.T) {
	facts := []Facts{
		{Resource: "ordenes", CreatedToday: 3, HasCreated: true, UpdatedToday: 1, HasUpdated: true,
			HasState: true, Attention: map[string]int64{"pagada": 2, "preparando": 1}, AttentionTotal: 3,
			Flow: map[string]int64{"enviada": 5}, FlowTotal: 5},
		{Resource: "productos", HasState: true, Flow: map[string]int64{"activo": 9, "borrador": 2}, FlowTotal: 11},
		{Resource: "clientes", CreatedToday: 0, HasCreated: true}, // nothing today — omitted
	}
	r := Compose("La Tiendita", "tiendita", "2026-09-18", Order(nil, facts), nil)
	if !r.HasMotion || r.Level != LevelRed || r.AttentionTotal != 3 {
		t.Fatalf("expected red motion with 3 attention, got level=%s total=%d motion=%v", r.Level, r.AttentionTotal, r.HasMotion)
	}
	for _, want := range []string{"La Tiendita", "2026-09-18", "🔴", "3 esperan acción", "⏳ 3 esperan acción (pagada: 2, preparando: 1)", "🆕 3 nuevos", "actualizad", "▫️ en curso: enviada: 5", "Sin novedad hoy: productos (activo: 9, borrador: 2)"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("text must contain %q; got:\n%s", want, r.Text)
		}
	}
	// The old wording is gone: a flow state is never "pendiente de alguien".
	if strings.Contains(r.Text, "pendientes de alguien") || strings.Contains(r.Text, "activo: 9 pend") {
		t.Errorf("flow states must not be called pending:\n%s", r.Text)
	}
	if strings.Contains(r.Text, "clientes") {
		t.Errorf("a zero-motion resource must be omitted; got:\n%s", r.Text)
	}
}

func TestCompose_InferredIsHumble(t *testing.T) {
	facts := []Facts{
		{Resource: "reservas", HasState: true, AttentionInferred: true, Attention: map[string]int64{"activa": 2}, AttentionTotal: 2},
	}
	r := Compose("Vet", "vet", "2026-09-18", facts, nil)
	if r.Level != LevelAmber {
		t.Fatalf("inferred only → amber, got %s", r.Level)
	}
	for _, want := range []string{"🟡", "2 sin avanzar", "nadie los movió", "🕐 2 sin avanzar (activa: 2)"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("text must contain %q; got:\n%s", want, r.Text)
		}
	}
	if strings.Contains(r.Text, "esperan acción") {
		t.Errorf("an inferred tier must never claim someone is waiting:\n%s", r.Text)
	}
}

func TestCompose_EmptyWithDignity(t *testing.T) {
	facts := []Facts{
		{Resource: "pedidos", CreatedToday: 0, HasCreated: true, UpdatedToday: 0, HasUpdated: true},
	}
	r := Compose("PetFriendly", "pet", "2026-09-18", facts, nil)
	if r.HasMotion || r.Level != LevelGreen {
		t.Fatalf("no motion expected, green; got motion=%v level=%s", r.HasMotion, r.Level)
	}
	for _, want := range []string{"Sin movimiento hoy", "🟢", "Nada que atender"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("empty day must say %q, got:\n%s", want, r.Text)
		}
	}
	if strings.Contains(r.Text, ": 0") {
		t.Errorf("must not list zeros, got:\n%s", r.Text)
	}
}

func TestCompose_EscapesVocabularyButKeepsBoldMarkup(t *testing.T) {
	facts := []Facts{{Resource: "pe<di>dos", CreatedToday: 1, HasCreated: true}}
	r := Compose("A<b>", "t", "2026-09-18", facts, nil)
	if strings.Contains(r.Text, "pe<di>dos") {
		t.Errorf("resource name must be HTML-escaped, got:\n%s", r.Text)
	}
	if !strings.Contains(r.Text, "<b>") {
		t.Errorf("deliberate <b> markup must survive, got:\n%s", r.Text)
	}
}

func TestComposeCensus(t *testing.T) {
	r := ComposeCensus("Tienda", "t", []Facts{{Resource: "ordenes", Total: 7, HasTotal: true}, {Resource: "x"}}, nil)
	if !strings.Contains(r.Text, "<b>ordenes</b>: 7") || strings.Contains(r.Text, "<b>x</b>") || !r.Census {
		t.Errorf("census text wrong:\n%s", r.Text)
	}
	if r := ComposeCensus("Tienda", "t", nil, nil); !strings.Contains(r.Text, "No hay datos todavía") {
		t.Errorf("empty census: %s", r.Text)
	}
}
