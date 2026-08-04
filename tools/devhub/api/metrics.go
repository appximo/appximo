package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
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
	// /metrics is admin-gated (observability.AdminAuth) → 401 without the key.
	// Send X-Admin-Key from ADMIN_KEY so the scraper gets the real Prometheus body.
	req, _ := http.NewRequest("GET", "http://localhost:8080/metrics", nil)
	if adminKey := os.Getenv("ADMIN_KEY"); adminKey != "" {
		req.Header.Set("X-Admin-Key", adminKey)
	}
	resp, err := client.Do(req)
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
	// appximo_request_duration_seconds is a histogram, not a summary, so there is
	// no quantile="0.95" series. Aggregate the cumulative _bucket counts by upper
	// bound (le) across every label set — the equivalent of Prometheus
	// `sum by (le)` — then interpolate p95 from the buckets below.
	buckets := map[float64]float64{} // le upper bound -> summed cumulative count
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "appximo_requests_total{"):
			if v, ok := lastFieldFloat(line); ok {
				s.RequestsTotal += v
			}
		case strings.HasPrefix(line, "appximo_active_tenants "):
			if v, ok := lastFieldFloat(line); ok {
				s.ActiveTenants = v
			}
		case strings.HasPrefix(line, "appximo_request_duration_seconds_bucket{"):
			le, ok := labelValue(line, "le")
			if !ok {
				continue
			}
			ub, err := parseLE(le)
			if err != nil {
				continue
			}
			if v, ok := lastFieldFloat(line); ok {
				buckets[ub] += v
			}
		}
	}
	s.P95Ms = histogramQuantile(0.95, buckets) * 1000
	return s
}

// lastFieldFloat parses the last whitespace-separated token of a Prometheus line
// (the metric value) as a float.
func lastFieldFloat(line string) (float64, bool) {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0, false
	}
	v, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// labelValue extracts the value of label `name` from a Prometheus line, e.g.
// labelValue(`..._bucket{le="0.05"} 6`, "le") -> "0.05".
func labelValue(line, name string) (string, bool) {
	key := name + `="`
	i := strings.Index(line, key)
	if i < 0 {
		return "", false
	}
	i += len(key)
	j := strings.IndexByte(line[i:], '"')
	if j < 0 {
		return "", false
	}
	return line[i : i+j], true
}

// parseLE parses a histogram bucket upper bound; the "+Inf" bucket maps to +Inf.
func parseLE(le string) (float64, error) {
	if le == "+Inf" {
		return math.Inf(1), nil
	}
	return strconv.ParseFloat(le, 64)
}

// histogramQuantile computes the q-quantile from cumulative histogram buckets,
// mirroring Prometheus' bucketQuantile (PromQL histogram_quantile): find the
// bucket holding the rank, then linearly interpolate within it. buckets maps
// each le upper bound to its summed cumulative count and must include +Inf.
func histogramQuantile(q float64, buckets map[float64]float64) float64 {
	if len(buckets) == 0 {
		return 0
	}
	ubs := make([]float64, 0, len(buckets))
	for ub := range buckets {
		ubs = append(ubs, ub)
	}
	sort.Float64s(ubs) // +Inf sorts last
	total := buckets[ubs[len(ubs)-1]]
	if total == 0 {
		return 0
	}
	rank := q * total
	b := 0
	for b < len(ubs) && buckets[ubs[b]] < rank {
		b++
	}
	if b == len(ubs) {
		b = len(ubs) - 1
	}
	// Quantile falls in the open-ended +Inf bucket → return the largest finite bound.
	if math.IsInf(ubs[b], 1) {
		for i := len(ubs) - 1; i >= 0; i-- {
			if !math.IsInf(ubs[i], 1) {
				return ubs[i]
			}
		}
		return 0
	}
	bucketStart, bucketEnd := 0.0, ubs[b]
	count := buckets[ubs[b]]
	if b > 0 {
		bucketStart = ubs[b-1]
		count -= buckets[ubs[b-1]]
		rank -= buckets[ubs[b-1]]
	}
	if count <= 0 {
		return bucketEnd
	}
	return bucketStart + (bucketEnd-bucketStart)*(rank/count)
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
