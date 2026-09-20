package schema

import (
	"strings"
	"testing"
)

// VOZ-AHORRO-S2 (ADR-038): `aliases` — how people name a resource or a state —
// are DECLARED in the schema and validated at load: an alias that points at
// nothing, repeats a declared name, or could mean two things is a load error
// naming the fix, never a word the voice parser silently guesses about.

func aliasSchemaJSON(ordenesAliases, estadoAliases, extra string) string {
	return `{
  "$schema": "https://appximo.com/schema/v1", "version": "1", "name": "t",
  "resources": {
    "ordenes": { ` + ordenesAliases + `"fields": {
      "estado": { "type": "string", "enum": ["creada","pendiente_pago","pagada","cancelada"], "default": "creada"` + estadoAliases + ` },
      "total_centavos": { "type": "int64" }
    } },
    "clientes": { "fields": { "nombre": { "type": "string" } } },
    "pagos": { "fields": { "estado": { "type": "string", "enum": ["pendiente","aprobado"] } } }` + extra + `
  },
  "rbac": { "roles": { "admin": { "resources": "*", "actions": ["*"] } } }
}`
}

func TestAliases_Validation(t *testing.T) {
	cases := []struct {
		name     string
		res      string // ordenes-level keys, before "fields"
		field    string // after estado's default
		extra    string // more resources
		wantRule string
	}{
		{"absent", ``, ``, ``, ""},
		{"resource aliases ok", `"aliases": ["pedidos", "ventas", "Compras"], `, ``, ``, ""},
		{"value aliases ok", ``, `, "aliases": {"pendiente_pago": ["sin pagar", "pendientes"], "pagada": ["cobrada", "pagas"]}`, ``, ""},
		{"same value alias on two resources is allowed (scoped by resource)", ``, `, "aliases": {"pendiente_pago": ["sin pagar"]}`,
			`, "facturas": { "fields": { "estado": { "type": "string", "enum": ["pendiente","emitida"], "aliases": {"pendiente": ["sin pagar"]} } } }`, ""},
		// resource aliases
		{"resource alias empty list", `"aliases": [], `, ``, ``, "alias_empty_list"},
		{"resource alias empty string", `"aliases": ["  "], `, ``, ``, "alias_empty"},
		{"resource alias too long", `"aliases": ["` + strings.Repeat("a", 41) + `"], `, ``, ``, "alias_too_long"},
		{"resource alias is its own name", `"aliases": ["orden"], `, ``, ``, "alias_is_resource_name"},
		{"resource alias is another resource's name (plural form)", `"aliases": ["cliente"], `, ``, ``, "alias_is_resource_name"},
		{"resource alias duplicated within the resource (accent/plural)", `"aliases": ["pedidos", "Pedido"], `, ``, ``, "alias_duplicate"},
		{"resource alias declared by two resources", `"aliases": ["ventas"], `, ``,
			`, "facturas": { "aliases": ["ventas"], "fields": { "n": { "type": "string" } } }`, "alias_ambiguous"},
		{"resource alias equals a declared value", `"aliases": ["pagadas"], `, ``, ``, "alias_is_value"},
		{"resource alias equals a value alias elsewhere", `"aliases": ["aprobados"], `, ``, ``, "alias_is_value"},
		// value aliases
		{"value alias key not in enum", ``, `, "aliases": {"enviada": ["despachada"]}`, ``, "alias_unknown_value"},
		{"value alias on a field without enum", ``, ``,
			`, "notas": { "fields": { "texto": { "type": "text", "aliases": {"x": ["y"]} } } }`, "alias_needs_enum"},
		{"value alias empty map", ``, `, "aliases": {}`, ``, "alias_empty_list"},
		{"value alias empty list", ``, `, "aliases": {"pagada": []}`, ``, "alias_empty_list"},
		{"value alias is the value's own form", ``, `, "aliases": {"pagada": ["pagadas"]}`, ``, "alias_is_value"},
		{"value alias repeats another value's alias in the resource", ``, `, "aliases": {"pagada": ["listas"], "cancelada": ["lista"]}`, ``, "alias_duplicate"},
		{"value alias equals another declared value of the resource", ``, `, "aliases": {"pagada": ["creadas"]}`, ``, "alias_duplicate"},
		{"value alias equals a resource name", ``, `, "aliases": {"pagada": ["pagos"]}`, ``, "alias_is_resource_name"},
		{"value alias equals a resource alias", `"aliases": ["ventas"], `, `, "aliases": {"pagada": ["venta"]}`, ``, "alias_is_resource_name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := parseFor(t, aliasSchemaJSON(tc.res, tc.field, tc.extra))
			if tc.wantRule == "" {
				if len(errs) != 0 {
					t.Fatalf("want valid, got %v", errs)
				}
				return
			}
			if !hasRule(errs, tc.wantRule) {
				t.Fatalf("want rule %q, got %v", tc.wantRule, errs)
			}
		})
	}
}

func TestAliases_FormsAreShared(t *testing.T) {
	// The forms the validator claims are the forms the parser matches — one
	// function, so "unique at load" and "recognized at runtime" agree.
	if got := NameForms("orden_lineas"); strings.Join(got, "|") != "orden lineas|orden linea|orden lineass|orden lineases" {
		t.Errorf("NameForms: %v", got)
	}
	if got := ValueForms("pendiente_pago"); strings.Join(got, "|") != "pendiente pago|pendiente pagos|pendiente de pago|pendiente de pagos" {
		t.Errorf("ValueForms multi-word: %v", got)
	}
	if got := ValueForms("Pagada"); strings.Join(got, "|") != "pagada|pagadas|pagadaes|pagado|pagados" {
		t.Errorf("ValueForms single: %v", got)
	}
	if NormalizeText("¿Cuántas Órdenes, sin-pagar?") != "cuantas ordenes sin pagar" {
		t.Errorf("NormalizeText: %q", NormalizeText("¿Cuántas Órdenes, sin-pagar?"))
	}
}
