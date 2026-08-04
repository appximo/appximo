package appximo

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
)

// tasksSchema is a minimal quickstart-shaped schema: one resource `tasks` with a
// required title and a status enum, plus an admin (wildcard) role.
func tasksSchema() *schema.APISchema {
	return &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"tasks": {Fields: map[string]schema.FieldDef{
				"title":  {Type: "string", Required: true, MaxLength: ptrInt(200)},
				"status": {Type: "string", Enum: []string{"open", "done"}},
			}},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}
}

func ptrInt(i int) *int { return &i }

func noopHandler(Ctx) error { return nil }

func TestRegister_CollisionAndValidation(t *testing.T) {
	s := tasksSchema()

	t.Run("valid non-resource route registers", func(t *testing.T) {
		app := &App{schema: s}
		if err := app.Register(Route{Method: "POST", Path: "/api/_echo", Handler: noopHandler}); err != nil {
			t.Fatalf("expected /api/_echo to register, got: %v", err)
		}
		if err := app.Register(Route{Method: "post", Path: "/api/declarations/submit", Handler: noopHandler}); err != nil {
			t.Fatalf("expected /api/declarations/submit to register, got: %v", err)
		}
	})

	t.Run("collision with a generated resource route is rejected at boot", func(t *testing.T) {
		app := &App{schema: s}
		err := app.Register(Route{Method: "POST", Path: "/api/tasks", Handler: noopHandler})
		if err == nil || !strings.Contains(err.Error(), "collides") {
			t.Fatalf("expected collision error for /api/tasks, got: %v", err)
		}
		// Also the {id}-shadow shape under a resource prefix.
		if err := app.Register(Route{Method: "GET", Path: "/api/tasks/special", Handler: noopHandler}); err == nil {
			t.Fatal("expected collision error for /api/tasks/special (owned by resource prefix)")
		}
	})

	t.Run("non-/api path rejected", func(t *testing.T) {
		app := &App{schema: s}
		if err := app.Register(Route{Method: "GET", Path: "/foo", Handler: noopHandler}); err == nil {
			t.Fatal("expected error for path not starting with /api/")
		}
	})

	t.Run("bad method and nil handler rejected", func(t *testing.T) {
		app := &App{schema: s}
		if err := app.Register(Route{Method: "FETCH", Path: "/api/x", Handler: noopHandler}); err == nil {
			t.Fatal("expected error for invalid method")
		}
		if err := app.Register(Route{Method: "GET", Path: "/api/x", Handler: nil}); err == nil {
			t.Fatal("expected error for nil handler")
		}
	})

	t.Run("duplicate custom route rejected", func(t *testing.T) {
		app := &App{schema: s}
		if err := app.Register(Route{Method: "POST", Path: "/api/_echo", Handler: noopHandler}); err != nil {
			t.Fatalf("first register: %v", err)
		}
		if err := app.Register(Route{Method: "POST", Path: "/api/_echo", Handler: noopHandler}); err == nil {
			t.Fatal("expected error registering the same method+path twice")
		}
	})

	t.Run("register after Start rejected", func(t *testing.T) {
		app := &App{schema: s, started: true}
		if err := app.Register(Route{Method: "GET", Path: "/api/late", Handler: noopHandler}); err == nil {
			t.Fatal("expected error registering after Start")
		}
	})
}

// TestBindResource_ValidatesAgainstSchema confirms BindResource runs the same
// compiled rule engine as REST/GraphQL (no DB needed — validation is in-memory).
func TestBindResource_ValidatesAgainstSchema(t *testing.T) {
	s := tasksSchema()
	res := s.Resources["tasks"]
	eng := &engineRefs{
		schema:     s,
		validators: map[string]*schema.ResourceValidator{"tasks": schema.CompileRules(&res)},
		policy:     &rbac.Policy{},
	}

	t.Run("missing required field → ValidationError", func(t *testing.T) {
		rc := &requestCtx{
			w:   httptest.NewRecorder(),
			r:   httptest.NewRequest("POST", "/api/_x", strings.NewReader(`{"status":"open"}`)),
			eng: eng,
		}
		var dst map[string]any
		err := rc.BindResource("tasks", &dst)
		ve, ok := asValidationError(err)
		if !ok {
			t.Fatalf("expected *ValidationError, got %T: %v", err, err)
		}
		found := false
		for _, f := range ve.Fields {
			if f.Field == "title" && f.Rule == "required" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a required-title violation, got %+v", ve.Fields)
		}
	})

	t.Run("valid body binds into dst", func(t *testing.T) {
		rc := &requestCtx{
			w:   httptest.NewRecorder(),
			r:   httptest.NewRequest("POST", "/api/_x", strings.NewReader(`{"title":"buy milk","status":"open"}`)),
			eng: eng,
		}
		var dst struct {
			Title  string `json:"title"`
			Status string `json:"status"`
		}
		if err := rc.BindResource("tasks", &dst); err != nil {
			t.Fatalf("expected valid body to bind, got: %v", err)
		}
		if dst.Title != "buy milk" || dst.Status != "open" {
			t.Fatalf("unexpected bound struct: %+v", dst)
		}
	})

	t.Run("unknown resource → error", func(t *testing.T) {
		rc := &requestCtx{
			w:   httptest.NewRecorder(),
			r:   httptest.NewRequest("POST", "/api/_x", strings.NewReader(`{}`)),
			eng: eng,
		}
		if err := rc.BindResource("nope", nil); err == nil {
			t.Fatal("expected error for unknown resource")
		}
	})
}

// TestResponseBuffering confirms JSON buffers a success body and Error buffers an
// error body + returns a handledError (the middleware flushes after commit).
func TestResponseBuffering(t *testing.T) {
	t.Run("JSON buffers, does not write until flush", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rc := &requestCtx{w: rec, r: httptest.NewRequest("GET", "/", nil)}
		if err := rc.JSON(201, map[string]any{"ok": true}); err != nil {
			t.Fatalf("JSON: %v", err)
		}
		if rec.Code != 200 || rec.Body.Len() != 0 {
			t.Fatalf("JSON must not write before flush: code=%d bodylen=%d", rec.Code, rec.Body.Len())
		}
		rc.flush(rec)
		if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"ok":true`) {
			t.Fatalf("after flush: code=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("Error buffers + returns handledError", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rc := &requestCtx{w: rec, r: httptest.NewRequest("GET", "/", nil)}
		err := rc.Error(403, "forbidden", nil)
		var he *handledError
		if err == nil {
			t.Fatal("Error must return a non-nil error so the handler can return it")
		}
		if !asHandled(err, &he) {
			t.Fatalf("expected *handledError, got %T", err)
		}
		rc.flush(rec)
		if rec.Code != 403 || !strings.Contains(rec.Body.String(), "forbidden") {
			t.Fatalf("after flush: code=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func asHandled(err error, target **handledError) bool {
	he, ok := err.(*handledError)
	if ok {
		*target = he
	}
	return ok
}
