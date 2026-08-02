package schema_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
)

// TestMetaSchema_Compiles ensures the embedded meta-schema itself is a valid JSON
// Schema (a compile error here is a bug in appitools.schema.json).
func TestMetaSchema_Compiles(t *testing.T) {
	// A trivially-valid document exercises compilation without asserting content.
	raw := []byte(`{"$schema":"https://appitools.dev/schema/v1","version":"1"}`)
	if errs := schema.ValidateAgainstMetaSchema(raw); len(errs) != 0 {
		t.Fatalf("minimal valid schema rejected (meta-schema may not compile / be too strict): %v", errs)
	}
}

// TestMetaSchema_RepoSchemasPass is the PARITY proof in the accept direction: every
// schema in the repo that the Go validator accepts MUST also pass the meta-schema.
// If a Go-valid schema fails the meta-schema, the meta-schema is too strict (a bug).
func TestMetaSchema_RepoSchemasPass(t *testing.T) {
	roots := []string{
		"../../examples",
		"../../testdata",
		"../../tests/fixtures/schemas",
		"../../blueprints",
	}
	var files []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
				return nil
			}
			files = append(files, path)
			return nil
		})
	}
	if len(files) == 0 {
		t.Fatal("no candidate schema files found under the repo roots")
	}

	checked := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		// Only schemas the Go validator ACCEPTS are "valid repo schemas". Load+Validate
		// is the authority; if it rejects the file (a non-schema JSON or a deliberately
		// invalid fixture), it is not part of the accept-parity set.
		s, lerr := schema.LoadFromFile(f)
		if lerr != nil {
			continue
		}
		if verrs := schema.Validate(s); len(verrs) != 0 {
			continue
		}
		checked++
		if merrs := schema.ValidateAgainstMetaSchema(raw); len(merrs) != 0 {
			t.Errorf("Go-valid schema %s is REJECTED by the meta-schema (too strict):", f)
			for _, e := range merrs {
				t.Errorf("    %s", e.Error())
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Go-valid schemas were checked — the parity test would be vacuous")
	}
	t.Logf("meta-schema accepted all %d Go-valid repo schemas", checked)
}

