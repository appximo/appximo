package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "up":
		cmdUp(args)
	case "sweep":
		cmdSweep(args)
	case "soak":
		cmdSoak(args)
	case "report":
		cmdReport(args)
	case "down":
		cmdDown(args)
	case "reap":
		cmdReap(args)
	case "status":
		cmdStatus(args)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `lab — the ephemeral capacity laboratory (guarded; DRY-RUN by default)

  lab up      [-apply]                       bring the topology up (generator + 2 targets, VPC, install.sh, seed)
  lab sweep   [-apply] [-target basic|dedic|both] [-rates …]   the ladder (default: the authoritative CAPACIDAD-USL-S1 ladder + 420×5)
  lab soak    [-apply] -hours N [-rate R]    the endurance run, detached on the generator
  lab report  -in results.jsonl [-baseline old.jsonl]          conditions + USL fit + overlay + cost
  lab down    [-apply]                       destroy EVERYTHING applab; verifies by re-listing; idempotent
  lab reap    [-apply] [-max-age 6h]         destroy applab droplets older than max-age (the backstop; run at session close)
  lab status                                 what exists (API truth) + local state

Every mutating command is a DRY-RUN unless -apply is passed. The token is read
from `+TokenPath+` at runtime and is never printed. Only droplets carrying BOTH
the "applab-" name prefix AND the "applab" tag are ever touched; the cap is 4.
`)
	os.Exit(2)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "lab:", err)
	os.Exit(1)
}

type common struct {
	apply     bool
	region    string
	tokenFile string
}

func bindCommon(fs *flag.FlagSet) *common {
	var c common
	fs.BoolVar(&c.apply, "apply", false, "actually call the API (default: dry-run, print the plan)")
	fs.StringVar(&c.region, "region", "nyc3", "DigitalOcean region — ALL lab boxes share it (and its default VPC)")
	fs.StringVar(&c.tokenFile, "token-file", TokenPath, "path to the scoped lab token")
	return &c
}

func (c *common) client() *Client {
	if n := loadProtectedIPs(); n == 0 {
		fmt.Fprintf(os.Stderr, "lab: WARNING: the protected-IP belt is EMPTY — put the production boxes' addresses in %s (one per line) or APPLAB_PROTECTED_IPS. The name+tag guard still applies.\n", ProtectedIPsFile)
	}
	tok, err := LoadToken(c.tokenFile)
	if err != nil {
		fatal(err)
	}
	return NewClient(tok, c.apply, os.Stdout)
}

// ── up ─────────────────────────────────────────────────────────────────────

