//go:build linux

package observability

import "syscall"

// Linux filesystem magic numbers for RAM-backed (ephemeral) filesystems, whose
// contents do not survive a reboot or container restart. See statfs(2).
const (
	tmpfsMagic int64 = 0x01021994
	ramfsMagic int64 = 0x858458F6
)

// isTmpfs reports whether path resides on a RAM-backed filesystem (tmpfs/ramfs).
// Best-effort: any statfs error (e.g. the path does not exist yet) reports false.
func isTmpfs(path string) bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return false
	}
	return st.Type == tmpfsMagic || st.Type == ramfsMagic
}
