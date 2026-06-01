package tenant

import "context"

// TenantCtx holds the resolved tenant identity for a request.
type TenantCtx struct {
	ID       string // e.g. "acme"
	PGSchema string // "tenant_" + ID
}

type contextKey struct{}

// WithContext returns a new context carrying tc.
// Used by TenantMiddleware and in tests.
func WithContext(ctx context.Context, tc *TenantCtx) context.Context {
	return context.WithValue(ctx, contextKey{}, tc)
}

// FromCtx returns the TenantCtx from ctx, or nil if none was set.
func FromCtx(ctx context.Context) *TenantCtx {
	tc, _ := ctx.Value(contextKey{}).(*TenantCtx)
	return tc
}

// MustFromCtx returns the TenantCtx from ctx, panicking if none was set.
// Use in handlers that are guaranteed to run after TenantMiddleware.
func MustFromCtx(ctx context.Context) *TenantCtx {
	tc := FromCtx(ctx)
	if tc == nil {
		panic("appitools: no TenantCtx in context — ensure TenantMiddleware is in the middleware chain")
	}
	return tc
}
