//go:build !linux

package observability

import (
	"os"
	"time"
)

func nulPath(p string) []byte { return []byte(p) }

// Outside Linux the disk is not observed (statfs differs per platform and
// the production layout is Linux); the backup status file is read with the
// portable API (it allocates, which the Linux path avoids — acceptable where
// the alloc-free tick is not measured).
func statfsInto(_ []byte, d *DiskStat) ([2]int32, bool) {
	d.Err = "disk not observed on this platform"
	return [2]int32{}, false
}

func readStatusFile(path []byte, buf []byte) (int, time.Time, bool) {
	f, err := os.Open(string(path))
	if err != nil {
		return 0, time.Time{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, time.Time{}, false
	}
	n, _ := f.Read(buf)
	if n < 0 {
		n = 0
	}
	return n, st.ModTime(), true
}
