//go:build integration

// CTX-PARITY-S1 — the guarantee that Ctx.Insert / Ctx.Update stay equivalent to
// the generated POST / PATCH.
//
// docs/BACKEND_SPEC_LLM.md tells consumers that Ctx.Insert validates "exactly
// like the generated POST". A third-party field report (VecinGo, 18 resources,
// 13 custom handlers) proved that false: schema `default` values were not
// applied, so rows landed with a NULL status and the NEXT state-machine
// transition failed with `invalid transition from ""`. Two implementations of
// one contract had drifted, and the documentation promised the parity that did
// not exist.
//
// This file is the anti-drift instrument, in the spirit of
// TestIntegration_DeclaredEqualsApplied for migrations: it runs the SAME
// payload through BOTH write paths and asserts the resulting rows are
// identical. It is deliberately written as a table of payloads, so closing a
// future divergence means adding a row here, not inventing a new test.
package appximo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/tests/helpers"
)

// paritySchema exercises every write-path behaviour that could diverge:
// a default on a state-machine field (the field report's headline), a plain
// default, a numeric field (Go int64 vs JSON float64), an enum, a required
// field and an auto timestamp.
const paritySchemaJSON = `{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "parity",
  "resources": {
    "orders": {
      "fields": {
        "reference":   { "type": "string", "required": true, "minLength": 1, "maxLength": 40 },
        "total_cents": { "type": "int64", "min": 0 },
        "priority":    { "type": "int", "default": 3 },
        "channel":     { "type": "string", "enum": ["web", "phone"], "default": "web" },
        "status": {
          "type": "string",
          "enum": ["pending", "paid", "shipped", "cancelled"],
          "default": "pending",
          "state_machine": {
            "initial": "pending",
            "transitions": {
              "pending":   ["paid", "cancelled"],
              "paid":      ["shipped"],
              "shipped":   [],
              "cancelled": []
            }
          }
        },
        "created_at": { "type": "time", "auto": true }
      }
    }
  },
  "rbac": { "roles": { "admin": { "resources": "*", "actions": ["*"] } } }
}`

func writeParitySchema(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "parity.json")
	if err := os.WriteFile(p, []byte(paritySchemaJSON), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return p
}

// newParityApp serves the parity schema plus two custom routes that write
// through the LIBRARY path, so both paths are reachable on one server.
func mustLoadSchema(t *testing.T, path string) *schema.APISchema {
	t.Helper()
	s, err := schema.LoadFromFile(path)
	if err != nil {
		t.Fatalf("load schema %s: %v", path, err)
	}
	return s
}

func newServerFor(t *testing.T, app *App) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

func newParityApp(t *testing.T) (*App, string) {
	t.Helper()
	schemaPath := writeParitySchema(t)
	app, err := New(Config{
		SchemaPath: schemaPath,
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })

	// The library create: the body is passed to Ctx.Insert verbatim.
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_lib_orders", Handler: func(ctx Ctx) error {
		var body map[string]any
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		row, err := ctx.Insert("orders", body)
		if err != nil {
			return err // surfaced verbatim — the engine's own 422/403 shape
		}
		return ctx.JSON(201, row)
	}})

	// The library create with NATIVE Go types (what a real handler computes),
	// rather than the float64 a JSON decode produces.
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_lib_orders_native", Handler: func(ctx Ctx) error {
		row, err := ctx.Insert("orders", map[string]any{
			"reference":   "NATIVE-1",
			"total_cents": int64(4200), // int64 from a Go computation or a previous read
			"priority":    int(7),
		})
		if err != nil {
			return err
		}
		return ctx.JSON(201, row)
	}})

	// The library update, for the PATCH-parity half.
	mustRegister(t, app, Route{Method: "PATCH", Path: "/api/_lib_orders_update", Handler: func(ctx Ctx) error {
		var body struct {
			ID   string         `json:"id"`
			Data map[string]any `json:"data"`
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		row, err := ctx.Update("orders", body.ID, body.Data)
		if err != nil {
			return err
		}
		if row == nil {
			return ctx.Error(404, "not found", nil)
		}
		return ctx.JSON(200, row)
	}})
	return app, schemaPath
}

// volatile columns differ by construction between two separate writes.
var parityVolatile = map[string]bool{"id": true, "created_at": true, "updated_at": true}

func comparableRow(row map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range row {
		if parityVolatile[k] {
			continue
		}
		// Numbers arrive as float64 through JSON on both sides; normalise the
		// representation so the comparison is about VALUES, not encodings.
		if n, ok := v.(json.Number); ok {
			f, _ := n.Float64()
			v = f
		}
		out[k] = v
	}
	return out
}

