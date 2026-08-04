package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/appximo/appximo/tools/devhub/sshx"
)

// Remote live metrics (S47). The background 5s scraper + 1h ring stays
// local-only; remote servers are scraped ON DEMAND through an SSH tunnel,
// only while at least one SSE client is watching them: the scrape loop starts
// with the first subscriber of a server and stops (closing the tunnel) when
// the last one disconnects. Nobody watching → zero remote traffic.

const remoteRingSize = 720 // same 1h@5s shape as the local ring

type remoteScrape struct {
	mu      sync.Mutex
	subs    int
	samples []MetricSample // rolling window, oldest first
	stop    chan struct{}
}

var (
	remoteMu      sync.Mutex
	remoteScrapes = map[int64]*remoteScrape{}
)

// subscribeRemote registers an SSE watcher for a server, starting the scrape
// loop if it is the first one. Returns the scrape state and an unsubscribe fn.
func subscribeRemote(s *RegisteredServer) (*remoteScrape, func()) {
	remoteMu.Lock()
	rs, ok := remoteScrapes[s.ID]
	if !ok {
		rs = &remoteScrape{stop: make(chan struct{})}
		remoteScrapes[s.ID] = rs
		go remoteLoop(s, rs)
	}
	rs.mu.Lock()
	rs.subs++
	rs.mu.Unlock()
	remoteMu.Unlock()

	unsubscribe := func() {
		remoteMu.Lock()
		defer remoteMu.Unlock()
		rs.mu.Lock()
		rs.subs--
		last := rs.subs <= 0
		rs.mu.Unlock()
		if last {
			close(rs.stop)
			delete(remoteScrapes, s.ID)
		}
	}
	return rs, unsubscribe
}

// remoteLoop scrapes the server's /metrics through one long-lived tunnel every
// 5s until the last subscriber leaves. The admin key comes from the devhub
// process env var named by admin_key_env — never from SQLite, never logged.
func remoteLoop(s *RegisteredServer, rs *remoteScrape) {
	addr, closer, err := sshx.ForwardAddr(&s.Server, s.EnginePort)
	if err != nil {
		rs.push(MetricSample{TS: time.Now().Unix(), MotorUp: false})
		return
	}
	if closer != nil {
		defer closer.Close() //nolint:errcheck
	}
	client := &http.Client{Timeout: 4 * time.Second}
	// Resolved ONCE per scrape session (not per 5s tick), so the audit trail
	// gets one 'metrics_scrape' entry per watch session.
	adminKey := adminKeyFor(s, "metrics_scrape")

	scrapeOnce := func() {
		sample := MetricSample{TS: time.Now().Unix()}
		req, _ := http.NewRequest("GET", "http://"+addr+"/metrics", nil)
		if adminKey != "" {
			req.Header.Set("X-Admin-Key", adminKey)
		}
		resp, err := client.Do(req)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close() //nolint:errcheck
			if resp.StatusCode == http.StatusOK {
				sample = parseMetrics(string(body))
				sample.TS = time.Now().Unix()
			}
			// Non-200 (e.g. 401 admin-gated without key): the engine answered,
			// so the motor is up even though the charts will be empty.
			sample.MotorUp = true
		}
		rs.push(sample)
	}

	scrapeOnce()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-rs.stop:
			return
		case <-ticker.C:
			scrapeOnce()
		}
	}
}

func (rs *remoteScrape) push(s MetricSample) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.samples = append(rs.samples, s)
	if len(rs.samples) > remoteRingSize {
		rs.samples = rs.samples[len(rs.samples)-remoteRingSize:]
	}
}

func (rs *remoteScrape) snapshot() []MetricSample {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	out := make([]MetricSample, len(rs.samples))
	copy(out, rs.samples)
	return out
}

// remoteMetricsLive serves the SSE stream for one remote server. Mirrors the
// local MetricsLiveHandler loop but reads the on-demand scrape buffer.
func remoteMetricsLive(w http.ResponseWriter, r *http.Request, s *RegisteredServer) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	rs, unsubscribe := subscribeRemote(s)
	defer unsubscribe()

	send := func() {
		data, _ := json.Marshal(rs.snapshot())
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

// MetricsLiveRouter — GET /api/metrics/live?server={id}
// No server param (or a local server) → the existing local ring stream;
// a remote server id → the on-demand tunnel scrape stream.
func MetricsLiveRouter(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("server")
	if q == "" {
		MetricsLiveHandler(w, r)
		return
	}
	id, err := strconv.ParseInt(q, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid server id")
		return
	}
	s, err := LoadServer(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if s.Local() {
		MetricsLiveHandler(w, r) // the background scraper already covers this box
		return
	}
	remoteMetricsLive(w, r, s)
}
