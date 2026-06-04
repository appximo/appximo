package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// contextKey is the private key type for trace values stored in a context.
type contextKey string

// TraceIDKey is the context key under which the per-request trace id is stored.
const TraceIDKey = contextKey("trace_id")

// NewTraceID returns a 16-hex-character trace id (8 random bytes, no hyphens),
// using crypto/rand and the stdlib only — no google/uuid. crypto/rand.Read in
// modern Go is backed by a fast userspace CSPRNG, so this stays cheap.
func NewTraceID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) // modern crypto/rand.Read never fails
	return hex.EncodeToString(b[:])
}

// WithTraceID returns a context carrying the given trace id.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, TraceIDKey, id)
}

// TraceIDFromCtx returns the trace id from ctx, or "" if none is set.
func TraceIDFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(TraceIDKey).(string); ok {
		return v
	}
	return ""
}

// traceIDBytes decodes a 16-hex trace id into its 8 raw bytes (for compact
// storage in a Sample). A malformed/short id yields the zero array.
func traceIDBytes(id string) [8]byte {
	var out [8]byte
	if len(id) >= 16 {
		if dec, err := hex.DecodeString(id[:16]); err == nil {
			copy(out[:], dec)
		}
	}
	return out
}

// traceIDString re-encodes the 8 raw bytes back into the 16-hex trace id.
func traceIDString(b [8]byte) string {
	return hex.EncodeToString(b[:])
}
