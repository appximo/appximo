package extensions_test

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/extensions"
	"github.com/appximo/appximo/pkg/schema"
)

const identityWasmPath = "../../testdata/wasm/identity.wasm"

func newWasm(t *testing.T) *extensions.WasmRunner {
	t.Helper()
	wr, err := extensions.NewWasmRunner(context.Background())
	if err != nil {
		t.Fatalf("NewWasmRunner: %v", err)
	}
	t.Cleanup(func() { _ = wr.Close(context.Background()) })
	return wr
}

func loadIdentityWasm(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(identityWasmPath)
	if err != nil {
		t.Fatalf("read identity.wasm: %v", err)
	}
	return b
}

// Execute identity.wasm: output must equal input byte-for-byte.
func TestWasmRunner_Identity(t *testing.T) {
	wr := newWasm(t)
	wasm := loadIdentityWasm(t)
	input := []byte(`{"hello":"world","n":42}`)
	out, err := wr.Execute(context.Background(), "10", wasm, "transform", input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("identity mismatch:\n got %q\nwant %q", out, input)
	}
}

// Empty functionName defaults to "transform".
func TestWasmRunner_DefaultFunctionName(t *testing.T) {
	wr := newWasm(t)
	input := []byte("raw-bytes-123")
	out, err := wr.Execute(context.Background(), "10", loadIdentityWasm(t), "", input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Fatalf("got %q want %q", out, input)
	}
}

// An infinite-loop module must be cancelled via the context deadline.
func TestWasmRunner_TimeoutInfiniteLoop(t *testing.T) {
	wr := newWasm(t)
	wasm := loadIdentityWasm(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := wr.Execute(ctx, "10", wasm, "loop_forever", []byte("x"))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from the infinite-loop module, got nil")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("loop_forever was not interrupted promptly: took %v", elapsed)
	}
}

// The same module is compiled once; repeated Execute hits the cache. A different
// tenant scopes a separate cache entry (one extra compile).
func TestWasmRunner_CacheNoRecompile(t *testing.T) {
	wr := newWasm(t)
	wasm := loadIdentityWasm(t)
	for i := 0; i < 5; i++ {
		if _, err := wr.Execute(context.Background(), "10", wasm, "transform", []byte("x")); err != nil {
			t.Fatalf("Execute %d: %v", i, err)
		}
	}
	if c := wr.CompileCount(); c != 1 {
		t.Fatalf("expected 1 compilation for the same (tenant,module), got %d", c)
	}
	if _, err := wr.Execute(context.Background(), "11", wasm, "transform", []byte("x")); err != nil {
		t.Fatalf("Execute other tenant: %v", err)
	}
	if c := wr.CompileCount(); c != 2 {
		t.Fatalf("expected 2 compilations across 2 tenants, got %d", c)
	}
}

// Concurrent Execute on the same runner must be data-race free (run with -race).
func TestWasmRunner_ConcurrentNoRace(t *testing.T) {
	wr := newWasm(t)
	wasm := loadIdentityWasm(t)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			in := []byte(`{"i":` + strconv.Itoa(n) + `}`)
			out, err := wr.Execute(context.Background(), "10", wasm, "transform", in)
			if err != nil {
				t.Errorf("Execute: %v", err)
				return
			}
			if !bytes.Equal(out, in) {
				t.Errorf("got %q want %q", out, in)
			}
		}(i)
	}
	wg.Wait()
	// All 32 concurrent calls share one compiled module.
	if c := wr.CompileCount(); c != 1 {
		t.Fatalf("expected 1 compilation under concurrency, got %d", c)
	}
}

// A HookRunner with Type=="wasm" delegates to the WasmRunner and round-trips the
// payload through the identity module.
func TestHookRunner_WasmDelegates(t *testing.T) {
	wr := newWasm(t)
	wr.RegisterModule("identity", loadIdentityWasm(t))
	hr := extensions.NewHookRunnerWithWasm(extensions.NewJSSandbox(), nil, wr)

	hook := &schema.HookConfig{Type: "wasm", WasmModule: "identity", WasmFn: "transform"}
	payload := map[string]any{"nit": "800197268-4", "monto": 1000.0}
	res, err := hr.RunBeforeHook(context.Background(), hook, payload, map[string]any{"tenant_id": "10"})
	if err != nil {
		t.Fatalf("RunBeforeHook: %v", err)
	}
	if !res.Proceed {
		t.Fatalf("expected Proceed=true, got false (error=%q)", res.Error)
	}
	if res.Data["nit"] != "800197268-4" {
		t.Fatalf("identity wasm should preserve payload, got %v", res.Data)
	}
}

// A wasm hook referencing an unregistered module fails closed (Proceed=false).
func TestHookRunner_WasmUnregisteredFailsClosed(t *testing.T) {
	wr := newWasm(t)
	hr := extensions.NewHookRunnerWithWasm(extensions.NewJSSandbox(), nil, wr)
	hook := &schema.HookConfig{Type: "wasm", WasmModule: "missing"}
	res, err := hr.RunBeforeHook(context.Background(), hook, map[string]any{"x": 1.0}, nil)
	if err != nil {
		t.Fatalf("RunBeforeHook: %v", err)
	}
	if res.Proceed {
		t.Fatal("expected Proceed=false (fail closed) for a missing module")
	}
}

// validateNIT must be callable from inside a JS hook (registration check).
func TestJSSandbox_ValidateNITIntegration(t *testing.T) {
	sb := extensions.NewJSSandbox()
	script := `result.proceed = validateNIT(data.nit);`

	ok, err := sb.RunHook(context.Background(), script, map[string]any{"nit": "800197268-4"}, nil)
	if err != nil {
		t.Fatalf("RunHook(valid): %v", err)
	}
	if !ok.Proceed {
		t.Error("validateNIT('800197268-4') should be true from JS")
	}

	bad, err := sb.RunHook(context.Background(), script, map[string]any{"nit": "800197268-5"}, nil)
	if err != nil {
		t.Fatalf("RunHook(invalid): %v", err)
	}
	if bad.Proceed {
		t.Error("validateNIT('800197268-5') should be false from JS")
	}
}
