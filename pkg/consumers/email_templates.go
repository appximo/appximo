package consumers

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"strings"
)

// builtinTemplates are the demo HTML email templates compiled INTO the binary.
// An application defines its own by dropping .html files in a templates dir and
// loading them with LoadEmailTemplates(fs.FS) — the registry is the same; only
// the source FS differs. html/template (stdlib) is the engine: zero dependencies,
// automatic context-aware escaping (a hostile {{.name}} is escaped, not injected),
// and the app owns the full markup. (A batteries-included engine like hermes could
// sit behind the same Render call, at the cost of a dependency — not taken.)
//
//go:embed email_templates/*.html
var builtinTemplates embed.FS

// defaultSubjects supply a Subject when the event payload omits one. The payload's
// "subject" always wins; this is only the fallback per template name.
var defaultSubjects = map[string]string{
	"verification": "Confirm your email",
	"welcome":      "Welcome",
}

// templateSet is a parsed, render-ready set of email templates keyed by name
// (the file's base name without .html). Parsed ONCE at construction — the hot
// path only executes the precompiled template, same discipline as the engine.
type templateSet struct {
	tmpls    map[string]*template.Template
	subjects map[string]string
}

// newBuiltinTemplates parses the embedded demo templates. It panics on a parse
// error because the templates are compiled in — a failure is a build-time bug, not
// a runtime condition.
func newBuiltinTemplates() *templateSet {
	ts, err := loadTemplates(builtinTemplates, "email_templates")
	if err != nil {
		panic("consumers: parse builtin email templates: " + err.Error())
	}
	return ts
}

// loadTemplates parses every *.html under dir in fsys into a templateSet. Missing
// keys in the data render as empty strings (missingkey=zero) so an optional
// variable left out of the payload does not break the send.
func loadTemplates(fsys embed.FS, dir string) (*templateSet, error) {
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read templates dir %q: %w", dir, err)
	}
	set := &templateSet{tmpls: map[string]*template.Template{}, subjects: defaultSubjects}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".html")
		b, err := fsys.ReadFile(dir + "/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read template %q: %w", e.Name(), err)
		}
		t, err := template.New(name).Option("missingkey=zero").Parse(string(b))
		if err != nil {
			return nil, fmt.Errorf("parse template %q: %w", name, err)
		}
		set.tmpls[name] = t
	}
	if len(set.tmpls) == 0 {
		return nil, fmt.Errorf("no .html templates found in %q", dir)
	}
	return set, nil
}

// render executes the named template with data and returns the HTML body. An
// unknown template name is an error (a permanent one for the caller — it will
// never become known on a retry).
func (s *templateSet) render(name string, data any) (string, error) {
	t, ok := s.tmpls[name]
	if !ok {
		return "", fmt.Errorf("unknown email template %q", name)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render template %q: %w", name, err)
	}
	return buf.String(), nil
}

// subjectFor returns the explicit subject if non-empty, else the template's
// default, else a generic fallback.
func (s *templateSet) subjectFor(name, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if d, ok := s.subjects[name]; ok {
		return d
	}
	return "Notification"
}
