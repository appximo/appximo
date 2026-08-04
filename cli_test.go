package appximo

import (
	"strings"
	"testing"
)

// ADR-023: the deployable-binary CLI contract must be fail-LOUD — a misplaced
// argument aborts with an actionable message, never a silent boot with defaults.
func TestParseServeArgs_Contract(t *testing.T) {
	t.Parallel()
	def := ServeArgs{SchemaPath: "app.json", Port: 8099, ControlPort: 9099}

	// version → identity line, no error.
	_, line, err := parseServeArgs("myapp", "abc1234", "deadbeef", def, []string{"version"})
	if err != nil || !strings.Contains(line, "myapp abc1234") || !strings.Contains(line, "appximo framework") {
		t.Fatalf("version: line=%q err=%v", line, err)
	}

	// The unit's exact invocation: serve + flags.
	got, line, err := parseServeArgs("myapp", "v", "r", def,
		[]string{"serve", "--schema", "/etc/appximo/schema.json", "--port", "8090"})
	if err != nil || line != "" {
		t.Fatalf("serve: err=%v line=%q", err, line)
	}
	if got.SchemaPath != "/etc/appximo/schema.json" || got.Port != 8090 || got.ControlPort != 9099 {
		t.Fatalf("serve args = %+v", got)
	}

	// Bare flags (no serve) also work — the dev invocation.
	got, _, err = parseServeArgs("myapp", "v", "r", def, []string{"--port", "7000"})
	if err != nil || got.Port != 7000 || got.SchemaPath != "app.json" {
		t.Fatalf("bare flags: %+v err=%v", got, err)
	}

	// No args at all → the defaults.
	got, _, err = parseServeArgs("myapp", "v", "r", def, nil)
	if err != nil || got != def {
		t.Fatalf("defaults: %+v err=%v", got, err)
	}

	// An unknown subcommand fails LOUD with the contract in the message.
	_, _, err = parseServeArgs("myapp", "v", "r", def, []string{"migrate", "--tenant", "x"})
	if err == nil || !strings.Contains(err.Error(), "appximo-cli") {
		t.Fatalf("unknown subcommand must fail actionably, got: %v", err)
	}

	// THE trap: a bare word after serve would have silently discarded every
	// following flag under plain flag.Parse — here it is a hard error.
	_, _, err = parseServeArgs("myapp", "v", "r", def,
		[]string{"serve", "extra", "--schema", "x.json"})
	if err == nil || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("leftover argument must fail loud, got: %v", err)
	}

	// A leftover AFTER valid flags too.
	_, _, err = parseServeArgs("myapp", "v", "r", def,
		[]string{"--port", "8090", "stray"})
	if err == nil || !strings.Contains(err.Error(), "stray") {
		t.Fatalf("trailing argument must fail loud, got: %v", err)
	}
}