// TestParity_InsertMatchesGeneratedPOST is THE anti-drift guarantee: the same
// payload, through the generated POST and through Ctx.Insert, must produce the
// same row.
func TestParity_InsertMatchesGeneratedPOST(t *testing.T) {
	app, schemaPath := newParityApp(t)
	s := mustLoadSchema(t, schemaPath)
	srv := newServerFor(t, app)
	tenant := "parityins"
	helpers.RegisterTenant(t, itPool, tenant, s)
	token := helpers.GenToken(t, "admin", "u1", tenant)
	host := tenant + ".localhost"

	cases := []struct {
		name string
		body string
	}{
		{
			// The field report's exact shape: a create that omits the
			// state-machine field, relying on its declared default.
			name: "omits every defaulted field",
			body: `{"reference":"A-1"}`,
		},
		{
			name: "supplies some, omits others",
			body: `{"reference":"A-2","channel":"phone","total_cents":1500}`,
		},
		{
			name: "supplies every field explicitly",
			body: `{"reference":"A-3","channel":"web","priority":9,"total_cents":25,"status":"pending"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			genResp := do(t, srv, http.MethodPost, "/api/orders", host, token, tc.body)
			if genResp.StatusCode != http.StatusCreated {
				t.Fatalf("generated POST: want 201, got %d", genResp.StatusCode)
			}
			genRow := decode(t, genResp)

			// The library path needs a distinct unique-free reference only if the
			// schema declared uniqueness; it does not, so the same body is used.
			libResp := do(t, srv, http.MethodPost, "/api/_lib_orders", host, token, tc.body)
			if libResp.StatusCode != http.StatusCreated {
				b := decode(t, libResp)
				t.Fatalf("ctx.Insert: want 201, got %d (%v)", libResp.StatusCode, b)
			}
			libRow := decode(t, libResp)

			got, want := comparableRow(libRow), comparableRow(genRow)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ctx.Insert produced a DIFFERENT row than the generated POST for the same payload.\n"+
					"  generated: %v\n  ctx.Insert: %v\n"+
					"  This is the parity backend-spec promises; close the divergence in the shared write core, "+
					"not by patching one path.", want, got)
			}
			// The headline symptom, asserted explicitly so a regression names itself.
			if libRow["status"] == nil {
				t.Error("ctx.Insert left the state-machine field NULL: the declared default was not applied, " +
					"so the next transition fails with `invalid transition from \"\"`")
			}
		})
	}
}

// TestParity_InsertAcceptsNativeGoNumbers: it is a Go API. A value read from a
// row comes back as int64; handing that same value back to Insert must work.
func TestParity_InsertAcceptsNativeGoNumbers(t *testing.T) {
	app, schemaPath := newParityApp(t)
	s := mustLoadSchema(t, schemaPath)
	srv := newServerFor(t, app)
	tenant := "paritynum"
	helpers.RegisterTenant(t, itPool, tenant, s)
	token := helpers.GenToken(t, "admin", "u1", tenant)
	host := tenant + ".localhost"

	resp := do(t, srv, http.MethodPost, "/api/_lib_orders_native", host, token, `{}`)
	if resp.StatusCode != http.StatusCreated {
		b := decode(t, resp)
		t.Fatalf("ctx.Insert with native Go ints: want 201, got %d (%v) — "+
			"a Go API that rejects int64 on write while returning int64 on read is not usable",
			resp.StatusCode, b)
	}
	row := decode(t, resp)
	if fmt.Sprint(row["total_cents"]) != "4200" {
		t.Errorf("total_cents round-trip: want 4200, got %v", row["total_cents"])
	}
	if fmt.Sprint(row["priority"]) != "7" {
		t.Errorf("priority round-trip: want 7, got %v", row["priority"])
	}
}

