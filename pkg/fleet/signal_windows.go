//go:build windows

package fleet

import "os"

// Windows has no unix signals: terminate via os.Process.Kill (hard stop). The
// multi-process `fleet run` supervisor is a unix deployment shape; on Windows
// the supported path is the single-engine `serve` (see the quick start), so a
// hard kill here is acceptable degradation, not a contract.
func signalTerm(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

func signalKill(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
