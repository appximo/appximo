package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MetricSample struct {
	TS            int64   `json:"ts"`
	RequestsTotal float64 `json:"requests_total"`
	P95Ms         float64 `json:"p95_ms"`
	ActiveTenants float64 `json:"active_tenants"`
	MotorUp       bool    `json:"motor_up"`
}

const ringSize = 720

var (
	ring    [ringSize]MetricSample
	ringPos int
	ringMu  sync.RWMutex
)

func StartMetricsScraper() {
	go func() {
		scrape()
		ticker := time.NewTicker(5 * time.Second)
		for range ticker.C {
			scrape()
		}
	}()
}

func scrape() {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://localhost:8080/metrics")
	sample := MetricSample{TS: time.Now().Unix()}
	if err != nil {
		sample.MotorUp = false
	} else {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		sample = parseMetrics(string(body))
		sample.TS = time.Now().Unix()
		sample.MotorUp = true
	}
	ringMu.Lock()
	ring[ringPos] = sample
	ringPos = (ringPos + 1) % ringSize
	ringMu.Unlock()
}

func parseMetrics(body string) MetricSample {
	s := MetricSample{}
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "appitools_requests_total{") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
					s.RequestsTotal += v
				}
			}
		}
		if strings.HasPrefix(line, "appitools_active_tenants ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
					s.ActiveTenants = v
				}
			}
		}
		if strings.Contains(line, "appitools_request_duration_seconds{") &&
			strings.Contains(line, `quantile="0.95"`) {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				if v, err := strconv.ParseFloat(parts[1], 64); err == nil {
					s.P95Ms = v * 1000
				}
			}
		}
	}
	return s
}

func getSnapshot() []MetricSample {
	ringMu.RLock()
	defer ringMu.RUnlock()
	samples := make([]MetricSample, 0, ringSize)
	for i := 0; i < ringSize; i++ {
		idx := (ringPos + i) % ringSize
		if ring[idx].TS > 0 {
			samples = append(samples, ring[idx])
		}
	}
	return samples
}

func MetricsSnapshotHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(getSnapshot())
}

func MetricsLiveHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	send := func() {
		data, _ := json.Marshal(getSnapshot())
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	send()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			send()
		}
	}
}
