package consumers

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"time"
)

// Email is one message to send. From/Date/Message-ID are filled by the SMTPSender
// (From from config; Message-ID by the EmailProcessor for idempotency). Text is an
// optional plaintext alternative — when set, the message is multipart/alternative
// so clients that can't render HTML still show something.
type Email struct {
	To        string
	Subject   string
	HTML      string
	Text      string // optional plaintext alternative
	MessageID string // deterministic per outbox row — best-effort dedup hint
}

// EmailSender sends one Email. The interface is the seam the EmailProcessor talks
// to, so tests substitute a capturing mock and never touch a real SMTP server.
type EmailSender interface {
	Send(ctx context.Context, m Email) error
}

// SMTPConfig points the SMTPSender at an EXTERNAL provider (Brevo, Resend,
// Mailgun, SES …) entirely through env-supplied values — no provider SDK, no
// hard-coded host. Self-hosting SMTP is deliberately NOT supported (deliverability,
// SPF/DKIM and IP reputation are a provider's job).
type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string        // envelope + header From, e.g. "App <no-reply@app.com>"
	Timeout  time.Duration // whole-dialog deadline; default 15s
	// InsecureSkipVerify disables TLS cert verification — TEST ONLY.
	InsecureSkipVerify bool
}

// SMTPSender sends mail over SMTP with STARTTLS (used automatically when the
// server advertises it) and AUTH PLAIN (only when a username is set). The whole
// dialog is bounded by Timeout, so a hung provider can never wedge the worker.
type SMTPSender struct {
	cfg SMTPConfig
}

// NewSMTPSender builds an SMTP sender. Host, Port and From are required; Username/
// Password are optional (an open relay or a test server needs neither).
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if cfg.Host == "" || cfg.Port == "" {
		return nil, fmt.Errorf("consumers: SMTP host and port are required")
	}
	if cfg.From == "" {
		return nil, fmt.Errorf("consumers: SMTP from address is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &SMTPSender{cfg: cfg}, nil
}

// Send delivers m. A connection/timeout/transient SMTP error is returned so the
// outbox keeps the row pending for retry; a permanent rejection (e.g. 550) is also
// returned and is eventually parked 'failed' by the worker after maxAttempts.
func (s *SMTPSender) Send(ctx context.Context, m Email) error {
	addr := net.JoinHostPort(s.cfg.Host, s.cfg.Port)

	d := net.Dialer{Timeout: s.cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("consumers: smtp dial %s: %w", addr, err)
	}
	// One deadline for the entire dialog — net/smtp has no per-call context.
	_ = conn.SetDeadline(time.Now().Add(s.cfg.Timeout))

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("consumers: smtp client: %w", err)
	}
	defer c.Close() //nolint:errcheck

	// STARTTLS whenever the server offers it — credentials and content then travel
	// encrypted. AUTH PLAIN refuses to send a password over a non-TLS link anyway
	// (net/smtp guards that), so plaintext auth only ever happens to localhost.
	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: s.cfg.Host, InsecureSkipVerify: s.cfg.InsecureSkipVerify} //nolint:gosec // opt-in, test-only
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("consumers: smtp starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
				return fmt.Errorf("consumers: smtp auth: %w", err)
			}
		}
	}

	if err := c.Mail(addrSpec(s.cfg.From)); err != nil {
		return fmt.Errorf("consumers: smtp MAIL FROM: %w", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("consumers: smtp RCPT TO %q: %w", m.To, err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("consumers: smtp DATA: %w", err)
	}
	if _, err := w.Write(s.buildMessage(m)); err != nil {
		return fmt.Errorf("consumers: smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("consumers: smtp close body: %w", err)
	}
	return c.Quit()
}

// buildMessage renders an RFC 5322 message: headers + an HTML body, or a
// multipart/alternative (text + HTML) when a plaintext alternative is supplied.
// CRLF line endings throughout, the Subject RFC 2047-encoded for non-ASCII.
func (s *SMTPSender) buildMessage(m Email) []byte {
	var b bytes.Buffer
	hdr := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }

	hdr("From", s.cfg.From)
	hdr("To", m.To)
	hdr("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	hdr("Date", time.Now().Format(time.RFC1123Z))
	if m.MessageID != "" {
		hdr("Message-ID", m.MessageID)
	}
	hdr("MIME-Version", "1.0")

	if m.Text == "" {
		hdr("Content-Type", "text/html; charset=UTF-8")
		b.WriteString("\r\n")
		writeCRLF(&b, m.HTML)
		return b.Bytes()
	}

	// multipart/alternative: plaintext first (lowest fidelity), HTML last.
	boundary := "appitools-" + nonce(m.MessageID)
	hdr("Content-Type", "multipart/alternative; boundary=\""+boundary+"\"")
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	writeCRLF(&b, m.Text)
	fmt.Fprintf(&b, "\r\n--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	writeCRLF(&b, m.HTML)
	fmt.Fprintf(&b, "\r\n--%s--\r\n", boundary)
	return b.Bytes()
}

// writeCRLF writes s with bare "\n" normalised to "\r\n" (SMTP requires CRLF) and
// dot-stuffs a leading "." so a body line of "." cannot end the DATA early.
func writeCRLF(b *bytes.Buffer, s string) {
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			line = trimCR(line)
			writeDotStuffed(b, line)
			b.WriteString("\r\n")
			start = i + 1
		}
	}
	if start < len(s) {
		writeDotStuffed(b, trimCR(s[start:]))
	}
}

func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}

func writeDotStuffed(b *bytes.Buffer, line string) {
	if len(line) > 0 && line[0] == '.' {
		b.WriteByte('.')
	}
	b.WriteString(line)
}

// addrSpec extracts the bare addr-spec from a header value like "Name <a@b.com>"
// for the SMTP MAIL FROM envelope (which wants only the address).
func addrSpec(from string) string {
	if i := bytes.IndexByte([]byte(from), '<'); i >= 0 {
		rest := from[i+1:]
		if j := bytes.IndexByte([]byte(rest), '>'); j >= 0 {
			return rest[:j]
		}
	}
	return from
}

// nonce derives a short, stable boundary suffix from the message id (or a fixed
// fallback) — deterministic so the same event produces a byte-identical message.
func nonce(seed string) string {
	if seed == "" {
		return "boundary"
	}
	out := make([]byte, 0, len(seed))
	for i := 0; i < len(seed); i++ {
		c := seed[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return "boundary"
	}
	return string(out)
}
