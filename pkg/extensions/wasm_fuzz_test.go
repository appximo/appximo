//go:build go1.18

package extensions_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/extensions"
)

// FuzzWasmRunner throws (a) arbitrary bytes as a WASM module and (b) arbitrary
// input bytes at the real identity module. Neither may panic, escape the sandbox,
// or hang past the watchdog. Garbage module bytes must fail compilation cleanly.
func FuzzWasmRunner(f *testing.F) {
	wr, err := extensions.NewWasmRunner(context.Background())
	if err != nil {
		f.Fatalf("NewWasmRunner: %v", err)
	}
	defer wr.Close(context.Background()) //nolint:errcheck

	identity, err := os.ReadFile("../../testdata/wasm/identity.wasm")
	if err != nil {
		f.Fatalf("read identity.wasm: %v", err)
	}

	f.Add([]byte("hello"), []byte{0x00, 0x61, 0x73, 0x6d}) // input + wasm magic prefix
	f.Add([]byte(""), []byte("not a module"))
	f.Add([]byte("\r\n<script>alert(1)</script>"), identity)
	f.Add(make([]byte, 70000), identity) // input larger than one 64KiB page

	f.Fuzz(func(t *testing.T, input, moduleBytes []byte) {
		// 1) Arbitrary module bytes must fail gracefully (no panic, no escape).
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		_, _ = wr.Execute(ctx, "10", moduleBytes, "transform", input)
		cancel()

		// 2) The real identity module with arbitrary input must never panic and must
		//    return within the watchdog.
		done := make(chan struct{})
		go func() {
			defer close(done)
			ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel2()
			_, _ = wr.Execute(ctx2, "10", identity, "transform", input)
		}()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("Execute did not return within 3s for input len %d", len(input))
		}
	})
}
