package flowtest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPG(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"), tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)))
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		ctr.Terminate(ctx) //nolint:errcheck
		t.Fatalf("connection string: %v", err)
	}
	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		ctr.Terminate(ctx) //nolint:errcheck
		t.Fatalf("new pool: %v", err)
	}
	return pool, func() { pool.Close(); ctr.Terminate(ctx) } //nolint:errcheck
}

// The persistence contract: flows CRUD (unique names, cascade with the tenant)
// and the regression trail (runs anchored to a schema version, survivors of a
// flow's deletion).
func TestIntegration_FlowStoreAndRuns(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE public.tenants (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("tenants table: %v", err)
	}
	if err := EnsureTables(ctx, pool); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO public.tenants (id) VALUES ('opt')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	flow := &Flow{Name: "optica", Steps: []Step{
		{Name: "login", Method: "POST", Path: "/auth/login", Body: `{}`, Expect: Expect{Status: 200}},
	}}
	saved, err := SaveFlow(ctx, pool, "opt", "", flow)
	if err != nil || saved.ID == "" {
		t.Fatalf("save: %+v err=%v", saved, err)
	}
	if _, err := SaveFlow(ctx, pool, "opt", "", flow); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate name: err=%v, want ErrDuplicateName", err)
	}

	flow.Steps = append(flow.Steps, Step{Name: "crear", Method: "POST", Path: "/api/citas", Expect: Expect{Status: 201}})
	if _, err := SaveFlow(ctx, pool, "opt", saved.ID, flow); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := GetFlow(ctx, pool, "opt", saved.ID)
	if err != nil || len(got.Flow.Steps) != 2 {
		t.Fatalf("get after update: %+v err=%v", got, err)
	}
	list, err := ListFlows(ctx, pool, "opt")
	if err != nil || len(list) != 1 || list[0].Steps != 2 {
		t.Fatalf("list: %+v err=%v", list, err)
	}

	// A run is anchored to a schema version and outlives its flow.
	results, _ := json.Marshal([]FlowResult{{Name: "optica", Pass: false, StepsTotal: 2, StepsFail: 1}})
	run := &Run{TenantID: "opt", SchemaVersion: 5, Scope: "suite", Pass: false,
		FlowsTotal: 1, FlowsFailed: 1, StepsTotal: 2, StepsFailed: 1, Results: results}
	if err := SaveRun(ctx, pool, run); err != nil || run.ID == 0 {
		t.Fatalf("save run: %+v err=%v", run, err)
	}
	if err := DeleteFlow(ctx, pool, "opt", saved.ID); err != nil {
		t.Fatalf("delete flow: %v", err)
	}
	runs, err := ListRuns(ctx, pool, "opt", 10)
	if err != nil || len(runs) != 1 || runs[0].SchemaVersion != 5 || runs[0].Pass {
		t.Fatalf("runs after flow delete (trail must survive): %+v err=%v", runs, err)
	}
	full, err := GetRun(ctx, pool, "opt", run.ID)
	if err != nil || !json.Valid(full.Results) {
		t.Fatalf("get run: %+v err=%v", full, err)
	}
	if _, err := GetFlow(ctx, pool, "opt", saved.ID); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("deleted flow: err=%v, want ErrFlowNotFound", err)
	}
}
