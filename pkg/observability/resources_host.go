package observability

// Layer 5 — the HOST the app depends on and that nothing in the request path
// ever looks at: the disk under the data, and whether the backup is alive
// (RESILIENCIA-S1 §D). The two silent killers of a single-box app are a disk
// that fills up and a backup that stopped running weeks ago; both are cheap
// to read once every tick (a statfs per path, one small file) and expensive
// to learn about at 3 a.m. Same discipline as the other layers: read in the
// collector goroutine, allocation-free per tick, the alert (rare) goes out
// on its own goroutine through the SAME Alerter the SLO and new-error
// alerts use — no second channel to configure.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Alert kind for host conditions (disk low, backup missing/failed).
const KindHost = "host"

// Default thresholds; zero values in HostConfig take them. 0 in the env
// disables the corresponding check ("0 = off", like the memory guard).
const (
	DefaultDiskMinFreePct   = 10.0
	DefaultDiskMinFreeBytes = int64(1024) << 20 // 1 GiB
	DefaultBackupMaxAge     = 36 * time.Hour
	hostAlertCooldown       = 6 * time.Hour
	backupStatusFile        = "last-backup.status"
)

// HostConfig configures layer 5. Empty DiskPaths and BackupDir keep the
// layer off — `appximo up` and a bare `serve` pay nothing; the installer's
// env (APPXIMO_BACKUP_DIR, APPXIMO_FILES_DIR, OBS_DB_PATH) turns it on.
type HostConfig struct {
	DiskPaths        []string      // filesystems to watch (deduplicated by fsid at read time)
	DiskMinFreePct   float64       // alert when free % is under this (0 = off)
	DiskMinFreeBytes int64         // …or free bytes are under this (0 = off)
	BackupDir        string        // where backup.sh writes last-backup.status ("" = off)
	BackupMaxAge     time.Duration // alert when the last OK backup is older than this
}

func (h HostConfig) withDefaults() HostConfig {
	if h.DiskMinFreePct == 0 && h.DiskMinFreeBytes == 0 {
		h.DiskMinFreePct, h.DiskMinFreeBytes = DefaultDiskMinFreePct, DefaultDiskMinFreeBytes
	}
	if h.DiskMinFreePct < 0 {
		h.DiskMinFreePct = 0
	}
	if h.DiskMinFreeBytes < 0 {
		h.DiskMinFreeBytes = 0
	}
	if h.BackupMaxAge <= 0 {
		h.BackupMaxAge = DefaultBackupMaxAge
	}
	return h
}

// Enabled reports whether the layer reads anything at all.
func (h HostConfig) Enabled() bool { return len(h.DiskPaths) > 0 || h.BackupDir != "" }

// DiskStat is one watched filesystem in one tick.
type DiskStat struct {
	Path       string  `json:"path"`
	TotalBytes int64   `json:"total_bytes"`
	FreeBytes  int64   `json:"free_bytes"` // available to unprivileged users (f_bavail), what the app can actually write
	FreePct    float64 `json:"free_pct"`
	Low        bool    `json:"low"`           // under the floor
	Err        string  `json:"err,omitempty"` // statfs failed (path absent, permission)
}

// Backup status words, as written by scripts/backup.sh into last-backup.status.
const (
	BackupOK      = "ok"
	BackupFailed  = "failed"
	BackupNone    = "none"    // the dir is watched but no run has ever written a status here
	BackupUnknown = "unknown" // the status file exists but says neither ok nor failed
)

// BackupStat is what the engine knows about the last backup run.
type BackupStat struct {
	Dir     string  `json:"dir"`
	Status  string  `json:"status"`         // ok | failed | none | unknown
	LastAt  int64   `json:"last_at"`        // unix seconds of the last run (the status file's mtime); 0 = never
	AgeS    float64 `json:"age_s"`          // now − LastAt
	Stale   bool    `json:"stale"`          // the last OK run is older than BackupMaxAge (or none ever, after MaxAge of uptime)
	Alarm   bool    `json:"alarm"`          // failed || stale — what the alert fires on
	MaxAgeS float64 `json:"max_age_s"`      // the floor, so the reader sees the rule
	Line    string  `json:"line,omitempty"` // the status line's first 200 bytes (allocated only when it changes)
}

