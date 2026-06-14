package consumers

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockSMTPServer is a minimal in-memory SMTP server for tests: it speaks just
// enough of the protocol (EHLO → MAIL → RCPT → DATA → QUIT) to accept one message
// and capture the envelope + raw DATA. It advertises NO STARTTLS and NO AUTH, so
// the SMTPSender exercises the plaintext MAIL/RCPT/DATA path against it — no TLS
// certs, no real network, no real email.
type mockSMTPServer struct {
	ln       net.Listener
	mu       sync.Mutex
	mailFrom string
	rcptTo   string
	data     string
	done     chan struct{}
}

func newMockSMTPServer(t *testing.T) *mockSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &mockSMTPServer{ln: ln, done: make(chan struct{})}
	go s.serveOne()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *mockSMTPServer) hostPort() (string, string) {
	h, p, _ := net.SplitHostPort(s.ln.Addr().String())
	return h, p
}

func (s *mockSMTPServer) serveOne() {
	defer close(s.done)
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close() //nolint:errcheck
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(conn)
	wr := func(s string) { _, _ = conn.Write([]byte(s)) }

	wr("220 mock ESMTP\r\n")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			// Single-line greeting: advertise no extensions (no STARTTLS, no AUTH).
			wr("250 mock\r\n")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			s.set(&s.mailFrom, strings.TrimSpace(line[len("MAIL FROM"):]))
			wr("250 OK\r\n")
		case strings.HasPrefix(cmd, "RCPT TO"):
			s.set(&s.rcptTo, strings.TrimSpace(line[len("RCPT TO"):]))
			wr("250 OK\r\n")
		case cmd == "DATA":
			wr("354 End data with <CR><LF>.<CR><LF>\r\n")
			var b strings.Builder
			for {
				dl, err := br.ReadString('\n')
				if err != nil {
					return
				}
				if dl == ".\r\n" || dl == ".\n" {
					break
				}
				b.WriteString(dl)
			}
			s.set(&s.data, b.String())
			wr("250 OK queued\r\n")
		case cmd == "QUIT":
			wr("221 bye\r\n")
			return
		default:
			wr("250 OK\r\n")
		}
	}
}

func (s *mockSMTPServer) set(dst *string, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*dst = v
}

func (s *mockSMTPServer) captured() (from, to, data string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mailFrom, s.rcptTo, s.data
}

func TestSMTPSender_HTMLOnly(t *testing.T) {
	srv := newMockSMTPServer(t)
	host, port := srv.hostPort()
	sender, err := NewSMTPSender(SMTPConfig{Host: host, Port: port, From: "App <no-reply@app.com>", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}

	err = sender.Send(context.Background(), Email{
		To: "ana@example.com", Subject: "Hello ✓", HTML: "<p>Hi</p>", MessageID: "<m-1@appitools.local>",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	<-srv.done

	from, to, data := srv.captured()
	if !strings.Contains(from, "no-reply@app.com") {
		t.Fatalf("MAIL FROM = %q", from)
	}
	if !strings.Contains(to, "ana@example.com") {
		t.Fatalf("RCPT TO = %q", to)
	}
	// Headers + body present, Subject RFC2047-encoded (non-ASCII ✓), Message-ID set.
	for _, want := range []string{
		"To: ana@example.com",
		"Message-ID: <m-1@appitools.local>",
		"Content-Type: text/html; charset=UTF-8",
		"<p>Hi</p>",
		"=?utf-8?q?", // encoded-word subject
	} {
		if !strings.Contains(strings.ToLower(data), strings.ToLower(want)) {
			t.Fatalf("DATA missing %q\n---\n%s", want, data)
		}
	}
}

func TestSMTPSender_Multipart_WhenTextSet(t *testing.T) {
	srv := newMockSMTPServer(t)
	host, port := srv.hostPort()
	sender, _ := NewSMTPSender(SMTPConfig{Host: host, Port: port, From: "a@b.com", Timeout: 5 * time.Second})

	if err := sender.Send(context.Background(), Email{
		To: "x@y.com", Subject: "S", HTML: "<p>H</p>", Text: "plain", MessageID: "<id@d>",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	<-srv.done
	_, _, data := srv.captured()
	for _, want := range []string{"multipart/alternative", "text/plain; charset=UTF-8", "text/html; charset=UTF-8", "plain", "<p>H</p>"} {
		if !strings.Contains(data, want) {
			t.Fatalf("multipart DATA missing %q\n---\n%s", want, data)
		}
	}
}

func TestSMTPSender_DotStuffing(t *testing.T) {
	srv := newMockSMTPServer(t)
	host, port := srv.hostPort()
	sender, _ := NewSMTPSender(SMTPConfig{Host: host, Port: port, From: "a@b.com", Timeout: 5 * time.Second})

	// A body line that is exactly "." must be dot-stuffed to ".." on the wire so it
	// doesn't terminate DATA early; the captured (de-stuffed by our reader) line is ".".
	body := "line1\n.\nline3"
	if err := sender.Send(context.Background(), Email{To: "x@y.com", Subject: "S", HTML: body, MessageID: "<id@d>"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	<-srv.done
	_, _, data := srv.captured()
	if !strings.Contains(data, "line1") || !strings.Contains(data, "line3") {
		t.Fatalf("body truncated by an unescaped dot line:\n%s", data)
	}
}

func TestSMTPSender_DialError(t *testing.T) {
	// Nothing listening on this port → dial fails → transient error (retryable).
	sender, _ := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: "1", From: "a@b.com", Timeout: 500 * time.Millisecond})
	if err := sender.Send(context.Background(), Email{To: "x@y.com", Subject: "S", HTML: "h", MessageID: "<id@d>"}); err == nil {
		t.Fatal("expected a dial error")
	}
}

func TestNewSMTPSender_Validation(t *testing.T) {
	for _, cfg := range []SMTPConfig{
		{Port: "587", From: "a@b.com"}, // no host
		{Host: "h", From: "a@b.com"},   // no port
		{Host: "h", Port: "587"},       // no from
	} {
		if _, err := NewSMTPSender(cfg); err == nil {
			t.Fatalf("expected validation error for %+v", cfg)
		}
	}
}
