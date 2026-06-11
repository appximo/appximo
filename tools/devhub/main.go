package main

import (
	_ "embed"
	"log"
	"net/http"
	"os"

	"github.com/miguelangel/appitools/tools/devhub/api"
	"github.com/miguelangel/appitools/tools/devhub/secrets"
)

// benchSchema is the SQLite DDL for the statistics store. Embedded here (db/ is a
// subdirectory of the main package) because the api package can't go:embed a
// sibling directory.
//
//go:embed db/schema.sql
var benchSchema string

func main() {
	repoDir := os.Getenv("APPITOOLS_DIR")
	if repoDir == "" {
		repoDir = "/root/appitools"
	}
	api.StartMetricsScraper()
	if err := api.InitBenchDB(repoDir, benchSchema); err != nil {
		log.Printf("WARNING: bench DB init failed (bench endpoints disabled): %v", err)
	}
	// Encrypted secrets store (S47b): age identity + ciphertext live under
	// /root/.devhub on this box only.
	if store, err := secrets.Open("/root/.devhub"); err != nil {
		log.Printf("WARNING: secrets store init failed (admin keys fall back to env vars): %v", err)
	} else {
		api.InitSecrets(store)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/run", api.RunHandler(repoDir))
	mux.HandleFunc("/api/metrics/live", api.MetricsLiveRouter)
	mux.HandleFunc("/api/metrics/snapshot", api.MetricsSnapshotHandler)

	// Server registry + remote ops (S47).
	mux.HandleFunc("GET /api/servers", api.ServersListHandler)
	mux.HandleFunc("POST /api/servers", api.ServersCreateHandler)
	mux.HandleFunc("DELETE /api/servers/{id}", api.ServersDeleteHandler)
	mux.HandleFunc("POST /api/servers/{id}/test", api.ServersTestHandler)

	// Encrypted secrets store (S47b). Existence and audit only — values never
	// travel through the API.
	mux.HandleFunc("POST /api/servers/{id}/fetch-admin-key", api.ServerFetchAdminKeyHandler)
	mux.HandleFunc("PUT /api/servers/{id}/admin-key", api.ServerSetAdminKeyHandler)
	mux.HandleFunc("DELETE /api/servers/{id}/admin-key", api.ServerDeleteAdminKeyHandler)
	mux.HandleFunc("GET /api/servers/{id}/secret-status", api.ServerSecretStatusHandler)
	mux.HandleFunc("GET /api/audit/secrets", api.SecretAuditHandler)
	mux.HandleFunc("POST /api/deploy", api.DeployHandler(repoDir))
	mux.HandleFunc("GET /api/deploys", api.DeploysListHandler)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"devhub"}`))
	})

	// Statistical benchmark engine (S42).
	mux.HandleFunc("POST /api/bench/import", api.BenchImportHandler)
	mux.HandleFunc("GET /api/bench/runs", api.BenchRunsHandler)
	mux.HandleFunc("GET /api/bench/export", api.BenchExportHandler)
	mux.HandleFunc("GET /api/bench/runs/{id}/histogram", api.BenchRunHistogramHandler)
	mux.HandleFunc("GET /api/bench/runs/{id}/stats", api.BenchRunStatsHandler)
	mux.HandleFunc("POST /api/bench/compare", api.BenchCompareHandler)
	mux.HandleFunc("POST /api/bench/compare-groups", api.BenchCompareGroupsHandler)
	mux.HandleFunc("GET /api/bench/comparisons", api.BenchComparisonsHandler)
	mux.HandleFunc("POST /api/bench/protocol", api.BenchProtocolHandler(repoDir))

	mux.Handle("/", getUIHandler())
	log.Printf("DevHub :3099 — repo: %s", repoDir)
	log.Fatal(http.ListenAndServe(":3099", cors(mux)))
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