// HostStats is layer 5 in one tick. Disks is a fixed array (the ring holds
// no heap); Count says how many entries are in use.
type HostStats struct {
	Enabled bool               `json:"enabled"`
	Disks   [maxDisks]DiskStat `json:"-"`
	Count   int                `json:"-"`
	Backup  BackupStat         `json:"backup"`
}

const maxDisks = 4

// MarshalJSON projects the used disk slots (the fixed array would otherwise
// print empty entries). Allocates only on read, never in the tick.
func (h HostStats) MarshalJSON() ([]byte, error) {
	type alias struct {
		Enabled bool       `json:"enabled"`
		Disks   []DiskStat `json:"disks"`
		Backup  BackupStat `json:"backup"`
	}
	return json.Marshal(alias{Enabled: h.Enabled, Disks: h.Disks[:h.Count], Backup: h.Backup})
}

// LowDisks returns the watched filesystems currently under the floor.
func (h HostStats) LowDisks() []DiskStat {
	var out []DiskStat
	for i := 0; i < h.Count; i++ {
		if h.Disks[i].Low {
			out = append(out, h.Disks[i])
		}
	}
	return out
}

// hostReader owns the per-tick state of layer 5: the paths, the status-file
// buffer, the last line seen (so the string is allocated once per change,
// not per tick), and the alert cooldowns.
type hostReader struct {
	cfg       HostConfig
	startedAt time.Time
	statusBuf []byte
	lastLine  string
	lastLineN int
	// alert state
	alerter         Alerter
	lastDiskAlert   time.Time
	lastBackupAlert time.Time
	fsids           [maxDisks][2]int32
	// NUL-terminated paths, prepared once: the raw syscalls take them
	// without converting (and allocating) per tick.
	pathNUL       [][]byte
	statusPathNUL []byte
}

func newHostReader(cfg HostConfig, now time.Time) *hostReader {
	if len(cfg.DiskPaths) > maxDisks {
		cfg.DiskPaths = cfg.DiskPaths[:maxDisks]
	}
	r := &hostReader{cfg: cfg, startedAt: now, statusBuf: make([]byte, 512)}
	for _, p := range cfg.DiskPaths {
		r.pathNUL = append(r.pathNUL, nulPath(p))
	}
	if cfg.BackupDir != "" {
		r.statusPathNUL = nulPath(cfg.BackupDir + "/" + backupStatusFile)
	}
	return r
}

// fill reads the layer into h. Platform-specific parts (statfs, the raw
// file read) live in resources_host_linux.go / _other.go.
func (r *hostReader) fill(h *HostStats, now time.Time) {
	*h = HostStats{Enabled: r.cfg.Enabled()}
	if !h.Enabled {
		return
	}
	h.Count = 0
	for i, p := range r.cfg.DiskPaths {
		if h.Count >= maxDisks {
			break
		}
		d := &h.Disks[h.Count]
		d.Path = p
		fsid, ok := statfsInto(r.pathNUL[i], d)
		if !ok {
			h.Count++
			continue
		}
		// The same filesystem under two paths is one disk: report it once.
		dup := false
		for i := 0; i < h.Count; i++ {
			if r.fsids[i] == fsid && h.Disks[i].Err == "" {
				dup = true
				break
			}
		}
		if dup {
			*d = DiskStat{}
			continue
		}
		r.fsids[h.Count] = fsid
		if d.TotalBytes > 0 {
			d.FreePct = float64(d.FreeBytes) * 100 / float64(d.TotalBytes)
		}
		d.Low = (r.cfg.DiskMinFreePct > 0 && d.FreePct < r.cfg.DiskMinFreePct) ||
			(r.cfg.DiskMinFreeBytes > 0 && d.FreeBytes < r.cfg.DiskMinFreeBytes)
		h.Count++
	}
	r.fillBackup(&h.Backup, now)
	r.maybeAlert(h, now)
}

