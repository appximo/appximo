package observability_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/observability"
)

// Client context (ip/user_agent/browser/os/country) round-trips through SQLite.
func TestSaveSlowTrace_ClientContext(t *testing.T) {
	st := openTempStore(t)
	if err := st.SaveSlowTrace("10", observability.TraceView{
		TraceID: "c1c1c1c1c1c1c1c1", TS: time.Now().UnixMicro(), Route: "POST /api/guides",
		TotalUS: 13000, Status: 500, ErrMsg: "boom",
		IP: "190.85.0.1", UserAgent: "Mozilla/5.0 (Windows NT 10.0) Chrome/148",
		Browser: "Chrome", OS: "Windows 10", Country: "CO",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := st.SlowTraces("10", 24)
	if err != nil || len(got) == 0 {
		t.Fatalf("SlowTraces: %v (n=%d)", err, len(got))
	}
	tv := got[0]
	if tv.IP != "190.85.0.1" || tv.Browser != "Chrome" || tv.OS != "Windows 10" || tv.Country != "CO" {
		t.Errorf("client context not preserved: %+v", tv)
	}
	if tv.UserAgent != "Mozilla/5.0 (Windows NT 10.0) Chrome/148" {
		t.Errorf("user_agent = %q", tv.UserAgent)
	}
}

// method + full_url + filtered headers round-trip through SQLite (for the
// reproducible-curl UI).
func TestSaveSlowTrace_CurlContext(t *testing.T) {
	st := openTempStore(t)
	if err := st.SaveSlowTrace("10", observability.TraceView{
		TraceID: "cur1cur1cur1cur1", TS: time.Now().UnixMicro(), Route: "POST /api/guides",
		TotalUS: 9000, Status: 422, ErrMsg: "code required",
		Method: "POST", FullURL: "http://10.localhost/api/guides?dry=1",
		Headers: map[string]string{
			"Host": "10.localhost", "Authorization": "[Filtered]", "Content-Type": "application/json",
		},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := st.SlowTraces("10", 24)
	if err != nil || len(got) == 0 {
		t.Fatalf("SlowTraces: %v (n=%d)", err, len(got))
	}
	tv := got[0]
	if tv.Method != "POST" {
		t.Errorf("method = %q", tv.Method)
	}
	if tv.FullURL != "http://10.localhost/api/guides?dry=1" {
		t.Errorf("full_url = %q (query string must survive)", tv.FullURL)
	}
	if tv.Headers["Host"] != "10.localhost" || tv.Headers["Authorization"] != "[Filtered]" ||
		tv.Headers["Content-Type"] != "application/json" {
		t.Errorf("headers not preserved: %+v", tv.Headers)
	}
}

// Re-opening the store at the same path re-runs the idempotent ALTER migrations
// without error, and existing rows survive.
func TestOpenStore_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "obs.db")
	st1, err := observability.OpenStore(path)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	if err := st1.SaveSlowTrace("10", observability.TraceView{
		TraceID: "abcabcabcabcabca", TS: time.Now().UnixMicro(), Route: "r", TotalUS: 60000, Status: 200,
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	st1.Close()

	st2, err := observability.OpenStore(path) // re-runs ADD COLUMN migrations
	if err != nil {
		t.Fatalf("open 2 (idempotent migration) failed: %v", err)
	}
	defer st2.Close()
	got, _ := st2.SlowTraces("10", 24)
	if len(got) != 1 || got[0].TraceID != "abcabcabcabcabca" {
		t.Fatalf("row did not survive reopen: %+v", got)
	}
}

// stack_json is persisted only for 500s; 4xx errors store err_msg but no stack.
func TestSaveSlowTrace_StackOnlyFor500(t *testing.T) {
	st := openTempStore(t)
	now := time.Now().UnixMicro()

	// 500 with a stack.
	if err := st.SaveSlowTrace("10", observability.TraceView{
		TraceID: "5005005005005005", TS: now, Route: "POST /api/guides", TotalUS: 13420, Status: 500,
		ErrMsg: "dial tcp: connection refused",
		Stack: []observability.Frame{
			{Function: "pkg/codegen.handleCreate", File: "pkg/codegen/builder.go", Line: 245},
			{Function: "pkg/extensions.Dispatch", File: "pkg/extensions/webhook_dispatcher.go", Line: 156},
		},
	}); err != nil {
		t.Fatalf("save 500: %v", err)
	}
	// 401 — error message, no stack.
	if err := st.SaveSlowTrace("10", observability.TraceView{
		TraceID: "4014014014014014", TS: now, Route: "GET /api/guides", TotalUS: 500, Status: 401,
		ErrMsg: "jwt: missing token",
	}); err != nil {
		t.Fatalf("save 401: %v", err)
	}
	// 422 — error message, no stack.
	if err := st.SaveSlowTrace("10", observability.TraceView{
		TraceID: "4224224224224224", TS: now, Route: "POST /api/guides", TotalUS: 820, Status: 422,
		ErrMsg: "rejected by hook",
	}); err != nil {
		t.Fatalf("save 422: %v", err)
	}

	got, err := st.SlowTraces("10", 24)
	if err != nil {
		t.Fatalf("SlowTraces: %v", err)
	}
	by := map[string]observability.TraceView{}
	for _, tv := range got {
		by[tv.TraceID] = tv
	}

	tv500, ok := by["5005005005005005"]
	if !ok {
		t.Fatal("500 trace not persisted")
	}
	if len(tv500.Stack) != 2 {
		t.Fatalf("500 should have 2 stack frames, got %d", len(tv500.Stack))
	}
	// roundtrip: frames survive serialization unchanged.
	if tv500.Stack[1].File != "pkg/extensions/webhook_dispatcher.go" || tv500.Stack[1].Line != 156 ||
		tv500.Stack[1].Function != "pkg/extensions.Dispatch" {
		t.Errorf("stack frame not preserved: %+v", tv500.Stack[1])
	}
	if tv500.ErrMsg != "dial tcp: connection refused" {
		t.Errorf("500 err_msg = %q", tv500.ErrMsg)
	}

	for _, id := range []string{"4014014014014014", "4224224224224224"} {
		tv := by[id]
		if len(tv.Stack) != 0 {
			t.Errorf("%s (4xx) must have NO stack, got %d frames", id, len(tv.Stack))
		}
		if tv.ErrMsg == "" {
			t.Errorf("%s should still carry its err_msg", id)
		}
	}
}
