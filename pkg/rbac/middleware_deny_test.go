package rbac

import (
	"bytes"
	"github.com/rs/zerolog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDeny_UndeclaredRoleIndistinguishableToClient pins the ENG-27 contract,
// which is deliberately ASYMMETRIC:
//
//   - The CLIENT must not be able to tell "this role is not declared by any
//     schema role" from "this role is declared but lacks the permission".
//     Naming the difference in the response would turn every endpoint into an
//     enumeration oracle over the schema's role namespace.
//   - The OPERATOR must be able to tell, from the server log — that is where a
//     forged or typo'd role becomes visible, and the attacker cannot read it.
func TestDeny_UndeclaredRoleIndistinguishableToClient(t *testing.T) {
	policyJSON := []byte(`{"roles":{
		"viewer": {"resources": ["tasks"], "actions": ["read"]}
	}}`)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RBACMiddleware(policyJSON)(next)

	deny := func(role, method string) (*httptest.ResponseRecorder, string) {
		// The deny line is structured (zerolog, via the context logger); capture
		// it through zerolog's context fallback, not the std logger.
		var logBuf bytes.Buffer
		lg := zerolog.New(&logBuf)
		prev := zerolog.DefaultContextLogger
		zerolog.DefaultContextLogger = &lg
		defer func() { zerolog.DefaultContextLogger = prev }()

		req := httptest.NewRequest(method, "/api/tasks", nil)
		req.Header.Set("X-User-Role", role)
		req.Header.Set("X-User-ID", "u1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		// JSON escapes the quotes inside the detail field; compare unescaped.
		return rec, strings.ReplaceAll(logBuf.String(), `\"`, `"`)
	}

	// A role NO schema role declares, on an action a real role would also lack.
	ghostRec, ghostLog := deny("ghost_role", http.MethodPost)
	// A DECLARED role, denied because it lacks the action.
	viewerRec, viewerLog := deny("viewer", http.MethodPost)

	// Client view: byte-identical status, body and content type.
	if ghostRec.Code != http.StatusForbidden || viewerRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403/403, got %d/%d", ghostRec.Code, viewerRec.Code)
	}
	if ghostRec.Body.String() != viewerRec.Body.String() {
		t.Fatalf("bodies differ — enumeration oracle:\n undeclared: %q\n declared:   %q",
			ghostRec.Body.String(), viewerRec.Body.String())
	}
	if got, want := ghostRec.Header().Get("Content-Type"), viewerRec.Header().Get("Content-Type"); got != want {
		t.Fatalf("content-type differs: %q vs %q", got, want)
	}

	// Operator view: the log distinguishes the two.
	if !strings.Contains(ghostLog, `role "ghost_role" is not declared by any schema role`) {
		t.Fatalf("undeclared-role deny not distinguishable in log: %q", ghostLog)
	}
	if !strings.Contains(viewerLog, `role "viewer" is declared but not permitted "create" on "tasks"`) {
		t.Fatalf("declared-but-denied not distinguishable in log: %q", viewerLog)
	}
	// And the log never claims the wrong thing about either.
	if strings.Contains(ghostLog, "is declared but not permitted") ||
		strings.Contains(viewerLog, "is not declared") {
		t.Fatalf("logs crossed:\n ghost: %q\n viewer: %q", ghostLog, viewerLog)
	}
}
