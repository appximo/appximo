package appitools

// Graceful self-restart (UI-F4-S2): the mechanism that turns the editor's
// "needs an engine restart" banner into one click. The deploy persists the new
// schema as the BOOT schema (validated first, written atomically, previous one
// backed up), then the process drains through the EXISTING graceful-shutdown
// path (readyz→503 → drain sleep → http.Server.Shutdown → cleanup) and
// RE-EXECS itself — same binary, same argv, so the relaunch reads the
// persisted schema and rebuilds every boot-derived artifact (routes, GraphQL,
// RBAC, validators, hooks, OpenAPI) consistently. Re-exec is supervisor-
// agnostic: it works for a loose dev process, systemd and Docker alike (the
// process image is replaced in place; Go sockets are CLOEXEC so the listeners
// are released at exec).
//
// Safety ladder (the service must never end up down):
//  1. The schema is validated (LoadFromBytes + Validate — the SAME checks boot
//     runs) BEFORE anything is written. Invalid ⇒ nothing persisted, no restart.
//  2. The write is atomic (temp file + rename in the schema's directory).
//  3. The previous boot schema is kept at <schema>.bak, and a <schema>.selfrestart
//     marker brackets the operation: if the relaunch cannot load the schema
//     (a non-schema corruption — validation already passed), New() restores the
//     backup and boots from it instead of dying (see recoverBootSchema).

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/miguelangel/appitools/pkg/platformadmin"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
)

func bootBackupPath(schemaPath string) string { return schemaPath + ".bak" }
func bootMarkerPath(schemaPath string) string { return schemaPath + ".selfrestart" }

// loadAndValidateSchema is the one boot-schema gate: structural load (strict
// keys) + full semantic validation, with the aggregated error New has always
// reported. persistBootSchemaFile runs the SAME gate before persisting, so a
// schema that persists is a schema that boots.
func loadAndValidateSchema(path string) (*schema.APISchema, error) {
	s, err := schema.LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("appitools: read schema: %w", err)
	}
	if errs := schema.Validate(s); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return nil, fmt.Errorf("appitools: invalid schema:\n  %s", strings.Join(msgs, "\n  "))
	}
	return s, nil
}

// persistBootSchemaFile validates raw as a complete engine schema and atomically
// replaces the boot schema file at path with it, keeping the previous content at
// <path>.bak and writing the <path>.selfrestart marker for the boot-failure
// rollback. A rejected schema is reported wrapped in
// platformadmin.ErrSchemaRejected (→ 422; nothing written, no restart).
func persistBootSchemaFile(path string, raw []byte) error {
	s, err := schema.LoadFromBytes(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", platformadmin.ErrSchemaRejected, err)
	}
	if errs := schema.Validate(s); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return fmt.Errorf("%w:\n  %s", platformadmin.ErrSchemaRejected, strings.Join(msgs, "\n  "))
	}

	cur, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read current boot schema: %w", err)
	}
	if err := writeFileAtomic(bootBackupPath(path), cur); err != nil {
		return fmt.Errorf("write boot schema backup: %w", err)
	}
	if err := os.WriteFile(bootMarkerPath(path), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write self-restart marker: %w", err)
	}
	if err := writeFileAtomic(path, raw); err != nil {
		return fmt.Errorf("persist boot schema: %w", err)
	}
	return nil
}

// writeFileAtomic writes data to path via a temp file + rename in the same
// directory (atomic on POSIX filesystems) — a crash mid-write can never leave a
// half-written boot schema.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close() //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// recoverBootSchema is the boot-failure rollback: when the schema fails to load
// AND this boot follows a self-restart persist (the marker exists) AND a backup
// exists, restore the backup over the schema file and report true so New retries
// — the service comes back on the PREVIOUS structure instead of staying down.
// Without the marker (a hand-edited schema, no self-restart involved) it does
// nothing: failing loud is correct there.
func recoverBootSchema(path string, loadErr error) bool {
	if _, err := os.Stat(bootMarkerPath(path)); err != nil {
		return false
	}
	bak, err := os.ReadFile(bootBackupPath(path))
	if err != nil {
		return false
	}
	if err := writeFileAtomic(path, bak); err != nil {
		log.Printf("CRITICAL: self-restart rollback: boot schema is unloadable (%v) and the backup could not be restored: %v", loadErr, err)
		return false
	}
	os.Remove(bootMarkerPath(path)) //nolint:errcheck
	log.Printf("CRITICAL: self-restart rollback: boot schema at %s failed to load (%v) — restored the previous schema from %s and booting from it", path, loadErr, bootBackupPath(path))
	return true
}

// clearBootMarker removes the self-restart marker after a successful boot (the
// persisted schema loaded fine — the rollback window is closed).
func clearBootMarker(path string) { os.Remove(bootMarkerPath(path)) } //nolint:errcheck

