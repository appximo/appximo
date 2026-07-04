package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/miguelangel/appitools/tools/devhub/sshx"
)

// RegisteredServer is a row of the servers table plus its sshx identity. The
// SSH key is exposed to API consumers as its basename only — the full path
// stays server-side and the key material never leaves the filesystem.
type RegisteredServer struct {
	sshx.Server
	EnginePort   int
	AdminKeyEnv  string
	IsProduction bool
	BenchTenant  string
	SecretsPath  string
	StartScript  string
	BinaryPath   string
	LogPath      string
	CreatedAt    string
}

// EngineURL is the direct (untunneled) base URL of the server's engine —
// what an external load generator should hit.
func (s *RegisteredServer) EngineURL() string {
	host := s.Host
	if s.Local() {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%d", host, s.EnginePort)
}

// AdminKey resolves the server's engine admin key from the devhub process
// environment (admin_key_env names the variable; secrets never live in SQLite).
func (s *RegisteredServer) AdminKey() string {
	if s.AdminKeyEnv == "" {
		return ""
	}
	return os.Getenv(s.AdminKeyEnv)
}

// Input validation. Strict allowlists: these values end up in SSH dials and as
// arguments to fixed commands, so anything outside the pattern is rejected.
var (
	serverNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$`)
	hostnameRe   = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]{0,252}[a-zA-Z0-9])?$`)
	sshUserRe    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	envVarNameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	absPathRe    = regexp.MustCompile(`^/[a-zA-Z0-9._/-]{1,255}$`)
)

func validHost(h string) bool {
	if net.ParseIP(h) != nil {
		return true
	}
	return hostnameRe.MatchString(h)
}

func scanServer(row interface{ Scan(...any) error }) (*RegisteredServer, error) {
	var s RegisteredServer
	var prod int
	var adminEnv, hostKey sql.NullString
	if err := row.Scan(&s.ID, &s.Name, &s.Host, &s.Port, &s.User, &s.KeyPath,
		&s.EnginePort, &adminEnv, &prod, &s.BenchTenant, &s.SecretsPath, &hostKey,
		&s.StartScript, &s.BinaryPath, &s.LogPath, &s.CreatedAt); err != nil {
		return nil, err
	}
	s.AdminKeyEnv = adminEnv.String
	s.HostKey = hostKey.String
	s.IsProduction = prod != 0
	// Persist the host key on first connect (TOFU).
	id := s.ID
	s.OnHostKey = func(key string) error {
		_, err := benchDB.Exec(`UPDATE servers SET host_key = ? WHERE id = ? AND (host_key IS NULL OR host_key = '')`, key, id)
		return err
	}
	return &s, nil
}

const serverCols = `id, name, host, port, ssh_user, key_path, engine_port,
	admin_key_env, is_production, bench_tenant, secrets_path, host_key, start_script, binary_path, log_path, created_at`

// LoadServer fetches one registered server by id.
func LoadServer(id int64) (*RegisteredServer, error) {
	if benchDB == nil {
		return nil, errors.New("bench DB not initialized")
	}
	s, err := scanServer(benchDB.QueryRow(`SELECT `+serverCols+` FROM servers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("server %d not registered", id)
	}
	return s, err
}

// serverJSON is the API shape of a server. key_path is reduced to its basename.
func serverJSON(s *RegisteredServer) map[string]any {
	return map[string]any{
		"id":            s.ID,
		"name":          s.Name,
		"host":          s.Host,
		"port":          s.Port,
		"ssh_user":      s.User,
		"key_file":      filepath.Base(s.KeyPath),
		"engine_port":   s.EnginePort,
		"admin_key_env": s.AdminKeyEnv,
		"is_production": s.IsProduction,
		"bench_tenant":  s.BenchTenant,
		"secrets_path":  s.SecretsPath,
		"has_host_key":  s.HostKey != "",
		"is_local":      s.Local(),
		"start_script":  s.StartScript,
		"binary_path":   s.BinaryPath,
		"log_path":      s.LogPath,
		"created_at":    s.CreatedAt,
	}
}

// ServersListHandler — GET /api/servers
func ServersListHandler(w http.ResponseWriter, r *http.Request) {
	if benchDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "bench DB not initialized")
		return
	}
	rows, err := benchDB.Query(`SELECT ` + serverCols + ` FROM servers ORDER BY id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close() //nolint:errcheck
	out := []map[string]any{}
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, serverJSON(s))
	}
	writeJSON(w, http.StatusOK, out)
}

