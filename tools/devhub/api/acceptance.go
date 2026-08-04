package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/appximo/appximo/tools/devhub/sshx"
)

var (
	ansiEscape        = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	acceptanceMu      sync.Mutex
	acceptanceRunning = map[int64]bool{}
)

func stripANSI(s string) string { return ansiEscape.ReplaceAllString(s, "") }

// AcceptanceRunHandler — GET /api/acceptance-run?server=<id>
//
// Streams scripts/acceptance-test.sh against the chosen server over SSH
// tunnels (data-plane :engine_port and control-plane :9090). Each output line
// is delivered as an SSE "line" event; a final "done" event carries the exit
// code and pass/fail/info counts. Concurrent runs against the same server are
// rejected with 409 to prevent test interference.
func AcceptanceRunHandler(repoDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sid, err := strconv.ParseInt(r.URL.Query().Get("server"), 10, 64)
		if err != nil || sid == 0 {
			writeErr(w, http.StatusBadRequest, "server query param must be a valid server id")
			return
		}
		srv, err := LoadServer(sid)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}

		// One run at a time per server to avoid cross-run tenant interference.
		acceptanceMu.Lock()
		if acceptanceRunning[sid] {
			acceptanceMu.Unlock()
			writeErr(w, http.StatusConflict, "acceptance run already in progress for this server")
			return
		}
		acceptanceRunning[sid] = true
		acceptanceMu.Unlock()
		defer func() {
			acceptanceMu.Lock()
			delete(acceptanceRunning, sid)
			acceptanceMu.Unlock()
		}()

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		emit := func(line string) {
			payload, _ := json.Marshal(map[string]string{"line": line})
			fmt.Fprintf(w, "event: line\ndata: %s\n\n", payload)
			flusher.Flush()
		}
		finish := func(exitCode, pass, fail, info int) {
			payload, _ := json.Marshal(map[string]int{
				"exit": exitCode, "pass": pass, "fail": fail, "info": info,
			})
			fmt.Fprintf(w, "event: done\ndata: %s\n\n", payload)
			flusher.Flush()
		}
		abort := func(msg string) {
			emit("✗ " + msg)
			finish(1, 0, 1, 0)
		}

		// SSH tunnels to both ports — ForwardAddr returns localhost:port for
		// local servers (no tunnel opened) and an ephemeral local port for
		// remote servers. This handles both cases uniformly without needing to
		// know whether the server's ports are publicly reachable.
		baseAddr, baseCloser, err := sshx.ForwardAddr(&srv.Server, srv.EnginePort)
		if err != nil {
			abort("tunnel a data plane fallida: " + err.Error())
			return
		}
		if baseCloser != nil {
			defer baseCloser.Close() //nolint:errcheck
		}
		adminAddr, adminCloser, err := sshx.ForwardAddr(&srv.Server, 9090)
		if err != nil {
			abort("tunnel a control plane (:9090) fallida: " + err.Error())
			return
		}
		if adminCloser != nil {
			defer adminCloser.Close() //nolint:errcheck
		}

		jwtSecret, err := acceptanceFetchJWT(srv)
		if err != nil {
			abort("no se pudo obtener JWT_SECRET: " + err.Error())
			return
		}
		adminKey := srv.AdminKey()
		if adminKey == "" {
			abort("ADMIN_KEY vacío: configura admin_key_env en el server o ejecutá fetch-admin-key")
			return
		}

		// Local appximo binary for CLI operations (token minting only — no
		// DB needed). Use the engine binary from this devhub box.
		appximoCLI := repoDir + "/appximo"
		if _, err := os.Stat(appximoCLI); err != nil {
			appximoCLI = "/tmp/appximo-deploy"
			if _, err2 := os.Stat(appximoCLI); err2 != nil {
				abort("no se encontró el binario appximo para CLI (appximo token). Hacé: scripts/build-engine.sh appximo")
				return
			}
		}

		emit(fmt.Sprintf("⎇ acceptance-test.sh → http://%s (control: http://%s)", baseAddr, adminAddr))

		pReader, pWriter := io.Pipe()
		cmd := exec.CommandContext(r.Context(), "bash", "scripts/acceptance-test.sh")
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"BASE=http://"+baseAddr,
			"ADMIN=http://"+adminAddr,
			"JWT_SECRET="+jwtSecret,
			"ADMIN_KEY="+adminKey,
			"APPXIMO_CLI="+appximoCLI,
			"SCHEMA_FILE="+repoDir+"/examples/quickstart/schema.json",
			"PSQL_CMD=", // disable: no direct Postgres access through the tunnel
		)
		cmd.Stdout = pWriter
		cmd.Stderr = pWriter

		if err := cmd.Start(); err != nil {
			pWriter.Close() //nolint:errcheck
			abort("no se pudo iniciar scripts/acceptance-test.sh: " + err.Error())
			return
		}

		waitDone := make(chan error, 1)
		go func() {
			err := cmd.Wait()
			pWriter.Close() //nolint:errcheck
			waitDone <- err
		}()

		var pass, fail, info int
		scanner := bufio.NewScanner(pReader)
		for scanner.Scan() {
			clean := stripANSI(scanner.Text())
			emit(clean)
			switch {
			case strings.HasPrefix(clean, "✓ PASS"):
				pass++
			case strings.HasPrefix(clean, "✗ FAIL"):
				fail++
			case strings.HasPrefix(clean, "ℹ INFO"):
				info++
			}
		}

		runErr := <-waitDone
		exitCode := 0
		if runErr != nil {
			if ee, ok2 := runErr.(*exec.ExitError); ok2 {
				exitCode = ee.ExitCode()
			} else {
				exitCode = 1
			}
		}
		finish(exitCode, pass, fail, info)
	}
}

// acceptanceFetchJWT retrieves JWT_SECRET for the target server. For local
// servers it reads from the devhub process environment (the service unit
// inherits it). For remote servers it sources the server's registered secrets
// file via SSH — the path is an absolute path validated at registration, not
// user input.
func acceptanceFetchJWT(srv *RegisteredServer) (string, error) {
	if srv.Local() {
		if v := os.Getenv("JWT_SECRET"); v != "" {
			return v, nil
		}
	}
	// set -a makes all subsequent assignments exported; source reads the file;
	// then we print JWT_SECRET. Works for both "export KEY=val" and "KEY=val"
	// style secrets files. sshx.Run delegates to runLocal for local servers.
	cmd := "set -a && source " + srv.SecretsPath + " 2>/dev/null && echo \"$JWT_SECRET\""
	res, err := sshx.Run(&srv.Server, cmd, 10*time.Second)
	if err != nil {
		return "", fmt.Errorf("ssh source secrets: %w", err)
	}
	v := strings.TrimSpace(res.Stdout)
	if v == "" {
		return "", fmt.Errorf("JWT_SECRET not set in %s", srv.SecretsPath)
	}
	return v, nil
}
