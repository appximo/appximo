package main

import (
	"log"
	"net/http"
	"os"

	"github.com/miguelangel/appitools/tools/devhub/api"
)

func main() {
	repoDir := os.Getenv("APPITOOLS_DIR")
	if repoDir == "" {
		repoDir = "/root/appitools"
	}
	api.StartMetricsScraper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/run", api.RunHandler(repoDir))
	mux.HandleFunc("/api/metrics/live", api.MetricsLiveHandler)
	mux.HandleFunc("/api/metrics/snapshot", api.MetricsSnapshotHandler)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"devhub"}`))
	})
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
