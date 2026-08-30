//go:build linux

package observability

import (
	"syscall"
	"time"
	"unsafe"
)

// The raw syscalls take a NUL-terminated path prepared ONCE (newHostReader):
// syscall.Statfs / syscall.Open convert their string argument on every call,
// and that conversion is a heap allocation — four of them per tick, which
// TestResourceCollector_TickAllocatesNothing_WithHost caught on the first
// run. The collector's budget is one allocation per tick, and layer 5 keeps it.

func nulPath(p string) []byte {
	b := make([]byte, len(p)+1)
	copy(b, p)
	return b
}

// statfsInto fills d from statfs(2). Free is f_bavail (what an unprivileged
// writer — PostgreSQL, the engine — can still use; root's reserve excluded).
// Returns the fsid so two paths on one filesystem are reported once.
func statfsInto(pathNUL []byte, d *DiskStat) ([2]int32, bool) {
	var st syscall.Statfs_t
	_, _, e := syscall.Syscall(syscall.SYS_STATFS, uintptr(unsafe.Pointer(&pathNUL[0])), uintptr(unsafe.Pointer(&st)), 0)
	if e != 0 {
		d.Err = errnoText(e)
		return [2]int32{}, false
	}
	bs := int64(st.Bsize)
	d.TotalBytes = int64(st.Blocks) * bs
	d.FreeBytes = int64(st.Bavail) * bs
	return [2]int32{st.Fsid.X__val[0], st.Fsid.X__val[1]}, true
}

// readStatusFile reads up to len(buf) bytes of the file with raw syscalls
// (openat / fstat / pread / close — no *os.File, no allocation) and returns
// the byte count and the file's modification time. ok=false when absent.
func readStatusFile(pathNUL []byte, buf []byte) (int, time.Time, bool) {
	fdr, _, e := syscall.Syscall6(syscall.SYS_OPENAT, uintptr(atFdcwd), uintptr(unsafe.Pointer(&pathNUL[0])), uintptr(syscall.O_RDONLY|syscall.O_CLOEXEC), 0, 0, 0)
	if e != 0 {
		return 0, time.Time{}, false
	}
	fd := int(fdr)
	defer func() { _ = syscall.Close(fd) }()
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return 0, time.Time{}, false
	}
	n, err := syscall.Pread(fd, buf, 0)
	if err != nil || n < 0 {
		return 0, time.Time{}, false
	}
	// n == 0 is a real answer: the file EXISTS and is EMPTY — what a backup run
	// on a FULL disk leaves behind (the shell truncates the status file and the
	// write fails). The caller treats that as an alarm, not as "no backup".
	return n, time.Unix(st.Mtim.Sec, st.Mtim.Nsec), true
}

// AT_FDCWD as a variable: a negative constant cannot be converted to uintptr
// directly; the two's-complement of the int is what the kernel expects.
var atFdcwd = -0x64

// errnoText names the common statfs failures without allocating for them.
func errnoText(e syscall.Errno) string {
	switch e {
	case syscall.ENOENT:
		return "path does not exist"
	case syscall.EACCES:
		return "permission denied"
	default:
		return "statfs failed"
	}
}
