//go:build !windows

package fleet

import "syscall"

// signalTerm/signalKill isolate the unix-only syscall.Kill so the engine
// cross-compiles for windows (PHASE4-FIRST-MILE-S1: the release ships a
// Windows .exe; `fleet run`'s multi-process supervisor stays unix-only in
// behavior — on windows these degrade to a no-op error path, and the
// documented Windows path is the single-engine `serve`).
func signalTerm(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func signalKill(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }
