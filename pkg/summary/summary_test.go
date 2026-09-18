package summary

import (
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

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
	// pendiente is the only non-terminal state (pagado/cancelado are terminal).
	if len(p.PendingStates) != 1 || p.PendingStates[0] != "pendiente" {
		t.Errorf("pending states: want [pendiente], got %v", p.PendingStates)
	}
}

func TestCompose_OwnerLanguageAndMotion(t *testing.T) {
	facts := []Facts{
		{Resource: "pedidos", CreatedToday: 3, HasCreated: true, UpdatedToday: 1, HasUpdated: true,
			HasState: true, Pending: map[string]int64{"pendiente": 2}, PendingTotal: 2},
		{Resource: "clientes", CreatedToday: 0, HasCreated: true}, // nothing today — omitted
	}
	r := Compose("La Tiendita", "tiendita", "2026-09-18", facts)
	if !r.HasMotion {
		t.Fatal("expected motion")
	}
	for _, want := range []string{"La Tiendita", "2026-09-18", "pedidos", "🆕 3", "actualizad", "pendientes de alguien", "pendiente: 2"} {
		if !strings.Contains(r.Text, want) {
			t.Errorf("text must contain %q; got:\n%s", want, r.Text)
		}
	}
	// A resource with nothing today must NOT appear as a wall of zeros.
	if strings.Contains(r.Text, "clientes") {
		t.Errorf("a zero-motion resource must be omitted; got:\n%s", r.Text)
	}
}

func TestCompose_EmptyWithDignity(t *testing.T) {
	facts := []Facts{
		{Resource: "pedidos", CreatedToday: 0, HasCreated: true, UpdatedToday: 0, HasUpdated: true},
	}
	r := Compose("PetFriendly", "pet", "2026-09-18", facts)
	if r.HasMotion {
		t.Fatal("no motion expected")
	}
	if !strings.Contains(r.Text, "Sin movimiento hoy") {
		t.Errorf("empty day must say 'Sin movimiento hoy', got:\n%s", r.Text)
	}
	if strings.Contains(r.Text, ": 0") {
		t.Errorf("must not list zeros, got:\n%s", r.Text)
	}
}

func TestCompose_EscapesVocabularyButKeepsBoldMarkup(t *testing.T) {
	facts := []Facts{{Resource: "pe<di>dos", CreatedToday: 1, HasCreated: true}}
	r := Compose("A<b>", "t", "2026-09-18", facts)
	if strings.Contains(r.Text, "pe<di>dos") {
		t.Errorf("resource name must be HTML-escaped, got:\n%s", r.Text)
	}
	if !strings.Contains(r.Text, "<b>") {
		t.Errorf("deliberate <b> markup must survive, got:\n%s", r.Text)
	}
}
