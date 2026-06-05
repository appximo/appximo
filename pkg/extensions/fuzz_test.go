//go:build go1.18

package extensions

import (
	"context"
	"testing"
	"time"
)

// FuzzJSSandbox throws arbitrary scripts (including sandbox-escape and
// resource-exhaustion attempts) at the Goja hook sandbox. RunHook must never
// panic and must always return — the 80 ms watchdog has to interrupt runaway
// scripts. Escape attempts (require/fetch/os/process/constructor tricks) must not
// crash the host; they should simply error inside the VM.
func FuzzJSSandbox(f *testing.F) {
	seeds := []string{
		`result.proceed = true`,
		`while(true){}`,
		`throw new Error("boom")`,
		`require("os")`,
		`fetch("http://169.254.169.254/")`,
		`process.exit(1)`,
		`new Function("return this")()`,
		`globalThis.constructor.constructor("return process")()`,
		`data.x = now() + formatMoney(1.5)`,
		`for(var i=0;i<1e9;i++){}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	sb := NewJSSandbox()

	f.Fuzz(func(t *testing.T, script string) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = sb.RunHook(ctx, script, map[string]any{"a": 1}, map[string]any{"role": "x"})
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("RunHook did not return within 2s (watchdog failed) for script %.60q", script)
		}
	})
}
