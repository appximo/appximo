package appximo

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestCgroupMemoryLimitFrom covers the C1 (PROD-PATH-BUILD-S1) memory-limit
// detection: a real cgroup limit is honoured; "max"/sentinel/absent are treated
// as "no limit" (so the engine never derives a ceiling from total RAM, which
// would starve a co-located Postgres).
func TestCgroupMemoryLimitFrom(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	absent := filepath.Join(dir, "does-not-exist")

	t.Run("v2 explicit limit", func(t *testing.T) {
		v2 := write("memory.max.512", "536870912\n")
		got, ok := cgroupMemoryLimitFrom(v2, absent)
		if !ok || got != 536870912 {
			t.Fatalf("got (%d,%v), want (536870912,true)", got, ok)
		}
	})
	t.Run("v2 max means unlimited", func(t *testing.T) {
		v2 := write("memory.max.max", "max\n")
		if _, ok := cgroupMemoryLimitFrom(v2, absent); ok {
			t.Fatal("v2 \"max\" must report no limit")
		}
	})
	t.Run("v2 present wins over v1 (no fallthrough)", func(t *testing.T) {
		v2 := write("memory.max.unlim", "max\n")
		v1 := write("limit.real", "268435456\n")
		if _, ok := cgroupMemoryLimitFrom(v2, v1); ok {
			t.Fatal("a present v2 file is authoritative; must not fall through to v1")
		}
	})
	t.Run("v1 explicit limit when v2 absent", func(t *testing.T) {
		v1 := write("limit_in_bytes", "268435456\n")
		got, ok := cgroupMemoryLimitFrom(absent, v1)
		if !ok || got != 268435456 {
			t.Fatalf("got (%d,%v), want (268435456,true)", got, ok)
		}
	})
	t.Run("v1 unlimited sentinel", func(t *testing.T) {
		// The classic cgroup v1 "no limit" value.
		v1 := write("limit.sentinel", strconv.FormatInt(9223372036854771712, 10)+"\n")
		if _, ok := cgroupMemoryLimitFrom(absent, v1); ok {
			t.Fatal("the v1 near-int64-max sentinel must report no limit")
		}
	})
	t.Run("no cgroup files at all", func(t *testing.T) {
		if _, ok := cgroupMemoryLimitFrom(absent, absent); ok {
			t.Fatal("no readable cgroup file must report no limit")
		}
	})
}
