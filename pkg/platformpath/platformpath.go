// Package platformpath resolves the platform-correct default data directory —
// ONE function every default path derives from (field report W1: the defaults
// were POSIX constants, so on Windows `/var/lib/appximo/files` silently
// resolved to `C:\var\lib\appximo\files`, a tree created at the drive root that
// the boot log announced in a format that does not exist on the system).
//
// Linux (and every other Unix) keeps the historical `/var/lib/appximo` —
// production installs, the installer and the fleet supervisor all own that
// path, so nothing churns. Windows and macOS get their platform conventions.
// Every default remains overridable by its existing knob (APPXIMO_FILES_DIR,
// OBS_DB_PATH, the fleet manifest's data_dir, …) — this package only decides
// what "unset" means.
package platformpath

import (
	"os"
	"path/filepath"
	"runtime"
)

// DataDir returns the root directory for engine-owned data when no explicit
// path is configured:
//
//	Linux/Unix:  /var/lib/appximo
//	Windows:     %LOCALAPPDATA%\Appximo   (machine-local app data)
//	macOS:       ~/Library/Application Support/Appximo
//
// It never creates the directory — callers create what they need lazily.
func DataDir() string {
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Appximo")
		}
		if d, err := os.UserCacheDir(); err == nil && d != "" {
			return filepath.Join(d, "Appximo")
		}
		// Last resort on a stripped environment: a well-known machine path,
		// never a Unix literal that would silently become C:\var.
		return filepath.Join(`C:\ProgramData`, "Appximo")
	case "darwin":
		if h, err := os.UserHomeDir(); err == nil && h != "" {
			return filepath.Join(h, "Library", "Application Support", "Appximo")
		}
		return "/var/lib/appximo"
	default:
		return "/var/lib/appximo"
	}
}

// FilesDir is the default root of the local content-addressable file store.
func FilesDir() string { return filepath.Join(DataDir(), "files") }

// ObsDBPath is the default observability SQLite location.
func ObsDBPath() string { return filepath.Join(DataDir(), "obs.db") }

// FleetDataDir is the default fleet supervisor data root.
func FleetDataDir() string { return filepath.Join(DataDir(), "fleet") }
