package main

// The measurement commands. The generator box runs `tools/capacity` (the
// instrument is unchanged — it is already remote-capable: the Module C verdict
// travels over HTTP via -admin-key, and the CPU accounting it takes from
// /proc is the GENERATOR box's own, which is exactly the saturation check
// this separation exists for). The orchestrator (the 105) starts runs
// DETACHED under nohup and polls a done-marker, so an SSH hiccup never kills
// an hour-long ladder.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// readPath is the canonical workload — the list an admin panel actually sends
// (?fields= + filter + sort) with a cache buster, byte-identical to the
// CAPACIDAD-USL-S1 baseline so the curves compare point by point.
const readPath = `/api/productos?per_page=20&fields=id,nombre,precio_centavos,estado&filter[estado][eq]=activo&sort=created_at&order=desc&cb={n}`

// The authoritative ladder (CAPACIDAD-USL-S1 sweep2.sh): the linear region ×3,
// the knee and past it ×4 — denser where the system proved bistable.
var ladder = []struct {
	rates   string
	repeats int
	file    string
}{
	{"25,50,100,150,200,250,300,350", 3, "sweep-a.jsonl"},
	{"380,420,450,500,550,700", 4, "sweep-b.jsonl"},
}

// bistableProbe is Parte D's concrete question: does the 420 rps bistability
// survive moving the generator off the box? Five repeats at exactly 420.
var bistableProbe = struct {
	rates   string
	repeats int
	file    string
}{"420", 5, "sweep-420x5.jsonl"}

// genBusyThreshold invalidates a run when the GENERATOR box itself was too
// busy: past this the instrument is part of the measurement again.
const genBusyThreshold = 0.70

func capacityCmd(t NodeState, name, path, rates string, repeats int, out string) string {
	return fmt.Sprintf(`./capacity sweep -url http://%s:%d -host %s.%s.internal -token %q -admin-key %q `+
		`-span 1000000 -timeout 5s -patience 5s -max-inflight 600 `+
		`-name %s -path %q -rates %s -repeats %d -duration 40s -warmup 10s -rest 12s -out results/%s`,
		t.PrivateIP, t.Port, labTenant, t.Name, t.Token, t.AdminKey, name, path, rates, repeats, out)
}

// runDetached starts a script on the generator under nohup and waits for its
// done-marker, relaying log tail lines. Robust to SSH interruptions.
func runDetached(genIP, inner, tag string, poll time.Duration, timeout time.Duration) error {
	start := fmt.Sprintf(`cd /root/lab
rm -f results/%[1]s.done results/%[1]s.log
nohup bash -c %[2]q > results/%[1]s.log 2>&1 &
echo detached`, tag, inner+"; touch results/"+tag+".done")
	if _, err := sshRun(genIP, start); err != nil {
		return fmt.Errorf("start %s: %w", tag, err)
	}
	deadline := time.Now().Add(timeout)
	lastLen := 0
	for {
		time.Sleep(poll)
		out, err := sshRun(genIP, fmt.Sprintf(`cd /root/lab; cat results/%[1]s.log 2>/dev/null; test -f results/%[1]s.done && echo __LAB_DONE__`, tag))
		if err != nil {
			fmt.Printf("  (poll failed, retrying: %v)\n", err)
			continue
		}
		done := strings.Contains(out, "__LAB_DONE__")
		body := strings.ReplaceAll(out, "__LAB_DONE__", "")
		if len(body) > lastLen {
			fmt.Print(body[lastLen:])
			lastLen = len(body)
		}
		if done {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s: no done-marker after %s — inspect results/%s.log on the generator", tag, timeout, tag)
		}
	}
}

// sweepRow is the slice of a capacity JSONL row the orchestrator inspects.
type sweepRow struct {
	Label      string  `json:"label"`
	Offered    float64 `json:"offered_rps"`
	Achieved   float64 `json:"achieved_rps"`
	Goodput    float64 `json:"goodput_rps"`
	ResponseMS struct {
		P50 float64 `json:"p50"`
		P99 float64 `json:"p99"`
	} `json:"response_ms"`
	CPU struct {
		IdleShare  float64 `json:"idle_share"`
		StealShare float64 `json:"steal_share"`
	} `json:"cpu"`
}

func readRows(path string) ([]sweepRow, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var rows []sweepRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r sweepRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		rows = append(rows, r)
	}
	return rows, sc.Err()
}

