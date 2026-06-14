// Package consumers holds real outbox consumers — the business-logic Processors
// the worker runs (ADR-016 §Class 2). They are kept OUT of pkg/worker so the core
// delivery loop stays dependency-light (e.g. excelize lives only here, not in the
// worker engine). A consumer reads an event, does its work, and writes results
// BACK through the engine HTTP API with a scoped service JWT (worker.EngineClient)
// — never the tenant DB directly.
package consumers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
	"github.com/xuri/excelize/v2"

	"github.com/miguelangel/appitools/pkg/worker"
)

// XLSXProcessor is the first real consumer (XLSX-CONSUMER-V1): the canonical
// FileJob pattern. On a "{resource}.created" event it fetches the job, STREAMS
// the referenced XLSX (excelize row iterator — never the whole file in RAM),
// computes an aggregate, and writes {status, result} back via the engine API
// using a scoped service JWT. A corrupt/invalid file is a PERMANENT failure
// (job → "failed", event acked); a transient engine error keeps the row pending
// for retry (at-least-once). Idempotent: a job already in a terminal state is
// skipped, so a redelivery never double-processes.
type XLSXProcessor struct {
	client      *worker.EngineClient
	resource    string     // schema resource holding the jobs, e.g. "filejobs"
	statusField string     // default "status"
	resultField string     // default "result"
	fileField   string     // default "file_ref"
	open        FileOpener // resolves file_ref → a readable stream
	log         zerolog.Logger
}

// FileOpener resolves a job's file_ref to a readable stream for the tenant. The
// default opens file_ref as a local path; the VFS-backed opener (pkg/files) treats
// file_ref as a VFS file_id and streams the content-addressed blob — wiring the
// consumer to the real file store (FILES-V1) without coupling pkg/consumers to
// pkg/files. The caller closes the returned reader.
type FileOpener func(ctx context.Context, tenant, fileRef string) (io.ReadCloser, error)

// localPathOpener is the default: file_ref is a path on the worker's filesystem.
func localPathOpener(_ context.Context, _ string, fileRef string) (io.ReadCloser, error) {
	return os.Open(fileRef) //nolint:gosec // fileRef is operator/engine-provided, not raw client input
}

// NewXLSXProcessor builds the consumer. resource defaults to "filejobs". The file
// source defaults to the local filesystem; use WithFileOpener to read from the VFS.
func NewXLSXProcessor(client *worker.EngineClient, resource string, log zerolog.Logger) *XLSXProcessor {
	if resource == "" {
		resource = "filejobs"
	}
	return &XLSXProcessor{
		client:      client,
		resource:    resource,
		statusField: "status",
		resultField: "result",
		fileField:   "file_ref",
		open:        localPathOpener,
		log:         log,
	}
}

// WithFileOpener swaps the file source (e.g. the VFS). A nil opener is ignored so
// the default local-path source remains. Returns p for chaining.
func (p *XLSXProcessor) WithFileOpener(o FileOpener) *XLSXProcessor {
	if o != nil {
		p.open = o
	}
	return p
}

// crudEvent is the lean payload the generated CRUD path emits (CRUD-EMIT-V1).
type crudEvent struct {
	ID       string `json:"id"`
	Resource string `json:"resource"`
}

// XLSXResult is the demo aggregate written back as the job result: the FileJob
// pattern from the rt-api tax case — count rows, sum a numeric column, and sum it
// grouped by a type column.
type XLSXResult struct {
	Rows   int                `json:"rows"`
	Total  float64            `json:"total"`
	ByType map[string]float64 `json:"by_type"`
}

// Process implements worker.Processor. Non-".created" topics and other resources
// are acked (logged) so this consumer never blocks the shared queue.
func (p *XLSXProcessor) Process(ctx context.Context, row worker.Row) error {
	if !strings.HasSuffix(row.Topic, ".created") {
		return nil // not a create event — ack and move on
	}
	var ev crudEvent
	if err := json.Unmarshal(row.Payload, &ev); err != nil {
		return fmt.Errorf("consumers: decode event id=%d: %w", row.ID, err)
	}
	if ev.Resource != p.resource || ev.ID == "" {
		return nil // not our resource — ack
	}

	// 1. Fetch the job (read scope) to get its file_ref + current status. A
	//    transient/non-200 here is retried (the job should exist).
	job, err := p.fetchJob(ctx, row.TenantID, ev.ID)
	if err != nil {
		return err
	}

	// 2. Idempotency: a job already in a terminal state was processed by a prior
	//    (redelivered) attempt — ack without re-doing the side-effect.
	if job.Status == "completed" || job.Status == "failed" {
		p.log.Debug().Int64("id", row.ID).Str("job", ev.ID).Str("status", job.Status).
			Msg("consumers: job already terminal, skipping (idempotent)")
		return nil
	}

	// 3. Process the file. A processing error is PERMANENT (a bad file will never
	//    succeed) → record "failed" and ack; only a write-back transport failure
	//    is retried.
	if job.FileRef == "" {
		return p.writeback(ctx, row.TenantID, ev.ID, "failed",
			mustJSON(map[string]string{"error": "job has no file_ref"}))
	}
	// Resolve the file_ref to a stream (local path or VFS blob). An open error is
	// PERMANENT too — an unresolvable file_ref won't resolve on a later retry —
	// so it records "failed" and acks, exactly like a corrupt file.
	rc, oerr := p.open(ctx, row.TenantID, job.FileRef)
	if oerr != nil {
		p.log.Warn().Int64("id", row.ID).Str("job", ev.ID).Err(oerr).
			Msg("consumers: opening file_ref failed → marking job failed")
		return p.writeback(ctx, row.TenantID, ev.ID, "failed",
			mustJSON(map[string]string{"error": oerr.Error()}))
	}
	result, perr := ProcessXLSXReader(rc)
	_ = rc.Close()
	if perr != nil {
		p.log.Warn().Int64("id", row.ID).Str("job", ev.ID).Err(perr).
			Msg("consumers: xlsx processing failed → marking job failed")
		return p.writeback(ctx, row.TenantID, ev.ID, "failed",
			mustJSON(map[string]string{"error": perr.Error()}))
	}
	return p.writeback(ctx, row.TenantID, ev.ID, "completed", mustJSON(result))
}

