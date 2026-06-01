package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// GuideRow is the typed struct equivalent of what struct_generator would emit
// for the "guides" resource in the logistics schema.
type GuideRow struct {
	ID            string     `json:"id"`
	ClientId      *string    `json:"client_id,omitempty"`
	Code          string     `json:"code"`
	CreatedAt     *time.Time `json:"created_at,omitempty"`
	DeclaredValue *float64   `json:"declared_value,omitempty"`
	Destination   string     `json:"destination"`
	OperatorId    *string    `json:"operator_id,omitempty"`
	Origin        string     `json:"origin"`
	Status        *string    `json:"status,omitempty"`
	UpdatedAt     *time.Time `json:"updated_at,omitempty"`
	WeightKg      *float64   `json:"weight_kg,omitempty"`
}

// discardWriter is an http.ResponseWriter that discards all output.
// Benchmarks use a single instance to avoid per-iteration allocation.
type discardWriter struct{ h http.Header }

func (d *discardWriter) Header() http.Header        { return d.h }
func (d *discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (d *discardWriter) WriteHeader(int)             {}

// sampleRows returns n representative guide rows as []map[string]any (current format).
func sampleMaps(n int) []map[string]any {
	now := time.Now()
	wkg := 2.5
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{
			"id":          "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
			"code":        "GU-BOGOTA-001",
			"status":      "in_transit",
			"origin":      "Bogotá",
			"destination": "Medellín",
			"weight_kg":   wkg,
			"created_at":  now,
			"updated_at":  now,
		}
	}
	return rows
}

// sampleStructs returns n representative guide rows as []GuideRow (typed format).
func sampleStructs(n int) []GuideRow {
	now := time.Now()
	wkg := 2.5
	status := "in_transit"
	rows := make([]GuideRow, n)
	for i := range rows {
		rows[i] = GuideRow{
			ID:          "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
			Code:        "GU-BOGOTA-001",
			Status:      &status,
			Origin:      "Bogotá",
			Destination: "Medellín",
			WeightKg:    &wkg,
			CreatedAt:   &now,
			UpdatedAt:   &now,
		}
	}
	return rows
}

// --- Baselines: plain json.Marshal ---

func BenchmarkJSONMarshalMap10(b *testing.B) {
	data := sampleMaps(10)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		json.Marshal(data) //nolint:errcheck
	}
}

func BenchmarkJSONMarshalStruct10(b *testing.B) {
	data := sampleStructs(10)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		json.Marshal(data) //nolint:errcheck
	}
}

func BenchmarkJSONMarshalMap100(b *testing.B) {
	data := sampleMaps(100)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		json.Marshal(data) //nolint:errcheck
	}
}

func BenchmarkJSONMarshalStruct100(b *testing.B) {
	data := sampleStructs(100)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		json.Marshal(data) //nolint:errcheck
	}
}

// --- Pool-based WriteJSON (production path) ---

func BenchmarkWriteJSONMap10(b *testing.B) {
	data := sampleMaps(10)
	w := &discardWriter{h: http.Header{}}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		WriteJSON(w, data) //nolint:errcheck
	}
}

func BenchmarkWriteJSONStruct10(b *testing.B) {
	data := sampleStructs(10)
	w := &discardWriter{h: http.Header{}}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		WriteJSON(w, data) //nolint:errcheck
	}
}

func BenchmarkWriteJSONMap100(b *testing.B) {
	data := sampleMaps(100)
	w := &discardWriter{h: http.Header{}}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		WriteJSON(w, data) //nolint:errcheck
	}
}

func BenchmarkWriteJSONStruct100(b *testing.B) {
	data := sampleStructs(100)
	w := &discardWriter{h: http.Header{}}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		WriteJSON(w, data) //nolint:errcheck
	}
}
