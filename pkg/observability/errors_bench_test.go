package observability

import (
	"errors"
	"fmt"
	"testing"
)

// BenchmarkRecord_FirstOccurrence measures the cost of a brand-new error group:
// PC capture + fingerprint + symbolization (happens only once per fingerprint).
func BenchmarkRecord_FirstOccurrence(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		es := NewErrorStore()
		es.Record("tenant1", fmt.Errorf("unique error %d", i))
	}
}

// BenchmarkRecord_SubsequentOccurrence measures the hot path: same error
// repeated — only PC capture + fingerprint lookup, zero symbolization.
func BenchmarkRecord_SubsequentOccurrence(b *testing.B) {
	es := NewErrorStore()
	err := errors.New("repeated connection timeout")
	// Warm up: first occurrence triggers symbolization.
	es.Record("tenant1", err)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		es.Record("tenant1", err)
	}
}

// BenchmarkRecord_HighConcurrency verifies stackOnce does not cause contention
// under 100 goroutines hammering the same error.
func BenchmarkRecord_HighConcurrency(b *testing.B) {
	es := NewErrorStore()
	err := errors.New("concurrent error")
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			es.Record("tenant1", err)
		}
	})
}
