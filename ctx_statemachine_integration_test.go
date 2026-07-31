//go:build integration

// LIBRARY-GAPS-S2 (ENG-7): Ctx.Update enforces a schema-declared state machine
// with EXACTLY the generated PATCH's semantics — same guard (one source of
// truth: codegen.AppendStateTransitionGuard), same race-safety, same 422
// message. Before this, a custom route had no engine path that kept the
// lifecycle enforced, so consumers re-stated the transition table in Go.
package appitools

import (
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/tests/helpers"
)

func newStateMachineApp(t *testing.T) *httptest.Server {
	t.Helper()
	app, err := New(Config{
		SchemaPath: filepath.Join(helpers.RepoRoot(), "examples", "model-lab", "state-machine.json"),
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })

	// The custom transition route, the way a consumer writes it now: Ctx.Update
	// and NO transition table — the engine owns which moves exist.
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_advance", Handler: func(ctx Ctx) error {
		var body struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		row, err := ctx.Update("orders", body.ID, map[string]any{"status": body.Status})
		if err != nil {
			return err // *InvalidTransitionError → the SAME 422; ErrUpdateConflict → 409
		}
		if row == nil {
			return ctx.Error(404, "order not found", nil)
		}
		return ctx.JSON(200, map[string]any{"status": row["status"]})
	}})

	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

func TestCtxUpdate_StateMachine(t *testing.T) {
	s, err := schema.LoadFromFile(filepath.Join(helpers.RepoRoot(), "examples", "model-lab", "state-machine.json"))
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}
	helpers.RegisterTenant(t, itPool, "smlab", s)
	srv := newStateMachineApp(t)
	token := helpers.GenToken(t, "admin", "u1", "smlab")

	// Seed an order via the GENERATED route (status defaults to pending).
	resp := do(t, srv, "POST", "/api/orders", "smlab.localhost", token, `{"code":"SM-1","total":10}`)
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		t.Fatalf("create order: %d", resp.StatusCode)
	}
	created := decode(t, resp)
	id, _ := created["id"].(string)
	if id == "" {
		if data, ok := created["data"].(map[string]any); ok {
			id, _ = data["id"].(string)
		}
	}
	if id == "" {
		t.Fatalf("no order id in %v", created)
	}

	advance := func(status string) (int, string) {
		t.Helper()
		resp := do(t, srv, "POST", "/api/_advance", "smlab.localhost", token,
			fmt.Sprintf(`{"id":%q,"status":%q}`, id, status))
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// 1. A DECLARED move succeeds through Ctx.Update.
	if code, body := advance("paid"); code != 200 {
		t.Fatalf("pending→paid via Ctx.Update: %d %s", code, body)
	}

	// 2. An ILLEGAL move fails with the GENERATED path's exact 422 message.
	code, body := advance("delivered")
	if code != 422 {
		t.Fatalf("paid→delivered via Ctx.Update: want 422, got %d %s", code, body)
	}
	wantMsg := `invalid transition for \"status\": from \"paid\" to \"delivered\" is not allowed`
	if !strings.Contains(body, wantMsg) {
		t.Errorf("Ctx.Update 422 body = %s, want the generated-path message (%s)", body, wantMsg)
	}
	// …and the generated PATCH refuses the SAME move with the SAME message.
	respGen := do(t, srv, "PATCH", "/api/orders/"+id, "smlab.localhost", token, `{"status":"delivered"}`)
	genBody, _ := io.ReadAll(respGen.Body)
	respGen.Body.Close()
	if respGen.StatusCode != 422 || !strings.Contains(string(genBody), wantMsg) {
		t.Errorf("generated PATCH: %d %s — the two paths must agree byte for byte on the message",
			respGen.StatusCode, genBody)
	}

	// 3. The legal chain still flows.
	if code, body := advance("shipped"); code != 200 {
		t.Fatalf("paid→shipped: %d %s", code, body)
	}
	if code, body := advance("delivered"); code != 200 {
		t.Fatalf("shipped→delivered: %d %s", code, body)
	}

	// 4. A TERMINAL state is immutable through Ctx.Update.
	if code, body := advance("paid"); code != 422 || !strings.Contains(body, "invalid transition") {
		t.Errorf("delivered→paid (terminal): want 422 invalid transition, got %d %s", code, body)
	}

	// 5. Re-sending the CURRENT value is a no-op success (self-set), like PUT/PATCH.
	if code, body := advance("delivered"); code != 200 {
		t.Errorf("delivered→delivered (self-set no-op): want 200, got %d %s", code, body)
	}

	// 6. An unknown id stays the handler's plain 404 (nil, nil contract intact).
	old := id
	id = "00000000-0000-0000-0000-000000000000"
	if code, body := advance("paid"); code != 404 {
		t.Errorf("unknown id: want 404, got %d %s", code, body)
	}
	id = old
}