func cmdUp(args []string) {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	c := bindCommon(fs)
	dataset := fs.String("dataset", "large", "dataset size: small | large (dataset/README.md)")
	keep := fs.Bool("keep-on-failure", false, "do NOT tear the lab down when a provisioning step fails")
	_ = fs.Parse(args)
	if _, err := datasetVars(*dataset); err != nil {
		fatal(err)
	}
	cl := c.client()
	ctx := context.Background()
	st, err := loadState()
	if err != nil {
		fatal(err)
	}
	st.Region = c.region
	st.Dataset = *dataset

	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	if !c.apply {
		fmt.Println("DRY-RUN plan (pass -apply to execute):")
		fmt.Printf("  1. default VPC of %s (vpc:read) — without the VPC you measure the internet\n", c.region)
		for _, spec := range topology {
			fmt.Printf("  2. droplet %-22s size=%-12s image=%s tag=%s\n", spec.name, spec.size, baseImage, LabTag)
		}
		fmt.Println("  3. targets: scripts/install.sh (the customer path) → capacity arm → tenant → seed(" + *dataset + ") → token → smoke")
		fmt.Println("  4. generator: tools/capacity binary")
		fmt.Println("  5. VPC base latency (ping over the private link), snapshot attempt, state saved")
		// The guarded client still exercises create in dry-run mode:
		for _, spec := range topology {
			if _, err := cl.Create(ctx, CreateRequest{Name: spec.name, Size: spec.size, Region: c.region, Image: baseImage}); err != nil {
				fatal(err)
			}
		}
		return
	}

	engineBin, capacityBin, version, err := buildArtifacts(root)
	if err != nil {
		fatal(err)
	}
	st.EngineVersion = version
	fmt.Println("built engine + capacity binaries; engine:", version)

	vpc, vpcRange, err := cl.DefaultVPC(ctx, c.region)
	if err != nil {
		fatal(err)
	}
	st.VPCUUID, st.VPCRange = vpc, vpcRange
	keys, err := cl.SSHKeyIDs(ctx)
	if err != nil {
		fatal(err)
	}
	ud := userData()

	// Rollback on failure: a half-provisioned laboratory must not stay alive.
	ok := false
	defer func() {
		if ok || *keep {
			return
		}
		fmt.Println("up FAILED — tearing the laboratory down (pass -keep-on-failure to debug in place)")
		if _, derr := downAll(ctx, cl, 3, 5*time.Second); derr != nil {
			fmt.Fprintln(os.Stderr, "rollback:", derr)
		}
	}()

	image := baseImage
	if st.SnapshotImage != 0 {
		image = fmt.Sprintf("%d", st.SnapshotImage)
		fmt.Println("using target snapshot image", image)
	}
	nodes := map[string]NodeState{}
	for _, spec := range topology {
		img := baseImage
		if spec.target && st.SnapshotImage != 0 {
			img = image
		}
		n, err := ensureNode(ctx, cl, st, spec, img, vpc, keys, ud)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("  %-22s id=%d public=%s private=%s\n", n.Name, n.ID, n.PublicIP, n.PrivateIP)
		nodes[spec.role] = n
	}

	for _, spec := range topology {
		n := nodes[spec.role]
		if spec.target {
			fresh := st.SnapshotImage == 0
			if fresh {
				fmt.Println("provisioning", n.Name, "through install.sh (the customer path)…")
				if err := provisionTarget(root, &n, engineBin, *dataset); err != nil {
					fatal(err)
				}
			} else {
				fmt.Println(n.Name, "from snapshot — verifying the service instead of reinstalling…")
				if err := provisionTarget(root, &n, engineBin, *dataset); err != nil {
					fatal(err) // install.sh is idempotent; re-run verifies installed==asked
				}
			}
		} else {
			if err := provisionGen(&n, capacityBin); err != nil {
				fatal(err)
			}
		}
		nodes[spec.role] = n
		st.Nodes[spec.role] = n
		if err := st.save(); err != nil {
			fatal(err)
		}
	}

	gen := nodes["gen"]
	st.VPCLatencyMs = map[string]float64{}
	for _, role := range []string{"target-basic", "target-dedic"} {
		if t, okr := nodes[role]; okr {
			if ms, err := vpcLatency(gen.PublicIP, t.PrivateIP); err == nil {
				st.VPCLatencyMs[role] = ms
				fmt.Printf("  VPC base latency gen→%s: %.3f ms\n", role, ms)
			} else {
				fmt.Printf("  VPC latency gen→%s: %v\n", role, err)
			}
		}
	}

	// Snapshot the basic target so the next `up` skips the full install. The
	// scoped token may not cover droplet actions — degrade loudly, not fatally.
	if st.SnapshotImage == 0 {
		if t, okr := nodes["target-basic"]; okr {
			live, gerr := cl.Get(ctx, t.ID)
			if gerr == nil {
				if err := cl.Snapshot(ctx, live, NamePrefix+"target-snap"); err != nil {
					fmt.Println("  snapshot not taken (scope? action refused):", err)
					fmt.Println("  → every `up` will provision from scratch via install.sh (which also re-exercises the installer)")
				} else if snaps, serr := cl.DropletSnapshots(ctx, t.ID); serr == nil && len(snaps) > 0 {
					st.SnapshotImage = snaps[len(snaps)-1]
				}
			}
		}
	}
	if err := st.save(); err != nil {
		fatal(err)
	}
	ok = true
	fmt.Println("laboratory is UP. Next: lab sweep -apply · lab report · lab down -apply")
}

