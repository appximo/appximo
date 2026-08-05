package fleet

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Supervisor runs one engine process per app and keeps it alive.
//
// The one reconciliation rule that matters: the supervisor restarts an app
// ONLY when its process EXITS — never because health looks bad. The engine's
// own self-restart (UI-F4-S2) re-execs with syscall.Exec: same PID, the child
// never exits, so from here a self-restart is invisible except as a ~6 s
// window where /readyz answers 503 ("draining"). Restart-on-unhealthy would
// fight that drain and kill a healthy self-restart mid-flight; restart-on-exit
// composes with it for free.
type Supervisor struct {
	mf      *Manifest
	bin     string // engine binary — the fleet's own executable
	dataDir string

	mu    sync.Mutex
	procs map[string]*appProc

	// baseBackoff is the first restart delay after a crash (doubles per
	// consecutive crash, capped at 30 s; reset after 60 s of uptime).
	// Overridable in tests.
	baseBackoff time.Duration

	// bootstrap provisions an app's database before first spawn (the
	// control-plane DDL). Overridable in tests (no real Postgres there).
	bootstrap func(ctx context.Context, dsn string) error

	ctx context.Context
}

// appProc is the supervisor's view of one running app.
type appProc struct {
	spec        AppSpec
	port        int
	controlPort int
	env         []string
	logPath     string

	mu          sync.Mutex
	cmd         *exec.Cmd
	pid         int
	startedAt   time.Time
	restarts    int
	consecutive int // consecutive crashes without a healthy 60 s uptime
	stopping    bool
	running     bool
	lastExit    string
	healthy     bool
	healthNote  string
}

// AppStatus is one app's row in the fleet status API.
type AppStatus struct {
	Name        string   `json:"name"`
	Domains     []string `json:"domains"`
	Schema      string   `json:"schema"`
	Port        int      `json:"port"`
	ControlPort int      `json:"control_port"`
	PID         int      `json:"pid"`
	Running     bool     `json:"running"`
	Healthy     bool     `json:"healthy"`
	Health      string   `json:"health"` // ready | draining_or_down | unreachable | stopped
	Restarts    int      `json:"restarts"`
	UptimeS     int64    `json:"uptime_s"`
	LastExit    string   `json:"last_exit,omitempty"`
	Log         string   `json:"log"`
}

// NewSupervisor prepares (but does not start) a supervisor for the manifest.
// bin is the engine binary to spawn — normally the fleet's own executable.
func NewSupervisor(mf *Manifest, bin string) *Supervisor {
	return &Supervisor{
		mf:          mf,
		bin:         bin,
		dataDir:     mf.DataDir,
		procs:       map[string]*appProc{},
		baseBackoff: time.Second,
		bootstrap:   BootstrapControlPlane,
	}
}

// Start allocates ports, spawns every app, and launches the health poller.
// It returns once all apps are spawned (readiness is reported via Status).
func (s *Supervisor) Start(ctx context.Context) error {
	s.ctx = ctx
	if err := os.MkdirAll(filepath.Join(s.dataDir, "logs"), 0o755); err != nil {
		return fmt.Errorf("fleet: create data dir: %w", err)
	}
	for i := range s.mf.Apps {
		spec := s.mf.Apps[i]
		port, err := resolvePort(spec.Port)
		if err != nil {
			return fmt.Errorf("fleet: app %q: data port: %w", spec.Name, err)
		}
		cport, err := resolvePort(spec.ControlPort)
		if err != nil {
			return fmt.Errorf("fleet: app %q: control port: %w", spec.Name, err)
		}
		// Provision the app's database: the control-plane tables docker-compose
		// initdb applies in the single-app deployment. Idempotent; a fleet app
		// on a FRESH database gets a working control plane without manual SQL.
		if err := s.bootstrap(ctx, spec.MergedEnv()["DATABASE_URL"]); err != nil {
			return fmt.Errorf("fleet: app %q: bootstrap database: %w", spec.Name, err)
		}
		p := &appProc{
			spec:        spec,
			port:        port,
			controlPort: cport,
			logPath:     filepath.Join(s.dataDir, "logs", spec.Name+".log"),
			env:         s.buildEnv(&spec),
		}
		s.mu.Lock()
		s.procs[spec.Name] = p
		s.mu.Unlock()
		if err := s.spawn(p); err != nil {
			return fmt.Errorf("fleet: app %q: %w", spec.Name, err)
		}
	}
	go s.healthLoop(ctx)
	return nil
}

