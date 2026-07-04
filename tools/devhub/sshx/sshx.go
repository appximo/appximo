// Package sshx is the DevHub's outbound SSH client (S47). The DevHub on the
// dev box dials OUT to registered servers — Miguel keeps his single
// laptop→devbox tunnel; no inbound ports are opened anywhere.
//
// Security invariants (non-negotiable):
//   - Private keys are referenced by filesystem path only; key material never
//     reaches SQLite, API responses or logs.
//   - Host keys are TOFU: pinned on first connect (persisted by the caller via
//     OnHostKey) and strictly verified afterwards. A changed host key is an
//     explicit error — never InsecureIgnoreHostKey, never silent continue.
//   - Callers run FIXED commands with validated arguments; nothing here ever
//     receives a command string originating from the UI.
package sshx

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Server is the connection identity of a registered server. The api layer
// loads it from SQLite and persists TOFU host keys via OnHostKey.
type Server struct {
	ID      int64
	Name    string
	Host    string
	Port    int
	User    string
	KeyPath string
	// HostKey is the pinned public host key in authorized_keys format, empty
	// until the first successful connect.
	HostKey string
	// OnHostKey persists a newly-pinned host key (TOFU first connect). May be
	// nil, in which case the key is pinned for this process only.
	OnHostKey func(authorizedKey string) error
}

// Local reports whether the server is this box itself: commands run directly
// (no SSH) and the engine is reached on localhost.
func (s *Server) Local() bool {
	switch strings.ToLower(s.Host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

const (
	dialTimeout = 10 * time.Second
	idleClose   = 5 * time.Minute
)

// ── connection pool ──────────────────────────────────────────────────────────

type pooledConn struct {
	client   *ssh.Client
	lastUsed time.Time
}

var (
	poolMu sync.Mutex
	pool   = map[int64]*pooledConn{}
)

func init() {
	go func() {
		for range time.Tick(time.Minute) {
			poolMu.Lock()
			for id, pc := range pool {
				if time.Since(pc.lastUsed) > idleClose {
					pc.client.Close() //nolint:errcheck
					delete(pool, id)
				}
			}
			poolMu.Unlock()
		}
	}()
}

// Dial returns a live SSH client for the server, reusing the pooled connection
// when it is still alive and reconnecting when it is not.
func Dial(s *Server) (*ssh.Client, error) {
	if s.Local() {
		return nil, errors.New("sshx: server is local — no SSH connection needed")
	}
	poolMu.Lock()
	pc, ok := pool[s.ID]
	poolMu.Unlock()
	if ok {
		// Cheap liveness probe; a dead TCP conn fails immediately.
		if _, _, err := pc.client.SendRequest("keepalive@appitools", true, nil); err == nil {
			poolMu.Lock()
			pc.lastUsed = time.Now()
			poolMu.Unlock()
			return pc.client, nil
		}
		pc.client.Close() //nolint:errcheck
		poolMu.Lock()
		delete(pool, s.ID)
		poolMu.Unlock()
	}

	client, err := dialNew(s)
	if err != nil {
		return nil, err
	}
	poolMu.Lock()
	pool[s.ID] = &pooledConn{client: client, lastUsed: time.Now()}
	poolMu.Unlock()
	return client, nil
}

// Close drops the pooled connection for a server (e.g. when it is deleted
// from the registry).
func Close(serverID int64) {
	poolMu.Lock()
	defer poolMu.Unlock()
	if pc, ok := pool[serverID]; ok {
		pc.client.Close() //nolint:errcheck
		delete(pool, serverID)
	}
}

func dialNew(s *Server) (*ssh.Client, error) {
	keyBytes, err := os.ReadFile(s.KeyPath)
	if err != nil {
		// Do not echo the path contents anywhere; the path itself is fine.
		return nil, fmt.Errorf("read ssh key %s: %w", s.KeyPath, err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse ssh key %s: %w", s.KeyPath, err)
	}
	cfg := &ssh.ClientConfig{
		User:            s.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: tofuCallback(s),
		Timeout:         dialTimeout,
	}
	addr := net.JoinHostPort(s.Host, fmt.Sprintf("%d", s.Port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	return client, nil
}

// tofuCallback implements trust-on-first-use: with no pinned key the presented
// key is recorded (and persisted via OnHostKey); with a pinned key anything
// that does not match is a hard error.
func tofuCallback(s *Server) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		presented := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
		if s.HostKey == "" {
			if s.OnHostKey != nil {
				if err := s.OnHostKey(presented); err != nil {
					return fmt.Errorf("persist TOFU host key: %w", err)
				}
			}
			s.HostKey = presented
			return nil
		}
		if subtleEqual(s.HostKey, presented) {
			return nil
		}
		return fmt.Errorf("HOST KEY MISMATCH for %s: pinned key does not match the presented %s key — possible MITM; refusing to connect (clear the pinned key only if the change is expected)",
			s.Name, key.Type())
	}
}

// subtleEqual compares two short strings in constant time.
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// ── remote execution ─────────────────────────────────────────────────────────

// RunResult carries the outcome of one fixed command.
type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run executes a FIXED command (built by Go callers, never UI input) on the
// server and returns its output. Local servers execute directly.
func Run(s *Server, cmd string, timeout time.Duration) (*RunResult, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	if s.Local() {
		return runLocal(cmd, timeout)
	}
	client, err := Dial(s)
	if err != nil {
		return nil, err
	}
	sess, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh session: %w", err)
	}
	defer sess.Close() //nolint:errcheck

	var stdout, stderr strings.Builder
	sess.Stdout = &stdout
	sess.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case err = <-done:
	case <-time.After(timeout):
		sess.Signal(ssh.SIGKILL) //nolint:errcheck
		return nil, fmt.Errorf("remote command timed out after %s", timeout)
	}

	res := &RunResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitStatus()
			return res, nil
		}
		return nil, err
	}
	return res, nil
}