// ── sweep ──────────────────────────────────────────────────────────────────

func cmdSweep(args []string) {
	fs := flag.NewFlagSet("sweep", flag.ExitOnError)
	c := bindCommon(fs)
	target := fs.String("target", "both", "basic | dedic | both — which target box to sweep")
	rates := fs.String("rates", "", "custom comma-separated rates (default: the authoritative ladder + the 420×5 bistability probe)")
	repeats := fs.Int("repeats", 3, "repeats per level for -rates")
	name := fs.String("name", "read", "workload label")
	_ = fs.Parse(args)
	st, err := loadState()
	if err != nil {
		fatal(err)
	}
	gen, okg := st.Nodes["gen"]
	if !okg || gen.PublicIP == "" {
		fatal(fmt.Errorf("no generator in state — run `lab up -apply` first"))
	}
	var targets []string
	switch *target {
	case "both":
		targets = []string{"target-basic", "target-dedic"}
	case "basic":
		targets = []string{"target-basic"}
	case "dedic":
		targets = []string{"target-dedic"}
	default:
		fatal(fmt.Errorf("-target %q: basic | dedic | both", *target))
	}
	if !c.apply {
		fmt.Println("DRY-RUN: would run on the generator", gen.PublicIP, "against", strings.Join(targets, ", "))
		if *rates != "" {
			fmt.Printf("  custom ladder: %s ×%d (workload %s)\n", *rates, *repeats, *name)
		} else {
			for _, l := range ladder {
				fmt.Printf("  ladder: %s ×%d\n", l.rates, l.repeats)
			}
			fmt.Printf("  bistability probe: %s ×%d\n", bistableProbe.rates, bistableProbe.repeats)
		}
		return
	}
	for _, role := range targets {
		t, okt := st.Nodes[role]
		if !okt || t.Token == "" {
			fatal(fmt.Errorf("target %s not provisioned — run `lab up -apply`", role))
		}
		fmt.Printf("=== sweep against %s (%s, %s) ===\n", t.Name, t.Size, t.PrivateIP)
		var inner []string
		suffix := strings.TrimPrefix(role, "target-")
		if *rates != "" {
			inner = append(inner, capacityCmd(t, *name, readPath, *rates, *repeats, fmt.Sprintf("%s-%s.jsonl", *name, suffix)))
		} else {
			for _, l := range ladder {
				inner = append(inner, capacityCmd(t, *name, readPath, l.rates, l.repeats, strings.Replace(l.file, ".jsonl", "-"+suffix+".jsonl", 1)))
			}
			inner = append(inner, capacityCmd(t, *name, readPath, bistableProbe.rates, bistableProbe.repeats, strings.Replace(bistableProbe.file, ".jsonl", "-"+suffix+".jsonl", 1)))
		}
		tag := "sweep-" + suffix
		if err := runDetached(gen.PublicIP, strings.Join(inner, " && "), tag, 30*time.Second, 4*time.Hour); err != nil {
			fatal(err)
		}
	}
	dir, err := fetchResults(gen.PublicIP, "sweep")
	if err != nil {
		fatal(err)
	}
	fmt.Println("results in", dir)
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	for _, f := range files {
		rows, err := readRows(f)
		if err != nil {
			fmt.Println("  ", f, ":", err)
			continue
		}
		if bad := checkGenerator(rows); len(bad) > 0 {
			fmt.Printf("  ⚠ %s: %d run(s) INVALID — the generator itself was saturated: %s\n",
				filepath.Base(f), len(bad), strings.Join(bad, "; "))
		}
	}
}

// ── soak ───────────────────────────────────────────────────────────────────

