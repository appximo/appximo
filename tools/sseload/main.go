// sseload opens N concurrent SSE connections against an Appximo events
// endpoint and holds them for a duration, counting received events and
// heartbeats and reporting dropped connections. Built for the S45 benchmarks
// (H2: write latency with idle subscribers; H4: RSS under connection load).
//
// Usage:
//
//	go run ./tools/sseload -url http://localhost:8080/api/guides/events \
//	    -host acme.localhost -token "$JWT" -n 100 -duration 60s
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	var (
		urlFlag  = flag.String("url", "http://localhost:8080/api/guides/events", "events endpoint URL")
		hostFlag = flag.String("host", "acme.localhost", "Host header (tenant subdomain routing)")
		token    = flag.String("token", "", "Bearer token (required)")
		n        = flag.Int("n", 100, "number of concurrent SSE connections")
		duration = flag.Duration("duration", 60*time.Second, "how long to hold the connections")
		rampMS   = flag.Int("ramp-ms", 5, "delay between connection starts (ms) — avoids a thundering-herd connect")
	)
	flag.Parse()
	if *token == "" {
		fmt.Fprintln(os.Stderr, "ERROR: -token is required")
		os.Exit(2)
	}

	// One shared transport; each SSE stream pins one TCP connection, so allow n.
	transport := &http.Transport{
		MaxIdleConns:        *n + 8,
		MaxConnsPerHost:     0, // unlimited — every stream needs its own conn
		MaxIdleConnsPerHost: *n + 8,
		DisableCompression:  true,
	}
	client := &http.Client{Transport: transport} // no client timeout: streams are long-lived

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	var (
		connected  atomic.Int64 // streams that got the ": connected" preamble
		dropped    atomic.Int64 // streams that ended before the deadline
		events     atomic.Int64 // "event:" lines received (data events)
		heartbeats atomic.Int64 // ": ping" comment lines received
		badStatus  atomic.Int64 // non-200 responses
	)

	var wg sync.WaitGroup
	for i := 0; i < *n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, *urlFlag, nil)
			if err != nil {
				dropped.Add(1)
				return
			}
			req.Host = *hostFlag
			req.Header.Set("Authorization", "Bearer "+*token)
			req.Header.Set("Accept", "text/event-stream")

			resp, err := client.Do(req)
			if err != nil {
				dropped.Add(1)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				badStatus.Add(1)
				return
			}
			connected.Add(1)

			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(make([]byte, 0, 16*1024), 4*1024*1024)
			for sc.Scan() {
				line := sc.Text()
				switch {
				case strings.HasPrefix(line, "event:"):
					events.Add(1)
				case strings.HasPrefix(line, ": ping"):
					heartbeats.Add(1)
				}
			}
			// EOF before the deadline = server closed us (slow policy, restart, …).
			if ctx.Err() == nil {
				dropped.Add(1)
			}
		}()
		time.Sleep(time.Duration(*rampMS) * time.Millisecond)
	}

	// Periodic progress line so long holds are observable.
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fmt.Printf("[sseload] connected=%d dropped=%d events=%d heartbeats=%d\n",
					connected.Load(), dropped.Load(), events.Load(), heartbeats.Load())
			}
		}
	}()

	wg.Wait()
	fmt.Printf("[sseload] FINAL n=%d connected=%d dropped=%d bad_status=%d events=%d heartbeats=%d\n",
		*n, connected.Load(), dropped.Load(), badStatus.Load(), events.Load(), heartbeats.Load())
	if int(connected.Load()) != *n || dropped.Load() > 0 || badStatus.Load() > 0 {
		os.Exit(1)
	}
}
