//go:build !linux

package observability

// isTmpfs is a no-op outside Linux: the tmpfs magic-number probe (statfs) is
// Linux-specific, so ephemerality on other platforms is detected by the /tmp path
// check in isEphemeralPath alone.
func isTmpfs(string) bool { return false }