// persistBootSchema is the App-level persist (closure the admin API calls).
//
// It adds the one gate persistBootSchemaFile cannot do on its own: the incoming
// schema's custom-route grants are cross-checked against THIS binary's registered
// routes (LIBRARY-GAPS-S1). Without it, deploying a schema that grants an
// unregistered route segment would persist, restart, fail to boot, and come back
// via the .bak rollback — safe, but a terrible way to learn about a typo. Here it
// is a clean 422 with nothing written.
func (a *App) persistBootSchema(raw json.RawMessage) error {
	if s, err := schema.LoadFromBytes(raw); err == nil {
		if gerr := validateRouteGrants(s, a.routes); gerr != nil {
			return fmt.Errorf("%w: %v", platformadmin.ErrSchemaRejected, gerr)
		}
	}
	return persistBootSchemaFile(a.cfg.SchemaPath, raw)
}

// triggerRestart is the deploy-activation dispatcher (called by the admin
// POST /admin/engine/schema after the new schema is persisted). In the
// in-process fleet (MT-STRUCT-S4) it hot-swaps ONLY this app — no process
// restart, the other apps untouched. In single-engine mode (hotSwap nil) it
// falls back to the graceful whole-process re-exec (UI-F4-S2).
func (a *App) triggerRestart() {
	if a.hotSwap != nil {
		a.hotSwap()
		return
	}
	a.requestRestart()
}

// rebuildRouter recompiles this app's data-plane router from the schema now on
// disk (a.cfg.SchemaPath — the deploy persisted the new schema there first),
// reusing ALL process infra (pool — the DSN never changes on a schema deploy —
// control plane, obs, caches, SSE hub, files, auth). It returns the new handler
// for the registry swap; the OLD router keeps serving in-flight requests until
// they finish and is then GC'd. A schema that fails to load leaves the current
// router in place (the caller keeps serving the old surface, logs loudly).
func (a *App) rebuildRouter() (http.Handler, error) {
	s, err := loadAndValidateSchema(a.cfg.SchemaPath)
	if err != nil {
		return nil, err
	}
	policyBytes, err := json.Marshal(s.RBAC)
	if err != nil {
		return nil, fmt.Errorf("serialize RBAC policy: %w", err)
	}
	var pol rbac.Policy
	if err := json.Unmarshal(policyBytes, &pol); err != nil {
		return nil, fmt.Errorf("parse RBAC policy: %w", err)
	}
	return a.buildRouter(builtSurface{schema: s, policy: &pol}), nil
}

// requestRestart initiates the graceful self-restart: it flags the intent and,
// after letting the HTTP response flush, signals SIGTERM to the OWN process —
// deliberately reusing the battle-tested shutdown path (signal.NotifyContext in
// Start → shutdown.State.Run: readyz→503, drain sleep, Shutdown(10s), cleanup)
// instead of a second, parallel drain implementation. Start re-execs after the
// clean shutdown when this flag is set. Idempotent: a second call while a
// restart is in flight is a no-op.
func (a *App) requestRestart() {
	if !a.restartRequested.CompareAndSwap(false, true) {
		return
	}
	log.Println("self-restart: requested — draining (readyz→503) then re-exec'ing with the persisted boot schema")
	go func() {
		time.Sleep(400 * time.Millisecond) // let the 200 response reach the caller
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			log.Printf("self-restart: could not signal self: %v", err)
			a.restartRequested.Store(false)
		}
	}()
}

// execRestart replaces the process image with a fresh instance of the same
// binary and argv — the relaunch loads the persisted boot schema and rebuilds
// every boot-derived artifact. Called only after the graceful shutdown finished
// (server drained, pool closed); Go sets CLOEXEC on sockets, so any listener
// still open (control plane, pprof) is released by the exec itself. On success
// it never returns. On failure it logs and returns — the process then exits
// normally and a supervisor (systemd/Docker restart policy) is the fallback.
func (a *App) execRestart() { execRestartProcess("boot schema " + a.cfg.SchemaPath) }

// execRestartProcess replaces the process image with a fresh instance of the
// same binary + argv + env (same PID — supervisor-agnostic). Shared by the
// single-app self-restart and the multi-app runtime (ServeFleet), where a
// deploy-triggered restart relaunches the WHOLE fleet with the persisted
// schema files (per-app hot-swap is MT-STRUCT-S4). On success it never returns.
func execRestartProcess(note string) {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("CRITICAL: self-restart: cannot resolve own executable (%v) — exiting; a supervisor restart policy must relaunch the engine", err)
		return
	}
	log.Printf("self-restart: re-exec %s (same argv — %s)", exe, note)
	if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
		log.Printf("CRITICAL: self-restart: exec failed (%v) — exiting; a supervisor restart policy must relaunch the engine", err)
	}
}
