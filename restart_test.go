package appximo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/platformadmin"

	"errors"
)

const validBootSchema = `{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "restart-test",
  "resources": {
    "tasks": { "fields": { "title": { "type": "string", "required": true } } }
  }
}`

const validBootSchemaV2 = `{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "restart-test",
  "resources": {
    "tasks":  { "fields": { "title": { "type": "string", "required": true } } },
    "orders": { "fields": { "total": { "type": "float64" } } }
  }
}`

func writeBoot(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPersistBootSchemaFile_ValidReplacesAtomicallyWithBackupAndMarker(t *testing.T) {
	path := writeBoot(t, t.TempDir(), validBootSchema)

	if err := persistBootSchemaFile(path, []byte(validBootSchemaV2)); err != nil {
		t.Fatalf("persist: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != validBootSchemaV2 {
		t.Fatalf("boot schema not replaced:\n%s", got)
	}
	bak, err := os.ReadFile(bootBackupPath(path))
	if err != nil || string(bak) != validBootSchema {
		t.Fatalf("backup missing or wrong (err=%v):\n%s", err, bak)
	}
	if _, err := os.Stat(bootMarkerPath(path)); err != nil {
		t.Fatalf("self-restart marker missing: %v", err)
	}
}

func TestPersistBootSchemaFile_InvalidRejectsAndWritesNothing(t *testing.T) {
	path := writeBoot(t, t.TempDir(), validBootSchema)

	cases := []string{
		`{not json`,
		`{"version":"1","resources":{}}`, // missing $schema
		`{"$schema":"x","version":"1","resources":{"t":{"fieldz":{}}}}`,                      // unknown key
		`{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"type":"number"}}}}}`, // bad type
	}
	for _, raw := range cases {
		err := persistBootSchemaFile(path, []byte(raw))
		if err == nil {
			t.Fatalf("expected rejection for %q", raw)
		}
		if !errors.Is(err, platformadmin.ErrSchemaRejected) {
			t.Fatalf("want ErrSchemaRejected, got %v", err)
		}
	}
	// The boot schema, backup and marker are untouched — nothing was written.
	got, _ := os.ReadFile(path)
	if string(got) != validBootSchema {
		t.Fatal("boot schema was modified by a rejected persist")
	}
	if _, err := os.Stat(bootBackupPath(path)); !os.IsNotExist(err) {
		t.Fatal("backup written for a rejected persist")
	}
	if _, err := os.Stat(bootMarkerPath(path)); !os.IsNotExist(err) {
		t.Fatal("marker written for a rejected persist")
	}
}

func TestRecoverBootSchema_RestoresBackupOnlyWithinSelfRestartWindow(t *testing.T) {
	dir := t.TempDir()
	path := writeBoot(t, dir, validBootSchema)

	// No marker (a hand-edited broken schema): recovery must NOT engage.
	if recoverBootSchema(path, errors.New("boom")) {
		t.Fatal("recovered without a self-restart marker")
	}

	// Persist v2 (writes .bak + marker), then corrupt the schema file — the
	// non-schema failure a validated persist can't rule out (e.g. disk fault).
	if err := persistBootSchemaFile(path, []byte(validBootSchemaV2)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{corrupted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !recoverBootSchema(path, errors.New("parse error")) {
		t.Fatal("recovery did not engage inside the self-restart window")
	}
	got, _ := os.ReadFile(path)
	if string(got) != validBootSchema {
		t.Fatalf("backup not restored:\n%s", got)
	}
	if _, err := os.Stat(bootMarkerPath(path)); !os.IsNotExist(err) {
		t.Fatal("marker not cleared after recovery")
	}

	if s, err := loadAndValidateSchema(path); err != nil || s == nil {
		t.Fatalf("restored schema does not boot: %v", err)
	}
}

func TestLoadAndValidateSchema_ErrorsAreAggregated(t *testing.T) {
	path := writeBoot(t, t.TempDir(), `{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"type":"number"}}}}}`)
	_, err := loadAndValidateSchema(path)
	if err == nil || !strings.Contains(err.Error(), "invalid schema") {
		t.Fatalf("want aggregated invalid-schema error, got %v", err)
	}
}

// A boot schema the engine cannot replace is named, with the ONE command that
// fixes it, BEFORE anything is written — a real box (2026-09-26) had its
// schema owned by root under install.sh's sticky-bit directory, and Studio's
// deploy failed with a raw «rename …: operation not permitted» after leaving a
// self-restart marker behind.
func TestPersistBootSchema_UnreplaceableFileIsNamedWithTheFix(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, []byte(`{"$schema":"https://appximo.com/schema/v1","version":"1","name":"a","resources":{"t":{"fields":{"x":{"type":"string"}}}},"rbac":{"roles":{"admin":{"resources":"*","actions":["*"]}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() == 0 {
		t.Skip("root can replace anything")
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"$schema":"https://appximo.com/schema/v1","version":"1","name":"b","resources":{"t":{"fields":{"x":{"type":"string"}}}},"rbac":{"roles":{"admin":{"resources":"*","actions":["*"]}}}}`)
	err := persistBootSchemaFile(path, raw)
	if err == nil {
		t.Fatal("a read-only schema was replaced")
	}
	for _, want := range []string{"cannot replace the boot schema", "chown"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if _, statErr := os.Stat(bootMarkerPath(path)); statErr == nil {
		t.Error("a self-restart marker was left behind by a persist that did not happen")
	}
}
