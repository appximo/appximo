//go:build integration

// XLSX-CONSUMER-V1 integration tests: the full async FileJob chain — POST a
// filejob (file_ref → a local XLSX) emits filejobs.created → the worker fetches
// the job, streams + processes the XLSX, and writes {status, result} back through
// the engine API with a scoped service JWT. Reuses the shared Postgres container
// + control plane from TestMain in library_integration_test.go.
package appitools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/xuri/excelize/v2"

	"github.com/miguelangel/appitools/pkg/consumers"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/worker"
	"github.com/miguelangel/appitools/tests/helpers"
)

func fileJobFixturePath() string {
	return filepath.Join(helpers.RepoRoot(), "tests", "fixtures", "schemas", "filejob.json")
}

func loadFileJobSchema(t *testing.T) *schema.APISchema {
	t.Helper()
	s, err := schema.LoadFromFile(fileJobFixturePath())
	if err != nil {
		t.Fatalf("load filejob fixture: %v", err)
	}
	return s
}

func newFileJobApp(t *testing.T) *httptest.Server {
	t.Helper()
	app, err := New(Config{
		SchemaPath: fileJobFixturePath(),
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New(filejob): %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

// writeXLSXFile writes a small valid sheet to a temp file and returns its path.
func writeXLSXFile(t *testing.T, rows [][]any) string {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck
	sheet := f.GetSheetName(0)
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			t.Fatalf("set row: %v", err)
		}
	}
	path := filepath.Join(t.TempDir(), "job.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save xlsx: %v", err)
	}
	return path
}

// createFileJob POSTs a pending filejob (as admin) and returns its id.
func createFileJob(t *testing.T, srv *httptest.Server, tenant, fileRef string) string {
	t.Helper()
	adminTok := helpers.GenToken(t, "admin", "admin-user", tenant)
	body, _ := json.Marshal(map[string]any{"status": "pending", "file_ref": fileRef})
	resp := do(t, srv, "POST", "/api/filejobs", tenant+".localhost", adminTok, string(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create filejob: want 201, got %d", resp.StatusCode)
	}
	id, _ := decode(t, resp)["id"].(string)
	if id == "" {
		t.Fatal("create filejob: no id")
	}
	return id
}

func getFileJob(t *testing.T, srv *httptest.Server, tenant, id string) map[string]any {
	t.Helper()
	adminTok := helpers.GenToken(t, "admin", "admin-user", tenant)
	resp := do(t, srv, "GET", "/api/filejobs/"+id, tenant+".localhost", adminTok, "")
	return decode(t, resp)
}

func newXLSXProc(srv *httptest.Server) *consumers.XLSXProcessor {
	client := worker.NewEngineClient(srv.URL, "localhost", helpers.JWTSecret, "service_worker", worker.DefaultServiceTokenTTL)
	return consumers.NewXLSXProcessor(client, "filejobs", zerolog.Nop())
}

func TestXLSXConsumer_E2E(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "xlsxe2e", loadFileJobSchema(t))
	srv := newFileJobApp(t)

	xlsx := writeXLSXFile(t, [][]any{
		{"tipo", "monto"},
		{"ingreso", 100},
		{"ingreso", 50},
		{"egreso", 25},
	})
	id := createFileJob(t, srv, "xlsxe2e", xlsx)

	// Drain the filejobs.created event through the real XLSX consumer.
	nop := zerolog.Nop()
	res, err := worker.Drain(context.Background(), itPool, newXLSXProc(srv), 50, 5, &nop)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if res.Processed < 1 {
		t.Fatalf("processed %d, want >=1", res.Processed)
	}

	// The job is now completed with the computed aggregate written back via API.
	job := getFileJob(t, srv, "xlsxe2e", id)
	if job["status"] != "completed" {
		t.Fatalf("status = %v, want completed", job["status"])
	}
	resultStr, _ := job["result"].(string)
	var result consumers.XLSXResult
	if err := json.Unmarshal([]byte(resultStr), &result); err != nil {
		t.Fatalf("decode result %q: %v", resultStr, err)
	}
	if result.Rows != 3 || result.Total != 175 || result.ByType["ingreso"] != 150 || result.ByType["egreso"] != 25 {
		t.Fatalf("unexpected result: %+v", result)
	}

	// The outbox row is marked sent.
	var sentAt *string
	if err := itPool.QueryRow(context.Background(),
		"SELECT sent_at::text FROM public.outbox WHERE tenant_id='xlsxe2e' AND topic='filejobs.created' AND payload->>'id'=$1",
		id).Scan(&sentAt); err != nil {
		t.Fatalf("outbox query: %v", err)
	}
	if sentAt == nil {
		t.Fatal("event not marked sent after successful processing")
	}
}

func TestXLSXConsumer_CorruptFileFailed(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "xlsxbad", loadFileJobSchema(t))
	srv := newFileJobApp(t)

	bad := filepath.Join(t.TempDir(), "corrupt.xlsx")
	if err := os.WriteFile(bad, []byte("not an xlsx"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := createFileJob(t, srv, "xlsxbad", bad)

	nop := zerolog.Nop()
	res, err := worker.Drain(context.Background(), itPool, newXLSXProc(srv), 50, 5, &nop)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	// A corrupt file is a PERMANENT failure: the job is recorded failed and the
	// event is acked (Processed, not retried forever). Worker did not crash.
	if res.Processed < 1 {
		t.Fatalf("processed %d, want >=1 (failure recorded + acked)", res.Processed)
	}
	job := getFileJob(t, srv, "xlsxbad", id)
	if job["status"] != "failed" {
		t.Fatalf("status = %v, want failed", job["status"])
	}
	if rs, _ := job["result"].(string); !strings.Contains(rs, "error") {
		t.Fatalf("failed result should carry an error message, got %q", rs)
	}
}

func TestXLSXConsumer_Idempotent(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "xlsxidem", loadFileJobSchema(t))
	srv := newFileJobApp(t)
	xlsx := writeXLSXFile(t, [][]any{{"tipo", "monto"}, {"a", 10}})
	id := createFileJob(t, srv, "xlsxidem", xlsx)

	proc := newXLSXProc(srv)
	row := worker.Row{
		TenantID: "xlsxidem",
		Topic:    "filejobs.created",
		Payload:  json.RawMessage(`{"id":"` + id + `","resource":"filejobs","action":"created"}`),
	}
	// First delivery → processes → completed.
	if err := proc.Process(context.Background(), row); err != nil {
		t.Fatalf("first Process: %v", err)
	}
	job1 := getFileJob(t, srv, "xlsxidem", id)
	if job1["status"] != "completed" {
		t.Fatalf("after first: status = %v, want completed", job1["status"])
	}
	// Redelivery (at-least-once) → job already terminal → no-op, no error, result
	// unchanged.
	if err := proc.Process(context.Background(), row); err != nil {
		t.Fatalf("second Process (redelivery): %v", err)
	}
	job2 := getFileJob(t, srv, "xlsxidem", id)
	if job2["status"] != "completed" || job2["result"] != job1["result"] {
		t.Fatalf("redelivery changed the job: before=%v after=%v", job1["result"], job2["result"])
	}
}
