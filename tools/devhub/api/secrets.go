package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/appximo/appximo/tools/devhub/secrets"
	"github.com/appximo/appximo/tools/devhub/sshx"
)

// S47b: encrypted secrets store wiring. Values decrypt only into process
// memory; this file makes sure no handler ever ECHOES one — every response
// here is existence/ok/audit metadata.

var secretStore *secrets.Store

// InitSecrets connects the age store and routes its usage audit into SQLite.
func InitSecrets(store *secrets.Store) {
	store.Audit = func(serverID, operation string) {
		id, err := strconv.ParseInt(serverID, 10, 64)
		if err != nil {
			return
		}
		recordSecretAccess(id, operation)
	}
	secretStore = store
}

func recordSecretAccess(serverID int64, operation string) {
	if benchDB == nil {
		return
	}
	benchDB.Exec(`INSERT INTO secret_access (server_id, operation) VALUES (?,?)`, //nolint:errcheck
		serverID, operation)
}

// adminKeyFor resolves a server's engine admin key: encrypted store first,
// then the legacy admin_key_env process env var. operation labels the use in
// the audit trail. Empty string = no key available (scrape without header).
func adminKeyFor(s *RegisteredServer, operation string) string {
	if secretStore != nil {
		if v, ok := secretStore.Get(strconv.FormatInt(s.ID, 10), "admin_key", operation); ok {
			return v
		}
	}
	return s.AdminKey()
}

// ServerFetchAdminKeyHandler — POST /api/servers/{id}/fetch-admin-key
// The human's click IS the authorization: the DevHub reads ADMIN_KEY from the
// server's own secrets file over SSH (fixed command, validated path) and
// stores it encrypted. The value never appears in the response, logs or DB.
func ServerFetchAdminKeyHandler(w http.ResponseWriter, r *http.Request) {
	s, ok := serverFromPath(w, r)
	if !ok {
		return
	}
	if secretStore == nil {
		writeErr(w, http.StatusServiceUnavailable, "secrets store not initialized")
		return
	}
	// Fixed command; secrets_path was validated against absPathRe at
	// registration. tail -1: last assignment wins, like `source` would.
	res, err := sshx.Run(&s.Server,
		"grep -E '^(export )?ADMIN_KEY=' "+s.SecretsPath+" 2>/dev/null | tail -1", 20*time.Second)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "ssh: "+err.Error())
		return
	}
	value := parseEnvAssignment(res.Stdout, "ADMIN_KEY")
	if value == "" {
		writeErr(w, http.StatusNotFound,
			"no ADMIN_KEY found in "+s.SecretsPath+" on "+s.Name+" (file missing or key not set)")
		return
	}
	if err := secretStore.Set(strconv.FormatInt(s.ID, 10), "admin_key", value); err != nil {
		writeErr(w, http.StatusInternalServerError, "store secret: "+err.Error())
		return
	}
	recordSecretAccess(s.ID, "fetch_from_server")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// parseEnvAssignment extracts VALUE from a `[export ]NAME=VALUE` line,
// stripping one level of single or double quotes.
func parseEnvAssignment(line, name string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "export ")
	if !strings.HasPrefix(line, name+"=") {
		return ""
	}
	v := strings.TrimPrefix(line, name+"=")
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
	}
	return v
}

// ServerSetAdminKeyHandler — PUT /api/servers/{id}/admin-key  body {"value":"..."}
// Manual load for servers where the SSH fetch does not apply.
func ServerSetAdminKeyHandler(w http.ResponseWriter, r *http.Request) {
	s, ok := serverFromPath(w, r)
	if !ok {
		return
	}
	if secretStore == nil {
		writeErr(w, http.StatusServiceUnavailable, "secrets store not initialized")
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Value == "" || len(req.Value) > 512 {
		writeErr(w, http.StatusBadRequest, "value must be non-empty and at most 512 chars")
		return
	}
	if err := secretStore.Set(strconv.FormatInt(s.ID, 10), "admin_key", req.Value); err != nil {
		writeErr(w, http.StatusInternalServerError, "store secret: "+err.Error())
		return
	}
	recordSecretAccess(s.ID, "manual_set")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ServerDeleteAdminKeyHandler — DELETE /api/servers/{id}/admin-key (rotation).
func ServerDeleteAdminKeyHandler(w http.ResponseWriter, r *http.Request) {
	s, ok := serverFromPath(w, r)
	if !ok {
		return
	}
	if secretStore == nil {
		writeErr(w, http.StatusServiceUnavailable, "secrets store not initialized")
		return
	}
	if err := secretStore.DeleteKey(strconv.FormatInt(s.ID, 10), "admin_key"); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	recordSecretAccess(s.ID, "rotate")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ServerSecretStatusHandler — GET /api/servers/{id}/secret-status
// The only thing the API reveals about a secret: whether one exists, and where
// it would come from. Never the value.
func ServerSecretStatusHandler(w http.ResponseWriter, r *http.Request) {
	s, ok := serverFromPath(w, r)
	if !ok {
		return
	}
	present, source := false, ""
	if secretStore != nil && secretStore.Has(strconv.FormatInt(s.ID, 10), "admin_key") {
		present, source = true, "store"
	} else if s.AdminKey() != "" {
		present, source = true, "env"
	}
	writeJSON(w, http.StatusOK, map[string]any{"admin_key_present": present, "source": source})
}

// SecretAuditHandler — GET /api/audit/secrets?server_id=
func SecretAuditHandler(w http.ResponseWriter, r *http.Request) {
	if benchDB == nil {
		writeErr(w, http.StatusServiceUnavailable, "bench DB not initialized")
		return
	}
	q := `SELECT id, server_id, operation, ts FROM secret_access`
	args := []any{}
	if sid := r.URL.Query().Get("server_id"); sid != "" {
		id, err := strconv.ParseInt(sid, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid server_id")
			return
		}
		q += ` WHERE server_id = ?`
		args = append(args, id)
	}
	q += ` ORDER BY id DESC LIMIT 100`
	rows, err := benchDB.Query(q, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close() //nolint:errcheck
	type entry struct {
		ID        int64  `json:"id"`
		ServerID  int64  `json:"server_id"`
		Operation string `json:"operation"`
		TS        string `json:"ts"`
	}
	out := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.ID, &e.ServerID, &e.Operation, &e.TS); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

// serverFromPath loads the {id} path-value server or writes the error.
func serverFromPath(w http.ResponseWriter, r *http.Request) (*RegisteredServer, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid server id")
		return nil, false
	}
	s, err := LoadServer(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return nil, false
	}
	return s, true
}