// checkGenerator verifies the instrument was not saturated: on the generator
// box, busy = 1 − idle. Every run past the threshold is INVALID and named.
func checkGenerator(rows []sweepRow) (invalid []string) {
	for _, r := range rows {
		if busy := 1 - r.CPU.IdleShare; busy > genBusyThreshold {
			invalid = append(invalid, fmt.Sprintf("%s (generator busy %.0f%% > %.0f%%)", r.Label, busy*100, genBusyThreshold*100))
		}
	}
	return invalid
}

// fetchResults pulls the generator's results directory into a dated local dir.
func fetchResults(genIP, sub string) (string, error) {
	dir := filepath.Join(resultsDir(), time.Now().UTC().Format("2006-01-02"), sub)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	names, err := sshRun(genIP, "ls /root/lab/results")
	if err != nil {
		return "", err
	}
	for _, name := range strings.Fields(names) {
		if strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".log") {
			if err := scpFrom(genIP, "/root/lab/results/"+name, filepath.Join(dir, name)); err != nil {
				return "", err
			}
		}
	}
	return dir, nil
}

// ── comparison (Parte D): old curve vs new, point by point ─────────────────

type levelAgg struct {
	offered     float64
	n           int
	x, p50, p99 float64 // means
}

func aggregate(rows []sweepRow) []levelAgg {
	byRate := map[float64]*levelAgg{}
	var order []float64
	for _, r := range rows {
		a, ok := byRate[r.Offered]
		if !ok {
			a = &levelAgg{offered: r.Offered}
			byRate[r.Offered] = a
			order = append(order, r.Offered)
		}
		a.n++
		a.x += r.Achieved
		a.p50 += r.ResponseMS.P50
		a.p99 += r.ResponseMS.P99
	}
	var out []levelAgg
	for _, rate := range order {
		a := byRate[rate]
		a.x /= float64(a.n)
		a.p50 /= float64(a.n)
		a.p99 /= float64(a.n)
		out = append(out, *a)
	}
	for i := range out { // insertion sort by offered (few levels)
		for j := i; j > 0 && out[j-1].offered > out[j].offered; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// compareCurves prints the overlay table: the baseline (same-box) sweep
// against the clean one, per offered level. THIS is the deliverable that says
// how much of the previous measurement was the instrument.
func compareCurves(baseline, clean []sweepRow) string {
	b := aggregate(baseline)
	c := aggregate(clean)
	byRate := map[float64]levelAgg{}
	for _, a := range c {
		byRate[a.offered] = a
	}
	var sb strings.Builder
	sb.WriteString("| offered | X̄ old | X̄ new | p50 old ms | p50 new ms | p99 old ms | p99 new ms |\n")
	sb.WriteString("|--:|--:|--:|--:|--:|--:|--:|\n")
	for _, a := range b {
		n, ok := byRate[a.offered]
		if !ok {
			fmt.Fprintf(&sb, "| %.0f | %.1f | — | %.1f | — | %.1f | — |\n", a.offered, a.x, a.p50, a.p99)
			continue
		}
		fmt.Fprintf(&sb, "| %.0f | %.1f | %.1f | %.1f | %.1f | %.1f | %.1f |\n",
			a.offered, a.x, n.x, a.p50, n.p50, a.p99, n.p99)
	}
	return sb.String()
}

// bistabilityVerdict inspects the per-run p50 spread at one level.
func bistabilityVerdict(rows []sweepRow, rate float64) string {
	var p50s []float64
	for _, r := range rows {
		if r.Offered == rate {
			p50s = append(p50s, r.ResponseMS.P50)
		}
	}
	if len(p50s) == 0 {
		return fmt.Sprintf("no runs at %.0f rps in this file", rate)
	}
	lo, hi := p50s[0], p50s[0]
	slow := 0
	for _, p := range p50s {
		if p < lo {
			lo = p
		}
		if p > hi {
			hi = p
		}
		if p > 100 { // an order of magnitude past the fast mode's ~2-3 ms
			slow++
		}
	}
	if slow > 0 && lo < 100 {
		return fmt.Sprintf("BISTABLE at %.0f rps: %d/%d runs tipped into the slow mode (p50 %.1f–%.1f ms) — it is the app; ENG-52 stands", rate, slow, len(p50s), lo, hi)
	}
	if slow == len(p50s) {
		return fmt.Sprintf("all %d runs at %.0f rps in the slow mode (p50 ≥ %.1f ms) — past the ceiling, not bistable", len(p50s), rate, lo)
	}
	return fmt.Sprintf("no tip at %.0f rps across %d runs (p50 %.1f–%.1f ms) — the same-box bistability does not reproduce here; it was generator contention", rate, len(p50s), lo, hi)
}
