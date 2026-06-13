package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/miguelangel/appitools/tools/devhub/sshx"
)

// Deploy panel (S47): the PRIMER deploy protocol as an SSE pipeline.
//
//	build (local, never on the target) → push binary.new → mv (never cp:
//	ETXTBSY) → SIGTERM old + wait → start script → smoke /health + /readyz
//	through the SSH tunnel → record in SQLite.
//
// Deploying to a server marked is_production requires the request to repeat
// the server's exact name in "confirm" (GitHub delete-repo style).

// goBin resolves the go toolchain (the devhub process PATH may not have it).
func goBin() string {
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	for _, p := range []string{"/usr/local/go/bin/go", "/root/go/bin/go"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "go"
}

// deployEmit writes one pipeline progress line as an SSE step event.
func deployEmit(sw *sseWriter, step, line string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	payload, _ := json.Marshal(map[string]string{"step": step, "line": line})
	fmt.Fprintf(sw.w, "event: step\ndata: %s\n\n", payload)
	sw.flusher.Flush()
}

// DeployHandler — POST /api/deploy  body: {"server_id":2,"confirm":"58-prod"}
func DeployHandler(repoDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ServerID int64  `json:"server_id"`
			Confirm  string `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.ServerID == 0 {
			writeErr(w, http.StatusBadRequest, "server_id is required")
			return
		}
		srv, err := LoadServer(req.ServerID)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		if srv.IsProduction && req.Confirm != srv.Name {
			writeErr(w, http.StatusPreconditionFailed,
				fmt.Sprintf("%q is marked production: repeat its exact name in the \"confirm\" field to deploy", srv.Name))
			return
		}

		// PRIMER: never deploy a dirty working tree. -uno: untracked scratch
		// files (PROMPT_*.md etc.) don't ship in the binary; tracked changes do.
		sha, dirty, err := gitState(repoDir)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "git state: "+err.Error())
			return
		}
		if dirty != "" {
			writeErr(w, http.StatusConflict, "working tree has uncommitted tracked changes — commit or stash before deploying:\n"+dirty)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		sw := &sseWriter{w: w, flusher: flusher}

		deployID := recordDeployStart(srv.ID, sha)
		status := "failed"
		defer func() { recordDeployEnd(deployID, status) }()

		fail := func(step, msg string) {
			deployEmit(sw, step, "✗ "+msg)
			// Surface the remote engine log so a failed start/smoke is debuggable
			// from the panel. Fixed command; log_path was validated at registration.
			if step == "start" || step == "smoke" {
				if res, err := sshx.Run(&srv.Server, "tail -50 "+srv.LogPath, 15*time.Second); err == nil {
					deployEmit(sw, step, "── tail -50 "+srv.LogPath+" ──")
					for _, l := range strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n") {
						deployEmit(sw, step, l)
					}
				}
			}
			fmt.Fprintf(sw.w, "event: done\ndata: {\"status\":\"failed\",\"step\":%q}\n\n", step)
			sw.flusher.Flush()
		}

		// 1 — HEAD already resolved; tree clean.
		deployEmit(sw, "git", "HEAD "+sha+" — working tree clean")

		// Deployer-staleness guard — the trap that bit us once: a fix to this
		// very pipeline was committed, but the RUNNING devhub predated it and
		// silently kept building with the old flags. If this binary is older
		// than the last commit touching tools/devhub, warn loudly.
		if exe, eerr := os.Executable(); eerr == nil {
			if fi, serr := os.Stat(exe); serr == nil {
				out, gerr := exec.CommandContext(r.Context(),
					"git", "-C", repoDir, "log", "-1", "--format=%ct", "--", "tools/devhub").Output()
				if gerr == nil {
					if ts, perr := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); perr == nil &&
						fi.ModTime().Unix() < ts {
						deployEmit(sw, "git",
							"⚠ deployer binary is OLDER than the last commit to tools/devhub — "+
								"rebuild + restart devhub (make devhub-build && systemctl restart devhub) "+
								"or this pipeline may run stale logic")
					}
				}
			}
		}

		// 2 — build locally (never on the target box) via the CANONICAL build
		// script (scripts/build-engine.sh — single source of truth shared with
		// the Dockerfile and release.yml: CGO_ENABLED=0 static + version stamp).
		// version = short SHA of the deployed HEAD, so /health on the target
		// reports exactly what is running.
		shortSHA := sha
		if len(shortSHA) > 7 {
			shortSHA = shortSHA[:7]
		}
		// Guard: smoke asserts version==SHA, so a binary built without the
		// version ldflags always fails the smoke. Catch the missing script
		// here with a clear message rather than a cryptic sh error later.
		buildScript := filepath.Join(repoDir, "scripts", "build-engine.sh")
		if fi, serr := os.Stat(buildScript); serr != nil || fi.Mode()&0o111 == 0 {
			fail("build", "scripts/build-engine.sh not found or not executable — canonical build required (smoke asserts version==SHA; direct go build embeds no version stamp)")
			return
		}
		deployEmit(sw, "build", "scripts/build-engine.sh /tmp/appitools-deploy "+shortSHA+" "+sha)
		build := exec.CommandContext(r.Context(), "sh", "scripts/build-engine.sh",
			"/tmp/appitools-deploy", shortSHA, sha)
		build.Dir = repoDir
		build.Env = append(os.Environ(), "GO="+goBin())
		if out, err := build.CombinedOutput(); err != nil {
			fail("build", strings.TrimSpace(string(out)))
			return
		}
		fi, _ := os.Stat("/tmp/appitools-deploy")
		deployEmit(sw, "build", fmt.Sprintf("✓ binario %0.1f MB", float64(fi.Size())/1024/1024))

		// 3 — push as <binary>.new (mv later: cp over a running binary = ETXTBSY).
		newPath := srv.BinaryPath + ".new"
		deployEmit(sw, "push", "push → "+srv.Name+":"+newPath)
		if err := sshx.Push(&srv.Server, "/tmp/appitools-deploy", newPath); err != nil {
			fail("push", err.Error())
			return
		}
		deployEmit(sw, "push", "✓ binario en el server")

		// 4 — swap + SIGTERM the old process and WAIT for it to exit (≤30s).
		// pgrep -x (never -f: -f self-matches the SSH shell — PRIMER gotcha).
		base := filepath.Base(srv.BinaryPath)
		dir := filepath.Dir(srv.BinaryPath)

		// BACKUP the live binary BEFORE the swap. Overwriting the running
		// binary without first copying it to the rollback dir is the exact gap
		// that left deploy #6 (sha 507f846) on prod with no rollback. Copy
		// <binary> -> <dir>-rollback/<base>.<YYYYMMDD-HHMMSS> with cp -p, VERIFY
		// the copy landed (by byte size), and ABORT before the mv if a live
		// binary existed but the backup did NOT land — a deploy without a
		// rollback must never complete silently. A clean server with no live
		// binary (first deploy) skips the backup without failing.
		backupDir := dir + "-rollback"
		deployEmit(sw, "backup", "respaldando binario vivo -> "+backupDir+"/"+base+".<YYYYMMDD-HHMMSS>")
		bres, err := sshx.Run(&srv.Server, backupRemoteCmd(srv.BinaryPath), 30*time.Second)
		if err != nil {
			fail("backup", err.Error())
			return
		}
		if bres.ExitCode != 0 {
			fail("backup", "backup failed — ABORTING before swap so no deploy completes without a rollback: "+
				strings.TrimSpace(bres.Stdout+" "+bres.Stderr))
			return
		}
		for _, l := range strings.Split(strings.TrimSpace(bres.Stdout), "\n") {
			switch l = strings.TrimSpace(l); {
			case l == "":
			case l == "NO_PREVIOUS":
				deployEmit(sw, "backup", "no previous binary, skipping backup (first deploy to this server)")
			case strings.HasPrefix(l, "BACKED_UP "):
				deployEmit(sw, "backup", "✓ rollback guardado: "+strings.TrimPrefix(l, "BACKED_UP "))
			case strings.HasPrefix(l, "PRUNED "):
				deployEmit(sw, "backup", "retención (>10 auto): borrado "+strings.TrimPrefix(l, "PRUNED "))
			default:
				deployEmit(sw, "backup", l)
			}
		}

		swap := "cd " + dir + " && mv " + base + ".new " + base + " && " +
			"OLD=$(pgrep -x " + base + " || true); " +
			"if [ -n \"$OLD\" ]; then kill -TERM $OLD; " +
			"for i in $(seq 1 30); do pgrep -x " + base + " >/dev/null || exit 0; sleep 1; done; " +
			"echo 'old process still alive after 30s'; exit 1; " +
			"else echo 'no previous process'; fi"
		deployEmit(sw, "swap", "mv "+base+".new "+base+" && SIGTERM al proceso viejo…")
		res, err := sshx.Run(&srv.Server, swap, 60*time.Second)
		if err != nil {
			fail("swap", err.Error())
			return
		}
		if res.ExitCode != 0 {
			fail("swap", strings.TrimSpace(res.Stdout+" "+res.Stderr))
			return
		}
		deployEmit(sw, "swap", "✓ binario swapeado, proceso viejo terminado")

		// 5 — start script (sources that box's own secrets).
		deployEmit(sw, "start", "bash "+srv.StartScript)
		res, err = sshx.Run(&srv.Server, "bash "+srv.StartScript, 30*time.Second)
		if err != nil {
			fail("start", err.Error())
			return
		}
		if res.ExitCode != 0 {
			fail("start", strings.TrimSpace(res.Stdout+" "+res.Stderr))
			return
		}
		deployEmit(sw, "start", "✓ "+strings.TrimSpace(res.Stdout))

		// 6 — smoke: /health version-match + /readyz must both be 200.
		deployEmit(sw, "smoke", "GET /health (version "+shortSHA+") + /readyz vía tunnel…")
		if err := smokeCheck(srv, 20, shortSHA); err != nil {
			fail("smoke", err.Error())
			return
		}
		deployEmit(sw, "smoke", "✓ /health 200 · /readyz 200 · version "+shortSHA+" confirmed")

		status = "success"
		deployEmit(sw, "record", "deploy registrado (id "+strconv.FormatInt(deployID, 10)+", sha "+sha[:12]+")")
		fmt.Fprintf(sw.w, "event: done\ndata: {\"status\":\"success\",\"sha\":%q}\n\n", sha)
		sw.flusher.Flush()
	}
}

// backupRemoteCmd builds the remote shell that, before a swap, copies the
// LIVE engine binary to its sibling rollback dir (<dir>-rollback) as
// <base>.<YYYYMMDD-HHMMSS>, verifies the copy by byte size, and prunes
// AUTOMATIC timestamped backups beyond the newest 10. The retention glob
// (<base>.[0-9]*-[0-9]*) only ever matches timestamped names, so manual
// pre_* backups are never touched. The paths come from the server record
// (validated at registration), not from request input. Output contract,
// one token per line, parsed by the caller:
//
//	NO_PREVIOUS          no live binary — first deploy, nothing to back up
//	BACKED_UP <path> …   the rollback copy that was created
//	PRUNED <name>        an over-retention backup that was removed
//
// Any failure to copy/verify exits non-zero → the caller ABORTS the deploy
// before the mv, so a deploy never completes without a rollback.
func backupRemoteCmd(binaryPath string) string {
	base := filepath.Base(binaryPath)
	rbDir := filepath.Dir(binaryPath) + "-rollback"
	lines := []string{
		"SRC='" + binaryPath + "'",
		"RBDIR='" + rbDir + "'",
		"BASE='" + base + "'",
		`if [ ! -f "$SRC" ]; then echo NO_PREVIOUS; exit 0; fi`,
		`mkdir -p "$RBDIR" || { echo MKDIR_FAILED; exit 1; }`,
		`TS=$(date +%Y%m%d-%H%M%S)`,
		`DEST="$RBDIR/$BASE.$TS"`,
		`cp -p "$SRC" "$DEST" || { echo CP_FAILED; exit 1; }`,
		`SZS=$(stat -c%s "$SRC" 2>/dev/null); SZD=$(stat -c%s "$DEST" 2>/dev/null)`,
		`if [ ! -f "$DEST" ] || [ -z "$SZD" ] || [ "$SZS" != "$SZD" ]; then echo "VERIFY_FAILED src=$SZS dest=$SZD"; rm -f "$DEST"; exit 1; fi`,
		`echo "BACKED_UP $DEST ($SZD bytes)"`,
		`for f in $(ls -1 "$RBDIR/$BASE".[0-9]*-[0-9]* 2>/dev/null | sort -r | tail -n +11); do rm -f "$f" && echo "PRUNED $(basename "$f")"; done`,
		`exit 0`,
	}
	return strings.Join(lines, "; ")
}

// gitState returns HEAD's sha and the tracked-files porcelain status (empty =
// clean).
func gitState(repoDir string) (sha, dirty string, err error) {
	rev := exec.Command("git", "rev-parse", "HEAD")
	rev.Dir = repoDir
	out, err := rev.Output()
	if err != nil {
		return "", "", err
	}
	sha = strings.TrimSpace(string(out))
	st := exec.Command("git", "status", "--porcelain", "-uno")
	st.Dir = repoDir
	out, err = st.Output()
	if err != nil {
		return "", "", err
	}
	return sha, strings.TrimSpace(string(out)), nil
}

// smokeCheck polls /health (asserting version == wantVersion) and /readyz on
// the freshly-started engine until both answer 200 and the version matches, or
// attempts run out. Delegates to smokeCheckAddr so the version-checking logic
// is testable without an SSH tunnel.
func smokeCheck(srv *RegisteredServer, attempts int, wantVersion string) error {
	addr, closer, err := sshx.ForwardAddr(&srv.Server, srv.EnginePort)
	if err != nil {
		return fmt.Errorf("tunnel: %w", err)
	}
	if closer != nil {
		defer closer.Close() //nolint:errcheck
	}
	return smokeCheckAddr(addr, attempts, wantVersion, time.Second)
}

// smokeCheckAddr is the testable core of smokeCheck. On each attempt it:
//  1. GETs /health — must be 200 with a JSON body where "version" == wantVersion.
//     Non-200 or connection errors are retried; a 200 with the wrong version is
//     a hard failure (the swap step already waited for the old process to die,
//     so the wrong version means the deployed binary wasn't built with the
//     expected SHA).
//  2. GETs /readyz — must be 200 (engine done warming its pool).
//
// retryInterval is the sleep between retriable attempts (time.Second in
// production, 0 in tests).
func smokeCheckAddr(addr string, attempts int, wantVersion string, retryInterval time.Duration) error {
	type healthResp struct {
		Version string `json:"version"`
	}
	client := &http.Client{Timeout: 3 * time.Second}
	var lastErr error
	for i := 0; i < attempts; i++ {
		resp, err := client.Get("http://" + addr + "/health")
		if err != nil {
			lastErr = fmt.Errorf("engine not healthy yet")
			time.Sleep(retryInterval)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close() //nolint:errcheck
			lastErr = fmt.Errorf("engine not healthy yet")
			time.Sleep(retryInterval)
			continue
		}
		var h healthResp
		if decErr := json.NewDecoder(resp.Body).Decode(&h); decErr != nil {
			resp.Body.Close() //nolint:errcheck
			lastErr = fmt.Errorf("could not parse /health body: %v", decErr)
			time.Sleep(retryInterval)
			continue
		}
		resp.Body.Close() //nolint:errcheck
		if h.Version != wantVersion {
			return fmt.Errorf("version mismatch: got %q want %q", h.Version, wantVersion)
		}
		r2, err := client.Get("http://" + addr + "/readyz")
		if err != nil {
			lastErr = fmt.Errorf("readyz failed: %v", err)
			time.Sleep(retryInterval)
			continue
		}
		ok := r2.StatusCode == http.StatusOK
		r2.Body.Close() //nolint:errcheck
		if !ok {
			lastErr = fmt.Errorf("readyz not ready yet")
			time.Sleep(retryInterval)
			continue
		}
		return nil
	}
	return fmt.Errorf("smoke FAILED after %d attempts: %v", attempts, lastErr)
}

func recordDeployStart(serverID int64, sha string) int64 {
	if benchDB == nil {
		return 0
	}
	res, err := benchDB.Exec(`INSERT INTO deploys (server_id, sha, status) VALUES (?,?, 'running')`, serverID, sha)
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

func recordDeployEnd(deployID int64, status string) {
	if benchDB == nil || deployID == 0 {
		return
	}
	benchDB.Exec(`UPDATE deploys SET status = ?, finished_at = CURRENT_TIMESTAMP WHERE id = ?`, //nolint:errcheck
		status, deployID)
}

// DeploysListHandler — GET /api/deploys?server_id=
func DeploysListHandler(w http.ResponseWriter, r *http.Request) {
	if benchDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "bench DB not initialized")
		return
	}
	q := `SELECT d.id, d.server_id, s.name, d.sha, d.status, d.started_at, COALESCE(d.finished_at, '')
	        FROM deploys d JOIN servers s ON s.id = d.server_id`
	args := []any{}
	if sid := r.URL.Query().Get("server_id"); sid != "" {
		id, err := strconv.ParseInt(sid, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid server_id")
			return
		}
		q += ` WHERE d.server_id = ?`
		args = append(args, id)
	}
	q += ` ORDER BY d.id DESC LIMIT 100`
	rows, err := benchDB.Query(q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close() //nolint:errcheck
	type dep struct {
		ID         int64  `json:"id"`
		ServerID   int64  `json:"server_id"`
		ServerName string `json:"server_name"`
		SHA        string `json:"sha"`
		Status     string `json:"status"`
		StartedAt  string `json:"started_at"`
		FinishedAt string `json:"finished_at"`
	}
	out := []dep{}
	for rows.Next() {
		var d dep
		if err := rows.Scan(&d.ID, &d.ServerID, &d.ServerName, &d.SHA, &d.Status, &d.StartedAt, &d.FinishedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}