// ServersCreateHandler — POST /api/servers
func ServersCreateHandler(w http.ResponseWriter, r *http.Request) {
	if benchDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "bench DB not initialized")
		return
	}
	var req struct {
		Name         string `json:"name"`
		Host         string `json:"host"`
		Port         int    `json:"port"`
		SSHUser      string `json:"ssh_user"`
		KeyPath      string `json:"key_path"`
		EnginePort   int    `json:"engine_port"`
		AdminKeyEnv  string `json:"admin_key_env"`
		IsProduction bool   `json:"is_production"`
		BenchTenant  string `json:"bench_tenant"`
		SecretsPath  string `json:"secrets_path"`
		StartScript  string `json:"start_script"`
		BinaryPath   string `json:"binary_path"`
		LogPath      string `json:"log_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.SSHUser == "" {
		req.SSHUser = "root"
	}
	if req.EnginePort == 0 {
		req.EnginePort = 8080
	}
	if req.BenchTenant == "" {
		req.BenchTenant = "acme"
	}
	if req.SecretsPath == "" {
		req.SecretsPath = "/root/.appitools-secrets"
	}
	if req.StartScript == "" {
		req.StartScript = "/tmp/start_prod.sh"
	}
	if req.BinaryPath == "" {
		req.BinaryPath = "/root/appitools/appitools"
	}
	if req.LogPath == "" {
		req.LogPath = "/tmp/appitools.log"
	}

	switch {
	case !serverNameRe.MatchString(req.Name):
		writeErr(w, http.StatusBadRequest, "name must match ^[a-zA-Z0-9][a-zA-Z0-9_-]{0,31}$")
		return
	case !validHost(req.Host):
		writeErr(w, http.StatusBadRequest, "host must be a valid IP or hostname")
		return
	case req.Port < 1 || req.Port > 65535:
		writeErr(w, http.StatusBadRequest, "port must be in [1,65535]")
		return
	case !sshUserRe.MatchString(req.SSHUser):
		writeErr(w, http.StatusBadRequest, "ssh_user must match ^[a-z_][a-z0-9_-]{0,31}$")
		return
	case req.EnginePort < 1 || req.EnginePort > 65535:
		writeErr(w, http.StatusBadRequest, "engine_port must be in [1,65535]")
		return
	case req.AdminKeyEnv != "" && !envVarNameRe.MatchString(req.AdminKeyEnv):
		writeErr(w, http.StatusBadRequest, "admin_key_env must be an env var NAME (^[A-Z_][A-Z0-9_]{0,63}$)")
		return
	case !benchTenantRe.MatchString(req.BenchTenant):
		writeErr(w, http.StatusBadRequest, "bench_tenant must match ^[a-z0-9_]{1,32}$")
		return
	case !absPathRe.MatchString(req.KeyPath):
		writeErr(w, http.StatusBadRequest, "key_path must be an absolute path")
		return
	case !absPathRe.MatchString(req.StartScript) || !absPathRe.MatchString(req.BinaryPath) ||
		!absPathRe.MatchString(req.LogPath) || !absPathRe.MatchString(req.SecretsPath):
		writeErr(w, http.StatusBadRequest, "start_script, binary_path, log_path and secrets_path must be absolute paths")
		return
	}
	if fi, err := os.Stat(req.KeyPath); err != nil || fi.IsDir() {
		writeErr(w, http.StatusBadRequest, "key_path does not exist on the devhub box (or is a directory)")
		return
	}

	prod := 0
	if req.IsProduction {
		prod = 1
	}
	res, err := benchDB.Exec(`INSERT INTO servers
		(name, host, port, ssh_user, key_path, engine_port, admin_key_env, is_production, bench_tenant, secrets_path, start_script, binary_path, log_path)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		req.Name, req.Host, req.Port, req.SSHUser, req.KeyPath, req.EnginePort,
		req.AdminKeyEnv, prod, req.BenchTenant, req.SecretsPath, req.StartScript, req.BinaryPath, req.LogPath)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeErr(w, http.StatusConflict, "a server with that name already exists")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	s, err := LoadServer(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, serverJSON(s))
}

// ServersDeleteHandler — DELETE /api/servers/{id}
func ServersDeleteHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid server id")
		return
	}
	res, err := benchDB.Exec(`DELETE FROM servers WHERE id = ?`, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, "server not found")
		return
	}
	sshx.Close(id)
	// A deleted server's secrets die with it.
	if secretStore != nil {
		if err := secretStore.Delete(strconv.FormatInt(id, 10)); err != nil {
			writeErr(w, http.StatusInternalServerError, "server deleted but secrets cleanup failed: "+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// ServersTestHandler — POST /api/servers/{id}/test
// Dials the server (TOFU-pinning the host key on first connect), runs the
// fixed identification commands and probes the engine's /health through an
// SSH tunnel. Pure read-only diagnostics.
func ServersTestHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid server id")
		return
	}
	s, err := LoadServer(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}

	report := map[string]any{
		"server": s.Name, "host": s.Host, "is_local": s.Local(),
	}

	// Fixed commands only — nothing user-supplied reaches the remote shell.
	res, err := sshx.Run(&s.Server, "uname -a && nproc && free -m | awk 'NR==2{print $2}'", 20*time.Second)
	if err != nil {
		report["ssh_ok"] = false
		report["error"] = err.Error()
		writeJSON(w, http.StatusBadGateway, report)
		return
	}
	report["ssh_ok"] = true
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) >= 3 {
		report["uname"] = lines[0]
		report["nproc"], _ = strconv.Atoi(strings.TrimSpace(lines[1]))
		report["mem_total_mb"], _ = strconv.Atoi(strings.TrimSpace(lines[2]))
	} else {
		report["raw"] = strings.TrimSpace(res.Stdout)
	}

	// Engine probe via tunnel (direct for local).
	up, status := probeEngine(s)
	report["engine_port"] = s.EnginePort
	report["engine_up"] = up
	if status != "" {
		report["engine_health"] = status
	}
	report["host_key_pinned"] = s.HostKey != "" || s.Local()
	writeJSON(w, http.StatusOK, report)
}

// probeEngine GETs /health on the server's engine through ForwardAddr.
func probeEngine(s *RegisteredServer) (bool, string) {
	addr, closer, err := sshx.ForwardAddr(&s.Server, s.EnginePort)
	if err != nil {
		return false, ""
	}
	if closer != nil {
		defer closer.Close() //nolint:errcheck
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		return false, ""
	}
	defer resp.Body.Close() //nolint:errcheck
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	return resp.StatusCode == http.StatusOK, strings.TrimSpace(string(buf[:n]))
}
