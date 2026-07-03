package files

import (
	"bytes"
	"errors"
	"testing"
)

// pngHeader is a minimal real PNG signature (magic bytes sniff → image/png).
var pngHeader = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 13, 'I', 'H', 'D', 'R'}

func TestUploadPolicy_ExtensionAllowlist(t *testing.T) {
	p := NewUploadPolicy(nil) // defaults

	// Not on the allowlist → rejected regardless of declared type.
	for _, name := range []string{"shell.php", "run.exe", "x.sh", "a.jsp", "b.phtml"} {
		if _, err := p.Validate(name, "image/jpeg", []byte("<?php echo 1; ?>")); !errors.Is(err, ErrUploadRejected) {
			t.Errorf("Validate(%q) err = %v, want ErrUploadRejected", name, err)
		}
	}
	// On the allowlist → accepted.
	if _, err := p.Validate("doc.txt", "text/plain", []byte("hello")); err != nil {
		t.Fatalf("txt: %v", err)
	}
	// No extension → accepted (hash-keyed, attachment+nosniff — inert).
	if _, err := p.Validate("README", "", []byte("hello")); err != nil {
		t.Fatalf("no ext: %v", err)
	}
	// Custom allowlist replaces the default.
	custom := NewUploadPolicy([]string{".csv"})
	if _, err := custom.Validate("a.txt", "", []byte("x")); !errors.Is(err, ErrUploadRejected) {
		t.Fatal("custom allowlist must reject .txt")
	}
	if _, err := custom.Validate("a.csv", "", []byte("x")); err != nil {
		t.Fatalf("custom allowlist must accept .csv: %v", err)
	}
	// "*" disables the extension check.
	all := NewUploadPolicy([]string{"*"})
	if _, err := all.Validate("shell.php", "", []byte("<?php")); err != nil {
		t.Fatalf("wildcard policy must accept any extension: %v", err)
	}
}

func TestUploadPolicy_MagicBytes(t *testing.T) {
	p := NewUploadPolicy(nil)

	// PHP source renamed to photo.jpg, declared image/jpeg: the sniff says
	// text/plain → rejected (BOTH the ext↔magic and declared↔magic checks fire).
	if _, err := p.Validate("photo.jpg", "image/jpeg", []byte("<?php system($_GET['c']); ?>")); !errors.Is(err, ErrUploadRejected) {
		t.Fatalf("spoofed jpg err = %v, want ErrUploadRejected", err)
	}
	// A real PNG named .png passes and keeps the declared type.
	ct, err := p.Validate("pic.png", "image/png", pngHeader)
	if err != nil {
		t.Fatalf("real png: %v", err)
	}
	if ct != "image/png" {
		t.Fatalf("stored ct = %q", ct)
	}
	// A real PNG named .jpg → extension/magic mismatch → rejected.
	if _, err := p.Validate("pic.jpg", "", pngHeader); !errors.Is(err, ErrUploadRejected) {
		t.Fatalf("png-as-jpg err = %v, want ErrUploadRejected", err)
	}
	// Declared image/* over text content with a NON-magic extension (.txt is
	// allowlisted, no signature): the declared-family check still catches it.
	if _, err := p.Validate("x.txt", "image/jpeg", []byte("just text")); !errors.Is(err, ErrUploadRejected) {
		t.Fatalf("declared-type spoof err = %v, want ErrUploadRejected", err)
	}
	// SVG is exempt from the family check (no sniff signature exists).
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)
	if _, err := p.Validate("pic.svg", "image/svg+xml", svg); err != nil {
		t.Fatalf("svg: %v", err)
	}
	// Inconclusive sniff (binary garbage → octet-stream) does NOT reject.
	if _, err := p.Validate("data.bin", "", bytes.Repeat([]byte{0xfe, 0x01, 0x9c}, 40)); err != nil {
		t.Fatalf("inconclusive sniff must pass: %v", err)
	}
	// Empty declared type stores the sniffed one.
	ct, err = p.Validate("note.txt", "", []byte("plain words"))
	if err != nil {
		t.Fatalf("sniffed ct: %v", err)
	}
	if ct != "text/plain" {
		t.Fatalf("stored ct = %q, want text/plain", ct)
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"informe.pdf":            "informe.pdf",
		"../../etc/passwd":       "passwd",
		`..\..\windows\evil.dll`: "evil.dll",
		".bashrc":                "bashrc",
		"a..b.txt":               "a.b.txt",
		"con\ntrol\rchars.txt":   "controlchars.txt",
		`quo"te.txt`:             "quote.txt",
		"año fiscal (2026).xlsx": "año fiscal (2026).xlsx",
		"weird<>|chars?.txt":     "weird___chars_.txt",
		"":                       "",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
	// Length cap.
	long := SanitizeName(string(bytes.Repeat([]byte("a"), 500)) + ".txt")
	if len([]rune(long)) != 200 {
		t.Fatalf("len = %d, want 200", len([]rune(long)))
	}
}
