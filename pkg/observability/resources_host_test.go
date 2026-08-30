package observability

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type hostRecAlerter struct {
	mu   sync.Mutex
	sent []Alert
}

func (r *hostRecAlerter) Send(_ context.Context, a Alert) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, a)
	return nil
}

func (r *hostRecAlerter) wait(n int) []Alert {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		if len(r.sent) >= n {
			out := append([]Alert(nil), r.sent...)
			r.mu.Unlock()
			return out
		}
		r.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Alert(nil), r.sent...)
}

// A disk under the floor alerts ONCE per cooldown; a healthy one never.
func TestHost_DiskLowAlertsOncePerCooldown(t *testing.T) {
	dir := t.TempDir()
	rec := &hostRecAlerter{}
	now := time.Now()
	// 100 % floor: any real filesystem is "low".
	r := newHostReader(HostConfig{DiskPaths: []string{dir}, DiskMinFreePct: 100}.withDefaults(), now)
	r.alerter = rec
	var h HostStats
	r.fill(&h, now)
	if !h.Enabled || h.Count != 1 || h.Disks[0].Err != "" {
		t.Fatalf("expected one watched disk, got enabled=%v count=%d err=%q", h.Enabled, h.Count, h.Disks[0].Err)
	}
	if !h.Disks[0].Low || h.Disks[0].TotalBytes <= 0 {
		t.Fatalf("expected the disk to be low under a 100%% floor: %+v", h.Disks[0])
	}
	sent := rec.wait(1)
	if len(sent) != 1 || sent[0].Kind != KindHost || sent[0].Route != "disk" {
		t.Fatalf("expected one disk alert, got %+v", sent)
	}
	// Within the cooldown: still low, no second alert.
	r.fill(&h, now.Add(time.Hour))
	time.Sleep(20 * time.Millisecond)
	if got := rec.wait(1); len(got) != 1 {
		t.Fatalf("expected the cooldown to hold, got %d alerts", len(got))
	}
	// Past the cooldown: one more.
	r.fill(&h, now.Add(hostAlertCooldown+time.Minute))
	if got := rec.wait(2); len(got) != 2 {
		t.Fatalf("expected a second alert past the cooldown, got %d", len(got))
	}
	// A 0 %% / 0-byte floor never alerts.
	r2 := newHostReader(HostConfig{DiskPaths: []string{dir}, DiskMinFreePct: -1, DiskMinFreeBytes: -1}, now)
	rec2 := &hostRecAlerter{}
	r2.alerter = rec2
	r2.fill(&h, now)
	if h.Disks[0].Low {
		t.Fatalf("a disabled floor must never mark a disk low: %+v", h.Disks[0])
	}
}

