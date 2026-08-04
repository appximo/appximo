package schema

import (
	"strings"
	"testing"
)

// FILES-1 — load-time validation of the per-field file attach policy.

func policySchema(fieldJSON string) string {
	return `{
		"$schema": "https://appximo.com/schema/v1",
		"version": "1",
		"name": "policy-test",
		"resources": {
			"docs": { "fields": { "adjunto": ` + fieldJSON + ` } }
		}
	}`
}

func TestFilePolicyValidAccepted(t *testing.T) {
	cases := []string{
		`{ "type": "file", "accept": "image", "max_bytes": 5242880 }`,
		`{ "type": "file", "accept": ["image", "pdf"] }`,
		`{ "type": "file", "accept": ["application/zip"] }`,
		`{ "type": "file", "max_bytes": 1 }`,
	}
	for _, c := range cases {
		s, err := LoadFromBytes([]byte(policySchema(c)))
		if err != nil {
			t.Errorf("valid policy %s rejected at load: %v", c, err)
			continue
		}
		if verrs := Validate(s); len(verrs) > 0 {
			t.Errorf("valid policy %s rejected by Validate: %v", c, verrs)
		}
	}
}

func TestFilePolicyRejections(t *testing.T) {
	cases := []struct{ field, wantSubstr string }{
		{`{ "type": "file", "accept": ["spreadsheet"] }`, "invalid accept entry"},
		{`{ "type": "file", "accept": ["image/"] }`, "invalid accept entry"},
		{`{ "type": "file", "max_bytes": -5 }`, "max_bytes"},
		{`{ "type": "string", "accept": "image" }`, "only valid on a file field"},
		{`{ "type": "int64", "max_bytes": 100 }`, "only valid on a file field"},
	}
	for _, c := range cases {
		s, err := LoadFromBytes([]byte(policySchema(c.field)))
		if err != nil {
			t.Errorf("policy %s failed at load (expected a Validate error): %v", c.field, err)
			continue
		}
		verrs := Validate(s)
		if len(verrs) == 0 {
			t.Errorf("invalid policy %s was accepted", c.field)
			continue
		}
		joined := ""
		for _, v := range verrs {
			joined += v.Error() + "\n"
		}
		if !strings.Contains(joined, c.wantSubstr) {
			t.Errorf("policy %s: errors %q do not mention %q", c.field, joined, c.wantSubstr)
		}
	}
}

func TestStringListSingleString(t *testing.T) {
	s, err := LoadFromBytes([]byte(policySchema(`{ "type": "file", "accept": "image" }`)))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	fd := s.Resources["docs"].Fields["adjunto"]
	if len(fd.Accept) != 1 || fd.Accept[0] != "image" {
		t.Fatalf("accept single-string form parsed as %v", fd.Accept)
	}
}

func TestFileAcceptMatches(t *testing.T) {
	fd := &FieldDef{Type: "file", Accept: StringList{"image", "pdf", "application/zip"}}
	cases := []struct {
		ct   string
		want bool
	}{
		{"image/jpeg", true},
		{"image/png", true},
		{"IMAGE/PNG", true},                    // stored types compared case-insensitively
		{"image/svg+xml; charset=utf-8", true}, // parameters stripped
		{"application/pdf", true},              // via the pdf alias
		{"application/zip", true},              // exact entry
		{"application/x-msdownload", false},    // not accepted
		{"imagejpeg", false},                   // family must be a prefix with '/'
		{"", false},                            // undetected type fails a non-empty list CLOSED
		{"text/plain", false},                  // family not listed
	}
	for _, c := range cases {
		if got := fd.FileAcceptMatches(c.ct); got != c.want {
			t.Errorf("FileAcceptMatches(%q) = %v, want %v", c.ct, got, c.want)
		}
	}
	empty := &FieldDef{Type: "file"}
	if !empty.FileAcceptMatches("anything/at-all") {
		t.Error("an empty accept list must accept everything")
	}
	if (&FieldDef{Type: "file", MaxBytes: 5}).HasFilePolicy() != true {
		t.Error("max_bytes alone is a policy")
	}
	if (&FieldDef{Type: "file"}).HasFilePolicy() {
		t.Error("a bare file field has no policy")
	}
}