// TestParity_ReadWriteRoundTrip closes the asymmetry the field report named:
// take a row the engine RETURNED and write its values straight back.
func TestParity_ReadWriteRoundTrip(t *testing.T) {
	app, schemaPath := newParityApp(t)
	s := mustLoadSchema(t, schemaPath)
	srv := newServerFor(t, app)
	tenant := "parityrt"
	helpers.RegisterTenant(t, itPool, tenant, s)
	token := helpers.GenToken(t, "admin", "u1", tenant)
	host := tenant + ".localhost"

	// Seed through the generated path, then re-insert what Ctx.Query returns.
	seed := do(t, srv, http.MethodPost, "/api/orders", host, token, `{"reference":"RT-1","total_cents":990,"priority":4}`)
	if seed.StatusCode != http.StatusCreated {
		t.Fatalf("seed: %d", seed.StatusCode)
	}
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_lib_roundtrip", Handler: func(ctx Ctx) error {
		rows, err := ctx.Query("orders", QueryOpts{Limit: 1})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return ctx.Error(404, "no seed row", nil)
		}
		src := rows[0]
		// Hand the engine back exactly what it gave us, minus the identity.
		clone := map[string]any{
			"reference":   fmt.Sprint(src["reference"]) + "-copy",
			"total_cents": src["total_cents"], // int64 from the driver
			"priority":    src["priority"],    // int32/int64 from the driver
		}
		row, err := ctx.Insert("orders", clone)
		if err != nil {
			return err
		}
		return ctx.JSON(201, row)
	}})
	srv2 := newServerFor(t, app)

	resp := do(t, srv2, http.MethodPost, "/api/_lib_roundtrip", host, token, `{}`)
	if resp.StatusCode != http.StatusCreated {
		b := decode(t, resp)
		t.Fatalf("read→write round trip failed with %d (%v) — the values came FROM the engine; "+
			"it must accept them back", resp.StatusCode, b)
	}
}

// TestParity_InsertEnforcesRowConditionOnCreate — the security half of the
// parity contract. The generated POST runs EnforceCreateRBAC, which FORCES the
// row-condition column to the caller's identity and REJECTS a body claiming
// another principal's id (the mass-assignment block). If Ctx.Insert only
// applies the field allowlist, a custom route becomes a way around a rule the
// REST API enforces — an owner-scoped role writing a row attributed to
// somebody else.
func TestParity_InsertEnforcesRowConditionOnCreate(t *testing.T) {
	schemaPath := writeOwnerSchema(t)
	app, err := New(Config{SchemaPath: schemaPath, DSN: itConnStr,
		JWTSecret: helpers.JWTSecret, AdminKey: helpers.AdminKey, Env: "test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	mustRegister(t, app, Route{Method: "POST", Path: "/api/libnotes", Handler: func(ctx Ctx) error {
		var body map[string]any
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "bad body", err)
		}
		row, err := ctx.Insert("notes", body)
		if err != nil {
			return err
		}
		return ctx.JSON(201, row)
	}})
	s := mustLoadSchema(t, schemaPath)
	tenant := "parityown"
	helpers.RegisterTenant(t, itPool, tenant, s)
	srv := newServerFor(t, app)
	host := tenant + ".localhost"
	tok := helpers.GenToken(t, "member", "user-alice", tenant)

	t.Run("the owner column is FORCED to the caller, not left to the body", func(t *testing.T) {
		for _, path := range []string{"/api/notes", "/api/libnotes"} {
			resp := do(t, srv, http.MethodPost, path, host, tok, `{"body":"mine"}`)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("%s: want 201, got %d", path, resp.StatusCode)
			}
			row := decode(t, resp)
			if row["owner_id"] != "user-alice" {
				t.Errorf("%s: owner_id = %v, want the caller's id — the row condition must be forced on create",
					path, row["owner_id"])
			}
		}
	})

	t.Run("a body claiming ANOTHER principal is refused on both paths", func(t *testing.T) {
		for _, path := range []string{"/api/notes", "/api/libnotes"} {
			resp := do(t, srv, http.MethodPost, path, host, tok, `{"body":"theirs","owner_id":"user-mallory"}`)
			if resp.StatusCode != http.StatusForbidden {
				b := decode(t, resp)
				t.Errorf("%s: want 403 (mass-assignment block), got %d (%v) — a custom route must not be a way "+
					"around a rule the REST API enforces", path, resp.StatusCode, b)
			}
		}
	})
}

func writeOwnerSchema(t *testing.T) string {
	t.Helper()
	const js = `{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "ownerparity",
  "resources": {
    "notes": {
      "fields": {
        "body":     { "type": "string", "required": true, "minLength": 1 },
        "owner_id": { "type": "string" }
      }
    }
  },
  "rbac": { "roles": {
    "member": {
      "resources": ["notes"],
      "actions": ["read", "create", "update"],
      "conditions": { "field": "owner_id", "op": "eq", "val": "$user_id" },
      "routes": { "libnotes": { "actions": ["create"] } }
    }
  } }
}`
	dir := t.TempDir()
	p := filepath.Join(dir, "owner.json")
	if err := os.WriteFile(p, []byte(js), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return p
}