// buildEnv merges the fleet's own environment with the app's manifest env
// (app wins), then fills per-app defaults for state the apps must NOT share:
// the obs SQLite, the local files root, the backup dir.
func (s *Supervisor) buildEnv(spec *AppSpec) []string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for k, v := range spec.MergedEnv() {
		env[k] = v
	}
	appDir := filepath.Join(s.dataDir, spec.Name)
	if env["OBS_DB_PATH"] == "" || env["OBS_DB_PATH"] == "/var/lib/appximo/obs.db" {
		env["OBS_DB_PATH"] = filepath.Join(appDir, "obs.db")
	}
	if env["APPXIMO_FILES_DIR"] == "" {
		env["APPXIMO_FILES_DIR"] = filepath.Join(appDir, "files")
	}
	if env["BACKUP_DIR"] == "" {
		env["BACKUP_DIR"] = filepath.Join(appDir, "backups")
	}
	os.MkdirAll(appDir, 0o755) //nolint:errcheck
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// spawn starts the app's engine process and its exit monitor.
func (s *Supervisor) spawn(p *appProc) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return fmt.Errorf("already running (pid %d)", p.pid)
	}
	logf, err := os.OpenFile(p.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	cmd := exec.Command(s.bin, "serve",
		"--schema", p.spec.Schema,
		"--port", strconv.Itoa(p.port),
		"--control-port", strconv.Itoa(p.controlPort))
	cmd.Env = p.env
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		logf.Close() //nolint:errcheck
		return fmt.Errorf("start engine: %w", err)
	}
	p.cmd = cmd
	p.pid = cmd.Process.Pid
	p.startedAt = time.Now()
	p.running = true
	p.stopping = false
	log.Printf("fleet: app %q up — pid %d, data :%d, control :%d, schema %s",
		p.spec.Name, p.pid, p.port, p.controlPort, p.spec.Schema)

	go func() {
		err := cmd.Wait()
		logf.Close() //nolint:errcheck
		s.onExit(p, err)
	}()
	return nil
}

// onExit implements restart-on-exit with exponential backoff. A deliberate
// stop (StopApp/Shutdown) does not restart; anything else does.
func (s *Supervisor) onExit(p *appProc, waitErr error) {
	p.mu.Lock()
	uptime := time.Since(p.startedAt)
	p.running = false
	p.pid = 0
	if waitErr != nil {
		p.lastExit = waitErr.Error()
	} else {
		p.lastExit = "exit 0"
	}
	stopped := p.stopping
	if uptime > 60*time.Second {
		p.consecutive = 0
	}
	p.consecutive++
	delay := s.baseBackoff << (p.consecutive - 1)
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	p.mu.Unlock()

	if stopped || (s.ctx != nil && s.ctx.Err() != nil) {
		log.Printf("fleet: app %q stopped (%s after %s)", p.spec.Name, p.lastExit, uptime.Round(time.Second))
		return
	}
	log.Printf("fleet: app %q EXITED (%s after %s) — restarting in %s (the others are untouched)",
		p.spec.Name, p.lastExit, uptime.Round(time.Second), delay)
	select {
	case <-time.After(delay):
	case <-s.ctx.Done():
		return
	}
	p.mu.Lock()
	p.restarts++
	p.mu.Unlock()
	if err := s.spawn(p); err != nil {
		log.Printf("fleet: app %q: respawn failed: %v", p.spec.Name, err)
		go s.onExit(p, err) // keep retrying with growing backoff
	}
}