// The backup watch: none → stale only after MaxAge of uptime; ok → fresh;
// failed → alarm at once; ok but old → stale.
func TestHost_BackupStatusStates(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	cfg := HostConfig{BackupDir: dir, BackupMaxAge: 36 * time.Hour}.withDefaults()
	r := newHostReader(cfg, now)
	rec := &hostRecAlerter{}
	r.alerter = rec
	var h HostStats

	r.fill(&h, now.Add(time.Hour))
	if h.Backup.Status != BackupNone || h.Backup.Alarm {
		t.Fatalf("fresh install, one hour up: expected none/no alarm, got %+v", h.Backup)
	}
	r.fill(&h, now.Add(40*time.Hour))
	if h.Backup.Status != BackupNone || !h.Backup.Stale || !h.Backup.Alarm {
		t.Fatalf("no backup ever after 40 h: expected stale alarm, got %+v", h.Backup)
	}
	if got := rec.wait(1); len(got) != 1 || got[0].Route != "backup" {
		t.Fatalf("expected one backup alert, got %+v", got)
	}

	status := filepath.Join(dir, backupStatusFile)
	if err := os.WriteFile(status, []byte("ok 2026-08-30T03:30:00Z app=x set=/var/backups/x/x-1 rows=10 files=0 offbox=yes seconds=7.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r2 := newHostReader(cfg, now)
	r2.fill(&h, time.Now())
	if h.Backup.Status != BackupOK || h.Backup.Stale || h.Backup.Alarm || h.Backup.LastAt == 0 {
		t.Fatalf("fresh ok status: expected ok/fresh, got %+v", h.Backup)
	}
	if h.Backup.Line == "" || h.Backup.Line[:2] != "ok" {
		t.Fatalf("expected the status line, got %q", h.Backup.Line)
	}
	// Older than the floor: stale.
	r2.fill(&h, time.Now().Add(48*time.Hour))
	if !h.Backup.Stale || !h.Backup.Alarm {
		t.Fatalf("ok status older than 36 h: expected stale, got %+v", h.Backup)
	}
	// A failed run alarms regardless of age.
	if err := os.WriteFile(status, []byte("failed 2026-08-30T03:30:00Z app=x exit=1 line=137\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec3 := &hostRecAlerter{}
	r3 := newHostReader(cfg, now)
	r3.alerter = rec3
	r3.fill(&h, time.Now())
	if h.Backup.Status != BackupFailed || !h.Backup.Alarm {
		t.Fatalf("failed status: expected alarm, got %+v", h.Backup)
	}
	if got := rec3.wait(1); len(got) != 1 || got[0].Level != LevelCritical {
		t.Fatalf("expected one critical backup alert, got %+v", got)
	}
}

// An EMPTY status file (a run on a full disk) is an alarm, not "no backup".
func TestHost_BackupEmptyStatusIsAlarm(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, backupStatusFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := newHostReader(HostConfig{BackupDir: dir}.withDefaults(), time.Now())
	rec := &hostRecAlerter{}
	r.alerter = rec
	var h HostStats
	r.fill(&h, time.Now())
	if h.Backup.Status != BackupUnknown || !h.Backup.Alarm || h.Backup.LastAt == 0 {
		t.Fatalf("empty status: expected unknown+alarm with an mtime, got %+v", h.Backup)
	}
	if got := rec.wait(1); len(got) != 1 || got[0].Route != "backup" {
		t.Fatalf("expected one backup alert, got %+v", got)
	}
}

// Layer 5 is off by default: no paths, no dir → Enabled=false, nothing read.
func TestHost_OffByDefault(t *testing.T) {
	r := newHostReader(HostConfig{}.withDefaults(), time.Now())
	var h HostStats
	r.fill(&h, time.Now())
	if h.Enabled || h.Count != 0 || h.Backup.Dir != "" {
		t.Fatalf("expected the layer off, got %+v", h)
	}
}

// The cardinal principle holds with layer 5 on: a tick still allocates only
// the published copy (statfs + the raw status-file read are alloc-free).
func TestResourceCollector_TickAllocatesNothing_WithHost(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, backupStatusFile), []byte("ok 2026-08-30T03:30:00Z app=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewResourceCollector(ResourceConfig{Host: HostConfig{DiskPaths: []string{dir, "/"}, BackupDir: dir}})
	c.SetDB(func() PoolStat { return PoolStat{MaxConns: 10, AcquiredConns: 1, IdleConns: 9} }, nil)
	c.rt.read()
	c.proc.read()
	c.db.readClient()
	now := time.Now()
	for i := 0; i < 3; i++ {
		now = now.Add(time.Second)
		c.tick(now)
	}
	allocs := testing.AllocsPerRun(20, func() {
		now = now.Add(time.Second)
		c.tick(now)
	})
	if allocs > 1 {
		t.Fatalf("tick with layer 5 allocates %.1f objects/tick; budget is 1 (the published copy)", allocs)
	}
	if s := c.Latest(); !s.Host.Enabled || s.Host.Count < 1 || s.Host.Backup.Status != BackupOK {
		t.Fatalf("expected layer 5 in the snapshot, got %+v", s.Host)
	}
}