func runLocal(cmd string, timeout time.Duration) (*RunResult, error) {
	c := exec.Command("bash", "-c", cmd)
	var stdout, stderr strings.Builder
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(timeout):
		c.Process.Kill() //nolint:errcheck
		return nil, fmt.Errorf("local command timed out after %s", timeout)
	}
	res := &RunResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return nil, err
	}
	return res, nil
}

// ── port forward ─────────────────────────────────────────────────────────────

// Forward opens an ephemeral local listener that tunnels every connection to
// 127.0.0.1:remotePort on the server (scraping /metrics, smoke checks — no
// remote ports get exposed). The caller must Close() the listener. For local
// servers it returns a direct listener-less address via ForwardAddr instead;
// callers that may handle local servers should use ForwardAddr.
func Forward(s *Server, remotePort int) (net.Listener, error) {
	client, err := Dial(s)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go func() {
				remote, err := client.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", remotePort))
				if err != nil {
					local.Close() //nolint:errcheck
					return
				}
				go func() { io.Copy(remote, local); remote.Close() }() //nolint:errcheck
				io.Copy(local, remote)                                 //nolint:errcheck
				local.Close()                                          //nolint:errcheck
			}()
		}
	}()
	return ln, nil
}

// ForwardAddr returns a "host:port" address reaching the server's remotePort:
// localhost directly for local servers, an SSH tunnel otherwise. closer is
// non-nil only when a tunnel was opened.
func ForwardAddr(s *Server, remotePort int) (addr string, closer io.Closer, err error) {
	if s.Local() {
		return fmt.Sprintf("127.0.0.1:%d", remotePort), nil, nil
	}
	ln, err := Forward(s, remotePort)
	if err != nil {
		return "", nil, err
	}
	return ln.Addr().String(), ln, nil
}

// ── file push (deploy) ───────────────────────────────────────────────────────

// Push copies a local file to remotePath on the server via SFTP (or a plain
// file copy for local servers), preserving an executable mode.
func Push(s *Server, localPath, remotePath string) error {
	src, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer src.Close() //nolint:errcheck

	if s.Local() {
		dst, err := os.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			dst.Close() //nolint:errcheck
			return err
		}
		return dst.Close()
	}

	client, err := Dial(s)
	if err != nil {
		return err
	}
	sc, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("sftp: %w", err)
	}
	defer sc.Close() //nolint:errcheck

	dst, err := sc.OpenFile(remotePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("sftp create %s: %w", remotePath, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close() //nolint:errcheck
		return fmt.Errorf("sftp write: %w", err)
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return sc.Chmod(remotePath, 0o755)
}
