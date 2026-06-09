package api

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
)

var allowedTargets = map[string]bool{
	"test": true, "test-integration": true, "test-e2e": true,
	"test-resilience": true, "test-perf": true, "test-all": true,
	"bench": true, "build": true, "lint": true, "cover": true,
}

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

func (sw *sseWriter) Write(p []byte) (n int, err error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	raw := strings.TrimRight(string(p), "\n")
	for _, line := range strings.Split(raw, "\n") {
		safe := strings.ReplaceAll(line, "\n", " ")
		fmt.Fprintf(sw.w, "data: %s\n\n", safe)
	}
	sw.flusher.Flush()
	return len(p), nil
}

func RunHandler(repoDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("target")
		if target == "" {
			http.Error(w, `{"error":"target required"}`, http.StatusBadRequest)
			return
		}
		if !allowedTargets[target] {
			http.Error(w, `{"error":"target not allowed"}`, http.StatusForbidden)
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
		fmt.Fprintf(w, "event: start\ndata: {\"target\":\"%s\"}\n\n", target)
		flusher.Flush()
		cmd := exec.CommandContext(r.Context(), "make", target)
		cmd.Dir = repoDir
		cmd.Stdout = sw
		cmd.Stderr = sw
		exitCode := 0
		if err := cmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		fmt.Fprintf(w, "event: done\ndata: {\"exit\":%d,\"target\":\"%s\"}\n\n", exitCode, target)
		flusher.Flush()
	}
}