// jobView is the slice of the job record this consumer reads.
type jobView struct {
	Status  string
	FileRef string
}

func (p *XLSXProcessor) fetchJob(ctx context.Context, tenant, id string) (jobView, error) {
	status, body, err := p.client.Do(ctx, tenant, http.MethodGet, "/api/"+p.resource+"/"+id, nil)
	if err != nil {
		return jobView{}, err
	}
	if status != http.StatusOK {
		return jobView{}, fmt.Errorf("consumers: fetch job %s → %d: %s", id, status, string(body))
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return jobView{}, fmt.Errorf("consumers: decode job %s: %w", id, err)
	}
	jv := jobView{}
	if s, ok := raw[p.statusField].(string); ok {
		jv.Status = s
	}
	if f, ok := raw[p.fileField].(string); ok {
		jv.FileRef = f
	}
	return jv, nil
}

// writeback PATCHes {status, result} back through the engine API. A non-2xx (or
// transport error) returns an error so the outbox row is NOT marked sent.
func (p *XLSXProcessor) writeback(ctx context.Context, tenant, id, status, result string) error {
	st, body, err := p.client.Do(ctx, tenant, http.MethodPatch, "/api/"+p.resource+"/"+id,
		map[string]any{p.statusField: status, p.resultField: result})
	if err != nil {
		return err
	}
	if st < 200 || st >= 300 {
		return fmt.Errorf("consumers: writeback job %s → %d: %s", id, st, string(body))
	}
	p.log.Info().Str("job", id).Str("status", status).Int("http", st).
		Msg("consumers: xlsx result written back")
	return nil
}

// ProcessXLSX opens path and STREAMS its first sheet with the excelize row
// iterator (f.Rows — NOT f.GetRows, which materialises every row in RAM),
// computing the demo aggregate. It validates the header has the expected columns
// (tipo, monto) and returns a descriptive error on a corrupt/empty/missing-column
// file so the caller can record a "failed" job rather than crash.
func ProcessXLSX(path string) (XLSXResult, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return XLSXResult{}, fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close() //nolint:errcheck
	return aggregateSheet(f)
}

// ProcessXLSXReader is ProcessXLSX over a stream rather than a path — the form the
// VFS uses (VFS.Get returns a reader). excelize.OpenReader buffers the zip archive
// (an .xlsx is a zip, so random access is inherent), but the per-row aggregation
// still STREAMS via the f.Rows iterator, never GetRows.
func ProcessXLSXReader(r io.Reader) (XLSXResult, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return XLSXResult{}, fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close() //nolint:errcheck
	return aggregateSheet(f)
}

// aggregateSheet computes the demo aggregate over the first sheet of f using the
// streaming row iterator. Shared by the path and reader entry points.
func aggregateSheet(f *excelize.File) (XLSXResult, error) {
	var res XLSXResult
	sheet := f.GetSheetName(0)
	if sheet == "" {
		return res, errors.New("xlsx has no sheets")
	}
	rows, err := f.Rows(sheet) // STREAMING iterator — bounded memory
	if err != nil {
		return res, fmt.Errorf("open rows: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	res.ByType = map[string]float64{}
	idxTipo, idxMonto := -1, -1
	header := false
	for rows.Next() {
		cols, err := rows.Columns()
		if err != nil {
			return res, fmt.Errorf("read row: %w", err)
		}
		if !header {
			// Locate columns by header name (order-independent).
			for i, c := range cols {
				switch strings.ToLower(strings.TrimSpace(c)) {
				case "tipo":
					idxTipo = i
				case "monto":
					idxMonto = i
				}
			}
			if idxTipo == -1 || idxMonto == -1 {
				return res, errors.New("missing required column(s): expected 'tipo' and 'monto'")
			}
			header = true
			continue
		}
		// Skip a fully blank row (excelize yields trailing/empty rows).
		if idxTipo >= len(cols) && idxMonto >= len(cols) {
			continue
		}
		tipo := ""
		if idxTipo < len(cols) {
			tipo = strings.TrimSpace(cols[idxTipo])
		}
		montoStr := ""
		if idxMonto < len(cols) {
			montoStr = strings.TrimSpace(cols[idxMonto])
		}
		if tipo == "" && montoStr == "" {
			continue
		}
		monto, perr := strconv.ParseFloat(montoStr, 64)
		if perr != nil {
			return res, fmt.Errorf("row %d: 'monto' value %q is not numeric", res.Rows+2, montoStr)
		}
		res.Rows++
		res.Total += monto
		res.ByType[tipo] += monto
	}
	if err := rows.Error(); err != nil {
		return res, fmt.Errorf("iterate rows: %w", err)
	}
	if !header {
		return res, errors.New("xlsx is empty (no header row)")
	}
	if res.Rows == 0 {
		return res, errors.New("xlsx has a header but no data rows")
	}
	return res, nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"marshal failed"}`
	}
	return string(b)
}