// healthLoop polls each app's /readyz. Observability only — it NEVER restarts
// (see the reconciliation note on Supervisor): a 503 here usually means the
// app is draining for its own self-restart.
func (s *Supervisor) healthLoop(ctx context.Context) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			procs := make([]*appProc, 0, len(s.procs))
			for _, p := range s.procs {
				procs = append(procs, p)
			}
			s.mu.Unlock()
			for _, p := range procs {
				p.mu.Lock()
				running, port := p.running, p.port
				p.mu.Unlock()
				if !running {
					continue
				}
				healthy, note := false, "unreachable"
				resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", port))
				if err == nil {
					if resp.StatusCode == http.StatusOK {
						healthy, note = true, "ready"
					} else {
						note = "draining_or_down"
					}
					resp.Body.Close() //nolint:errcheck
				}
				p.mu.Lock()
				p.healthy, p.healthNote = healthy, note
				p.mu.Unlock()
			}
		}
	}
}

// StopApp gracefully stops one app (SIGTERM → engine drain; SIGKILL after
// 15 s) and marks it so onExit does not restart it.
func (s *Supervisor) StopApp(name string) error {
	p := s.get(name)
	if p == nil {
		return fmt.Errorf("fleet: unknown app %q", name)
	}
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return nil
	}
	p.stopping = true
	pid := p.pid
	p.mu.Unlock()

	if err := signalTerm(pid); err != nil {
		return fmt.Errorf("fleet: stop %q: %w", name, err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		running := p.running
		p.mu.Unlock()
		if !running {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	signalKill(pid)
	return nil
}

// StartApp starts a previously stopped app.
func (s *Supervisor) StartApp(name string) error {
	p := s.get(name)
	if p == nil {
		return fmt.Errorf("fleet: unknown app %q", name)
	}
	p.mu.Lock()
	p.stopping = false
	p.consecutive = 0
	p.mu.Unlock()
	return s.spawn(p)
}

// RestartApp is a deliberate stop+start of ONE app; the others are untouched.
func (s *Supervisor) RestartApp(name string) error {
	if err := s.StopApp(name); err != nil {
		return err
	}
	return s.StartApp(name)
}

// Shutdown stops every app in parallel (graceful, bounded by StopApp's 15 s).
func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	names := make([]string, 0, len(s.procs))
	for n := range s.procs {
		names = append(names, n)
	}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, n := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if err := s.StopApp(name); err != nil {
				log.Printf("fleet: shutdown %q: %v", name, err)
			}
		}(n)
	}
	wg.Wait()
}

// Status snapshots every app, sorted by name.
func (s *Supervisor) Status() []AppStatus {
	s.mu.Lock()
	procs := make([]*appProc, 0, len(s.procs))
	for _, p := range s.procs {
		procs = append(procs, p)
	}
	s.mu.Unlock()
	out := make([]AppStatus, 0, len(procs))
	for _, p := range procs {
		p.mu.Lock()
		st := AppStatus{
			Name:        p.spec.Name,
			Domains:     append([]string(nil), p.spec.Domains...),
			Schema:      p.spec.Schema,
			Port:        p.port,
			ControlPort: p.controlPort,
			PID:         p.pid,
			Running:     p.running,
			Healthy:     p.healthy,
			Health:      p.healthNote,
			Restarts:    p.restarts,
			LastExit:    p.lastExit,
			Log:         p.logPath,
		}
		if p.running {
			st.UptimeS = int64(time.Since(p.startedAt).Seconds())
		} else {
			st.Health = "stopped"
			st.Healthy = false
		}
		p.mu.Unlock()
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Port returns an app's internal data port (for the proxy table).
func (s *Supervisor) Port(name string) (int, bool) {
	p := s.get(name)
	if p == nil {
		return 0, false
	}
	return p.port, true
}

func (s *Supervisor) get(name string) *appProc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.procs[name]
}

// resolvePort returns the pinned port, or asks the kernel for a free one.
func resolvePort(pinned int) (int, error) {
	if pinned != 0 {
		return pinned, nil
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close() //nolint:errcheck
	return l.Addr().(*net.TCPAddr).Port, nil
}
