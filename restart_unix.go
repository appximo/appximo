//go:build !windows

package appximo

import (
	"os"
	"syscall"
)

// signalSelfTerm/execSelf isolate the unix-only self-restart primitives
// (PHASE4-FIRST-MILE-S1: the release ships a Windows .exe). SIGTERM-to-self
// reuses the battle-tested drain path; exec replaces the image on the same PID.
func signalSelfTerm() error { return syscall.Kill(os.Getpid(), syscall.SIGTERM) }

func execSelf(exe string) error { return syscall.Exec(exe, os.Args, os.Environ()) }