func cmdSoak(args []string) {
	fs := flag.NewFlagSet("soak", flag.ExitOnError)
	c := bindCommon(fs)
	hours := fs.Float64("hours", 4, "duration of the endurance run")
	rate := fs.Float64("rate", 265, "sustained rps (default: the 70 % planning point of the previous fit)")
	target := fs.String("target", "basic", "basic | dedic")
	_ = fs.Parse(args)
	st, err := loadState()
	if err != nil {
		fatal(err)
	}
	gen, okg := st.Nodes["gen"]
	t, okt := st.Nodes["target-"+*target]
	if !okg || !okt || t.Token == "" {
		fatal(fmt.Errorf("laboratory not up — run `lab up -apply` first"))
	}
	inner := fmt.Sprintf(`./capacity soak -url http://%s:%d -host %s.%s.internal -token %q -admin-key %q `+
		`-span 1000000 -timeout 5s -patience 5s -rate %g -duration %s -out results/soak.jsonl`,
		t.PrivateIP, t.Port, labTenant, t.Name, t.Token, t.AdminKey, *rate, fmt.Sprintf("%dm", int(*hours*60)))
	if !c.apply {
		fmt.Printf("DRY-RUN: would soak %s at %g rps for %.1f h (detached on the generator)\n", t.Name, *rate, *hours)
		return
	}
	if err := runDetached(gen.PublicIP, inner, "soak", time.Minute, time.Duration(*hours*float64(time.Hour))+30*time.Minute); err != nil {
		fatal(err)
	}
	if dir, err := fetchResults(gen.PublicIP, "soak"); err == nil {
		fmt.Println("soak results in", dir)
	}
}

// ── report ─────────────────────────────────────────────────────────────────

func cmdReport(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	in := fs.String("in", "", "sweep JSONL to fit (required)")
	baseline := fs.String("baseline", "", "previous sweep JSONL to overlay, point by point (the deliverable is the comparison)")
	think := fs.String("think", "30s,5s,0s", "think-time profiles for the user translation")
	bootstrap := fs.Int("bootstrap", 2000, "bootstrap replicates for the N_max interval")
	probeRate := fs.Float64("bistable-at", 420, "level whose per-run spread is judged for bistability")
	_ = fs.Parse(args)
	if *in == "" {
		fatal(fmt.Errorf("-in is required"))
	}
	st, _ := loadState()

	// Conditions block — a number without its conditions cannot be compared.
	fmt.Println("## Conditions")
	fmt.Println()
	fmt.Println("| condition | value |")
	fmt.Println("|---|---|")
	fmt.Printf("| date (UTC) | %s |\n", time.Now().UTC().Format("2006-01-02 15:04"))
	fmt.Printf("| region / VPC | %s / %s |\n", st.Region, st.VPCRange)
	for _, role := range []string{"gen", "target-basic", "target-dedic"} {
		if n, ok := st.Nodes[role]; ok {
			fmt.Printf("| %s | %s (%s) |\n", role, n.Name, n.Size)
		}
	}
	for role, ms := range st.VPCLatencyMs {
		fmt.Printf("| VPC base RTT gen→%s | %.3f ms |\n", role, ms)
	}
	fmt.Printf("| engine | %s |\n", st.EngineVersion)
	fmt.Printf("| dataset | %s (deterministic seed, dataset/README.md) |\n", st.Dataset)
	fmt.Printf("| endpoint | `%s` |\n", readPath)
	fmt.Printf("| generator saturation gate | busy ≤ %.0f%% or the run is invalid |\n", genBusyThreshold*100)
	fmt.Println()

	rows, err := readRows(*in)
	if err != nil {
		fatal(err)
	}
	if bad := checkGenerator(rows); len(bad) > 0 {
		fmt.Printf("⚠ %d run(s) invalid (generator saturated): %s\n\n", len(bad), strings.Join(bad, "; "))
	}
	fmt.Println("### Bistability probe")
	fmt.Println()
	fmt.Println(bistabilityVerdict(rows, *probeRate))
	fmt.Println()

	if *baseline != "" {
		base, err := readRows(*baseline)
		if err != nil {
			fatal(err)
		}
		fmt.Println("### The two curves, point by point (old = same-box baseline, new = isolated lab)")
		fmt.Println()
		fmt.Print(compareCurves(base, rows))
		fmt.Println()
	}

	// The USL fit is the capacity tool's own — one authority, not a second fit.
	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	capBin := filepath.Join(artifactDir(), "capacity-local")
	if out, err := sh("go", "build", "-C", root, "-o", capBin, "./tools/capacity"); err != nil {
		fatal(fmt.Errorf("build capacity: %w\n%s", err, out))
	}
	out, err := sh(capBin, "fit", "-in", *in, "-think", *think, "-bootstrap", fmt.Sprint(*bootstrap))
	if err != nil {
		fatal(fmt.Errorf("capacity fit: %w\n%s", err, out))
	}
	fmt.Println(out)

	// Cost, from the actual uptime of the actual boxes (list prices).
	if len(st.Nodes) > 0 {
		fmt.Println("### Cost of this laboratory, so far (DO list prices)")
		fmt.Println()
		total := 0.0
		for _, n := range st.Nodes {
			if n.CreatedAt.IsZero() {
				continue
			}
			h := time.Since(n.CreatedAt).Hours()
			cost := h * hourlyUSD[n.Size]
			total += cost
			fmt.Printf("  %-22s %-12s %5.2f h × $%.5f/h = $%.3f\n", n.Name, n.Size, h, hourlyUSD[n.Size], cost)
		}
		fmt.Printf("  TOTAL ≈ $%.2f\n", total)
	}
}

// ── down / reap / status ───────────────────────────────────────────────────

func cmdDown(args []string) {
	fs := flag.NewFlagSet("down", flag.ExitOnError)
	c := bindCommon(fs)
	_ = fs.Parse(args)
	cl := c.client()
	survivors, err := downAll(context.Background(), cl, 3, 5*time.Second)
	if err != nil {
		fatal(err)
	}
	if len(survivors) == 0 && c.apply {
		fmt.Println("verified: zero applab droplets remain")
		// The droplets are gone; the coordinates and minted secrets in the
		// state file are dead — drop them (VPC/snapshot/dataset notes stay).
		if st, serr := loadState(); serr == nil && len(st.Nodes) > 0 {
			st.Nodes = map[string]NodeState{}
			st.VPCLatencyMs = nil
			if serr := st.save(); serr != nil {
				fmt.Fprintln(os.Stderr, "state cleanup:", serr)
			}
		}
	}
}

func cmdReap(args []string) {
	fs := flag.NewFlagSet("reap", flag.ExitOnError)
	c := bindCommon(fs)
	maxAge := fs.Duration("max-age", 6*time.Hour, "destroy applab droplets older than this")
	_ = fs.Parse(args)
	cl := c.client()
	if _, err := reap(context.Background(), cl, *maxAge); err != nil {
		fatal(err)
	}
	// The session-close proof: the listing, printed.
	ds, err := cl.ListLab(context.Background())
	if err != nil {
		fatal(err)
	}
	if len(ds) == 0 {
		fmt.Println("listing after reap: zero applab droplets")
		return
	}
	for _, d := range ds {
		fmt.Printf("  still alive: %-22s id=%d age=%s\n", d.Name, d.ID, time.Since(d.CreatedAt).Round(time.Minute))
	}
}

func cmdStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	c := bindCommon(fs)
	_ = fs.Parse(args)
	cl := c.client()
	ds, err := cl.ListLab(context.Background())
	if err != nil {
		fatal(err)
	}
	if len(ds) == 0 {
		fmt.Println("no applab droplets exist")
	}
	for _, d := range ds {
		pub := strings.Join(d.PublicIPs, ",")
		fmt.Printf("  %-22s id=%-11d %-8s %-12s public=%-15s age=%s\n",
			d.Name, d.ID, d.Status, d.SizeSlug, pub, time.Since(d.CreatedAt).Round(time.Minute))
	}
	if st, err := loadState(); err == nil && len(st.Nodes) > 0 {
		fmt.Println("local state:", statePath())
		for role, n := range st.Nodes {
			fmt.Printf("  %-14s → %s (%s)\n", role, n.Name, n.PublicIP)
		}
	}
}
