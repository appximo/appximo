//go:build windows

package appximo

import "errors"

// Windows has neither SIGTERM-to-self nor exec-in-place. The one-click engine
// self-restart is a unix deployment feature (systemd/Docker in production); on
// Windows — a development platform for Appximo — the deploy answer still lands
// and the operator restarts the process by hand. Returning an error keeps the
// caller's CRITICAL log honest instead of silently doing nothing.
func signalSelfTerm() error {
	return errors.New("self-restart is not supported on windows — stop and start the process by hand")
}

func execSelf(string) error {
	return errors.New("self-restart is not supported on windows — stop and start the process by hand")
}