func (r *hostReader) fillBackup(b *BackupStat, now time.Time) {
	b.Dir = r.cfg.BackupDir
	b.MaxAgeS = r.cfg.BackupMaxAge.Seconds()
	if b.Dir == "" {
		return
	}
	n, mtime, ok := readStatusFile(r.statusPathNUL, r.statusBuf)
	if !ok {
		b.Status = BackupNone
		// No run ever: stale once the process has been up longer than the
		// max age (a fresh install has a night to produce its first set).
		b.Stale = now.Sub(r.startedAt) > r.cfg.BackupMaxAge
		b.Alarm = b.Stale
		b.Line = ""
		return
	}
	line := r.statusBuf[:n]
	if i := indexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if len(line) > 200 {
		line = line[:200]
	}
	// Allocate the string only when the line changed (once per backup run).
	if r.lastLineN != len(line) || r.lastLine != string(line) {
		r.lastLine, r.lastLineN = string(line), len(line)
	}
	b.Line = r.lastLine
	switch {
	case hasPrefix(line, "ok"):
		b.Status = BackupOK
	case hasPrefix(line, "failed"):
		b.Status = BackupFailed
	default:
		// Empty or unreadable: a run that could not even write its status —
		// measured on a FULL disk (RESILIENCIA-S1 §B4: the shell truncated the
		// file, the write failed, 0 bytes remained). Not "ok", not "none".
		b.Status = BackupUnknown
	}
	b.LastAt = mtime.Unix()
	b.AgeS = now.Sub(mtime).Seconds()
	b.Stale = now.Sub(mtime) > r.cfg.BackupMaxAge
	b.Alarm = b.Status != BackupOK || b.Stale
}

// maybeAlert sends at most one disk alert and one backup alert per cooldown,
// each on its own goroutine so a slow webhook never delays a tick.
func (r *hostReader) maybeAlert(h *HostStats, now time.Time) {
	if r.alerter == nil {
		return
	}
	if low := h.LowDisks(); len(low) > 0 && now.Sub(r.lastDiskAlert) > hostAlertCooldown {
		r.lastDiskAlert = now
		parts := make([]string, 0, len(low))
		level := LevelWarning
		for _, d := range low {
			parts = append(parts, fmt.Sprintf("%s: %s free of %s (%.1f %%)", d.Path, humanBytes(d.FreeBytes), humanBytes(d.TotalBytes), d.FreePct))
			if d.FreePct < r.cfg.DiskMinFreePct/2 || (r.cfg.DiskMinFreeBytes > 0 && d.FreeBytes < r.cfg.DiskMinFreeBytes/2) {
				level = LevelCritical
			}
		}
		msg := fmt.Sprintf("disk low — %s; floor %.0f %% / %s. When it reaches 0 PostgreSQL stops accepting writes (the engine answers 503) — free space now: old backup sets (%s), journald (journalctl --vacuum-size=200M), apt cache",
			strings.Join(parts, "; "), r.cfg.DiskMinFreePct, humanBytes(r.cfg.DiskMinFreeBytes), orDash(r.cfg.BackupDir))
		r.send(Alert{Kind: KindHost, Level: level, Route: "disk", Message: msg})
	}
	if h.Backup.Alarm && now.Sub(r.lastBackupAlert) > hostAlertCooldown {
		r.lastBackupAlert = now
		var msg string
		switch h.Backup.Status {
		case BackupFailed:
			msg = fmt.Sprintf("the last backup FAILED (%s) — no new set exists in %s; check: journalctl -u '*-backup' -n 40", h.Backup.Line, h.Backup.Dir)
		case BackupNone:
			msg = fmt.Sprintf("no backup has EVER run into %s and the app has been up %s — is the backup timer installed? systemctl list-timers '*backup*'", h.Backup.Dir, humanDuration(now.Sub(r.startedAt)))
		case BackupUnknown:
			msg = fmt.Sprintf("the last backup left an EMPTY or unreadable status in %s (%s ago) — a run that could not even write its result, typically a FULL disk; check df -h and journalctl -u '*-backup' -n 40", h.Backup.Dir, humanDuration(time.Duration(h.Backup.AgeS)*time.Second))
		default:
			msg = fmt.Sprintf("the last backup is %s old (status %q, floor %s) — the timer is not running: systemctl list-timers '*backup*'; run one now: /opt/<app>/scripts/backup.sh --app=<app>", humanDuration(time.Duration(h.Backup.AgeS)*time.Second), h.Backup.Status, humanDuration(r.cfg.BackupMaxAge))
		}
		r.send(Alert{Kind: KindHost, Level: LevelCritical, Route: "backup", Message: msg})
	}
}

func (r *hostReader) send(a Alert) {
	al := r.alerter
	go func() {
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = al.Send(ctx, a)
	}()
}

// ── small allocation-free helpers ───────────────────────────────────────────

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func hasPrefix(b []byte, s string) bool {
	if len(b) < len(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if b[i] != s[i] {
			return false
		}
	}
	return true
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	default:
		return fmt.Sprintf("%.0f min", d.Minutes())
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
