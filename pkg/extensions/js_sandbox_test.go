package extensions_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/extensions"
	"github.com/dop251/goja"
)

func newSandbox() *extensions.JSSandbox { return extensions.NewJSSandbox() }

// Test 1: valid script that calculates VAT.
func TestRunHook_IVACalculation(t *testing.T) {
	sb := newSandbox()
	payload := map[string]any{"subtotal": 105.0}

	result, err := sb.RunHook(context.Background(),
		`data.iva = data.subtotal * 0.19; result.data = data;`,
		payload, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Proceed {
		t.Fatalf("expected Proceed=true, got false (Error: %s)", result.Error)
	}
	ivaRaw, ok := result.Data["iva"]
	if !ok {
		t.Fatal("expected 'iva' field in result.Data")
	}
	iva, ok := ivaRaw.(float64)
	if !ok {
		t.Fatalf("expected float64 for iva, got %T (%v)", ivaRaw, ivaRaw)
	}
	const want = 19.95
	if iva < want-0.001 || iva > want+0.001 {
		t.Errorf("expected iva ~%.2f, got %.4f", want, iva)
	}
}

// Test 2: script that explicitly rejects the operation.
func TestRunHook_ScriptRejects(t *testing.T) {
	sb := newSandbox()
	payload := map[string]any{"weight_kg": 200.0}

	result, err := sb.RunHook(context.Background(), `
		if (data.weight_kg > 100) {
			result.proceed = false;
			result.error = "weight exceeds maximum allowed";
		}
	`, payload, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Proceed {
		t.Error("expected Proceed=false")
	}
	if result.Error == "" {
		t.Error("expected non-empty Error")
	}
	if !strings.Contains(result.Error, "weight") {
		t.Errorf("unexpected error message: %q", result.Error)
	}
}

// Test 3: infinite loop must be interrupted by the 80ms watchdog.
// The error must be *goja.InterruptedError and arrive in <100ms.
func TestRunHook_WatchdogInterruptsInUnder100ms(t *testing.T) {
	sb := newSandbox()

	start := time.Now()
	result, err := sb.RunHook(context.Background(), `while(true){}`, nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected error from infinite loop, got result: %+v", result)
	}
	var ie *goja.InterruptedError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *goja.InterruptedError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected 'timeout' in error, got: %v", err)
	}
	if elapsed >= watchdogBudget {
		t.Errorf("watchdog took %v, expected <%v", elapsed, watchdogBudget)
	}
}

// Test 3b: legacy timeout test — alias for watchdog test.
func TestRunHook_Timeout(t *testing.T) {
	sb := newSandbox()

	result, err := sb.RunHook(context.Background(), `while(true){}`, nil, nil)

	if err == nil {
		t.Fatalf("expected timeout error, got result: %+v", result)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout in error, got: %v", err)
	}
}

// Test 4: syntax error in script must return Proceed=false with a non-empty Error.
func TestRunHook_SyntaxError(t *testing.T) {
	sb := newSandbox()

	result, err := sb.RunHook(context.Background(),
		`this is not valid javascript {{{`, nil, nil)

	if err != nil {
		t.Fatalf("unexpected error (wanted HookResult): %v", err)
	}
	if result.Proceed {
		t.Error("expected Proceed=false for syntax error")
	}
	if result.Error == "" {
		t.Error("expected non-empty Error for syntax error")
	}
}

// Test 5: script that sets data.total must be reflected in result.Data.
func TestRunHook_ModifiesDataTotal(t *testing.T) {
	sb := newSandbox()
	payload := map[string]any{"price": 50.0, "quantity": 3.0}

	result, err := sb.RunHook(context.Background(), `
		data.total = data.price * data.quantity;
		result.data = data;
	`, payload, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Proceed {
		t.Fatalf("expected Proceed=true, got false")
	}
	totalRaw, ok := result.Data["total"]
	if !ok {
		t.Fatal("expected 'total' key in result.Data")
	}
	var total float64
	switch v := totalRaw.(type) {
	case float64:
		total = v
	case int64:
		total = float64(v)
	default:
		t.Fatalf("unexpected type for total: %T (%v)", totalRaw, totalRaw)
	}
	if total != 150.0 {
		t.Errorf("expected total=150, got %v", total)
	}
}

// Test 6: semaphore does not block a concurrent goroutine serving a fast hook
// while a slow (interrupted) hook holds a slot.
func TestRunHook_SemaphoreDoesNotBlockConcurrentRequests(t *testing.T) {
	sb := newSandbox()

	var wg sync.WaitGroup
	slowDone := make(chan struct{})

	// Start a slow hook (infinite loop, will be interrupted by watchdog).
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(slowDone)
		sb.RunHook(context.Background(), `while(true){}`, nil, nil) //nolint:errcheck
	}()

	// Give the slow hook time to acquire its semaphore slot.
	time.Sleep(5 * time.Millisecond)

	// A fast hook must still complete concurrently.
	result, err := sb.RunHook(context.Background(),
		`result.proceed = true; result.data = data;`, nil, nil)
	if err != nil {
		t.Fatalf("concurrent fast hook failed: %v", err)
	}
	if !result.Proceed {
		t.Error("expected Proceed=true from fast hook")
	}

	wg.Wait() // slow hook was interrupted by watchdog
	<-slowDone
}
