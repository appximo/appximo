package observability_test

import (
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/observability"
)

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