// TestMetaSchema_RejectsInvalid is the PARITY proof in the reject direction: each
// constructed schema has a STRUCTURAL error the meta-schema must catch (the same
// classes the Go validator rejects: unknown type/key, bad name, bad enum value,
// op!=eq, js after-hook, …).
func TestMetaSchema_RejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"missing $schema":                                   `{"version":"1"}`,
		"missing version":                                   `{"$schema":"x"}`,
		"unknown field type (number)":                       `{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"type":"number"}}}}}`,
		"resource name with hyphen":                         `{"$schema":"x","version":"1","resources":{"order-items":{"fields":{"a":{"type":"string"}}}}}`,
		"resource name uppercase":                           `{"$schema":"x","version":"1","resources":{"Tasks":{"fields":{"a":{"type":"string"}}}}}`,
		"reserved resource transaction":                     `{"$schema":"x","version":"1","resources":{"transaction":{"fields":{"a":{"type":"string"}}}}}`,
		"reserved auth_ prefix":                             `{"$schema":"x","version":"1","resources":{"auth_users":{"fields":{"a":{"type":"string"}}}}}`,
		"unknown key at root":                               `{"$schema":"x","version":"1","resourcez":{}}`,
		"unknown key at resource":                           `{"$schema":"x","version":"1","resources":{"t":{"fieldz":{}}}}`,
		"unknown key at field":                              `{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"type":"string","foo":1}}}}}`,
		"field missing type":                                `{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"required":true}}}}}`,
		"bad on_delete":                                     `{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"type":"uuid","relation":"u","on_delete":"nuke"}}}}}`,
		"bad on_update":                                     `{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"type":"uuid","relation":"u","on_update":"nuke"}}}}}`,
		"unknown format":                                    `{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"type":"string","format":"phone"}}}}}`,
		"empty enum":                                        `{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"type":"string","enum":[]}}}}}`,
		"pattern over 200 chars":                            `{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"type":"string","pattern":"` + longPattern() + `"}}}}}`,
		"unknown relation type":                             `{"$schema":"x","version":"1","resources":{"t":{"relations":{"r":{"type":"has_one","target":"u","fk":"u_id"}}}}}`,
		"through on belongs_to":                             `{"$schema":"x","version":"1","resources":{"t":{"relations":{"r":{"type":"belongs_to","target":"u","fk":"u_id","through":"j"}}}}}`,
		"m2m missing target_fk":                             `{"$schema":"x","version":"1","resources":{"t":{"relations":{"r":{"type":"many_to_many","target":"u","fk":"u_id","through":"j"}}}}}`,
		"unknown hook event":                                `{"$schema":"x","version":"1","resources":{"t":{"hooks":{"on_create":{"type":"js","script":"x"}}}}}`,
		"js after_create hook":                              `{"$schema":"x","version":"1","resources":{"t":{"hooks":{"after_create":{"type":"js","script":"x"}}}}}`,
		"wasm after_update hook":                            `{"$schema":"x","version":"1","resources":{"t":{"hooks":{"after_update":{"type":"wasm","wasm_module":"m"}}}}}`,
		"webhook before_create hook":                        `{"$schema":"x","version":"1","resources":{"t":{"hooks":{"before_create":{"type":"webhook","url":"https://x.example/h"}}}}}`,
		"webhook before_update hook":                        `{"$schema":"x","version":"1","resources":{"t":{"hooks":{"before_update":{"type":"webhook","url":"https://x.example/h"}}}}}`,
		"js hook missing script":                            `{"$schema":"x","version":"1","resources":{"t":{"hooks":{"before_create":{"type":"js"}}}}}`,
		"bad event value":                                   `{"$schema":"x","version":"1","resources":{"t":{"events":["created"]}}}`,
		"duplicate events":                                  `{"$schema":"x","version":"1","resources":{"t":{"events":["create","create"]}}}`,
		"index without fields":                              `{"$schema":"x","version":"1","resources":{"t":{"indexes":[{"unique":true}]}}}`,
		"rbac condition op not eq":                          `{"$schema":"x","version":"1","rbac":{"roles":{"r":{"resources":["t"],"actions":["read"],"conditions":{"field":"x","op":"gt","val":"$user_id"}}}}}`,
		"rbac unknown key in role":                          `{"$schema":"x","version":"1","rbac":{"roles":{"r":{"resourcez":["t"]}}}}`,
		"rbac mixing both forms":                            `{"$schema":"x","version":"1","rbac":{"roles":{"r":{"resources":["t"],"actions":["read"],"permissions":{"t":{"actions":["read"]}}}}}}`,
		"per-resource bad action":                           `{"$schema":"x","version":"1","rbac":{"roles":{"r":{"permissions":{"t":{"actions":["frobnicate"]}}}}}}`,
		"per-resource condition_actions star":               `{"$schema":"x","version":"1","rbac":{"roles":{"r":{"permissions":{"t":{"actions":["read"],"conditions":{"field":"x","op":"eq","val":"$user_id"},"condition_actions":["*"]}}}}}}`,
		"per-resource condition_actions without conditions": `{"$schema":"x","version":"1","rbac":{"roles":{"r":{"permissions":{"t":{"actions":["read"],"condition_actions":["read"]}}}}}}`,
		"foreign_keys missing ref_columns":                  `{"$schema":"x","version":"1","resources":{"t":{"foreign_keys":[{"columns":["a"],"target":"u"}]}}}`,
		"state_machine missing initial":                     `{"$schema":"x","version":"1","resources":{"t":{"fields":{"s":{"type":"string","state_machine":{"transitions":{}}}}}}}`,
	}
	for name, raw := range cases {
		if errs := schema.ValidateAgainstMetaSchema([]byte(raw)); len(errs) == 0 {
			t.Errorf("%s: expected the meta-schema to REJECT it, but it passed", name)
		}
	}
}

func longPattern() string {
	b := make([]byte, 201)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
