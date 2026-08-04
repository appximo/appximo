package schema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

func TestLoadFromFile_FileNotFound(t *testing.T) {
	_, err := schema.LoadFromFile("/nonexistent/path/schema.json")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
	if !strings.Contains(err.Error(), "read schema file") {
		t.Errorf("expected 'read schema file' in error message, got: %v", err)
	}
}

func TestLoadFromFile_MalformedJSON(t *testing.T) {
	f := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(f, []byte(`{ this is not valid json`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := schema.LoadFromFile(f)
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
	if !strings.Contains(err.Error(), "parse schema JSON") {
		t.Errorf("expected 'parse schema JSON' in error message, got: %v", err)
	}
}

func TestLoadFromFile_MissingSchemaField(t *testing.T) {
	f := filepath.Join(t.TempDir(), "schema.json")
	if err := os.WriteFile(f, []byte(`{"version":"1","name":"test"}`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := schema.LoadFromFile(f)
	if err == nil {
		t.Fatal("expected error for missing $schema, got nil")
	}
	if !strings.Contains(err.Error(), "$schema") {
		t.Errorf("expected '$schema' in error message, got: %v", err)
	}
}

func TestLoadFromFile_MissingVersionField(t *testing.T) {
	f := filepath.Join(t.TempDir(), "schema.json")
	payload := `{"$schema":"https://appximo.com/schema/v1","name":"test"}`
	if err := os.WriteFile(f, []byte(payload), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := schema.LoadFromFile(f)
	if err == nil {
		t.Fatal("expected error for missing version, got nil")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("expected 'version' in error message, got: %v", err)
	}
}

func TestLoadFromFile_ValidSchema(t *testing.T) {
	s, err := schema.LoadFromFile("../../testdata/logistics/schema.json")
	if err != nil {
		t.Fatalf("expected no error for valid schema, got: %v", err)
	}
	if s.Name != "logistics-api" {
		t.Errorf("expected name 'logistics-api', got %q", s.Name)
	}
	if len(s.Resources) == 0 {
		t.Error("expected at least one resource, got 0")
	}
}
