package schemadiff_test

// Test helpers for applying plans. The MINIMAL test renderer that S2/S3 used has
// been REPLACED by the production renderer+executor (render.go): apply() now drives
// sd.Executor, so every convergence/property test exercises the real, production-
// safe SQL path (lock_timeout+retry, NOT VALID/VALIDATE, CONCURRENTLY partitioning,
// safe rename). q/qcols remain for tests that issue raw INSERT/SELECT.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	sd "github.com/miguelangel/appitools/pkg/schemadiff"
)

func q(name string) string { return `"` + name + `"` }

// apply orders, renders and executes plan against the ephemeral schema via the
// production Executor, failing the test on any error.
func apply(t *testing.T, pool *pgxpool.Pool, schema string, plan *sd.Plan) {
	t.Helper()
	ex := &sd.Executor{Pool: pool, Schema: schema}
	if err := ex.Apply(context.Background(), plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func introspectSchema(t *testing.T, pool *pgxpool.Pool, schema string) *sd.Schema {
	t.Helper()
	s, err := sd.Introspect(context.Background(), pool, schema)
	if err != nil {
		t.Fatalf("introspect %s: %v", schema, err)
	}
	return s
}
