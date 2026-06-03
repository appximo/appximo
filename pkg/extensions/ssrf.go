package extensions

import (
	"fmt"
	"net"
	"syscall"
)

// CheckSSRF returns an error if ip should be blocked to prevent Server-Side
// Request Forgery. Blocked ranges:
//   - Loopback       (127.0.0.0/8, ::1)
//   - Private        (RFC 1918 + RFC 4193 ULA)
//   - Link-local     (169.254.0.0/16, fe80::/10) — includes AWS/GCP metadata IPs
func CheckSSRF(ip net.IP) error {
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("ssrf guard: loopback address blocked: %s", ip)
	case ip.IsPrivate():
		return fmt.Errorf("ssrf guard: private address blocked: %s", ip)
	case ip.IsLinkLocalUnicast():
		return fmt.Errorf("ssrf guard: link-local address blocked: %s", ip)
	}
	return nil
}

// ssrfDialerControl is the net.Dialer.Control hook that enforces SSRF rules.
// address is already resolved to "ip:port" by the time this is called.
func ssrfDialerControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("ssrf guard: cannot parse address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("ssrf guard: non-IP address %q after resolution", host)
	}
	return CheckSSRF(ip)
}
