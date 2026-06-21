package aigen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
)

// loadTestSchemas gathers every schema the round-trip property must hold over: the
// stratified gold corpus (the "gold" sub-object of each case) and the repo's public
// example schemas. Read from disk (not imported) so this test stays in package
// aigen with no import cycle (eval imports aigen).
func loadTestSchemas(t *testing.T) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}

	// Corpus golds (eval/corpus/<stratum>/<id>.json → ["gold"]).
	corpusRoot := filepath.Join("eval", "corpus")
	_ = filepath.WalkDir(corpusRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("read %s: %v", path, rerr)
		}
		var wrap struct {
			Gold json.RawMessage `json:"gold"`
		}
		if jerr := json.Unmarshal(b, &wrap); jerr != nil || len(wrap.Gold) == 0 {
			t.Fatalf("parse corpus case %s: %v", path, jerr)
		}
		out["corpus:"+path] = decodeMap(t, wrap.Gold)
		return nil
	})

	// Repo example schemas (the file IS the schema).
	exampleGlobs := []string{
		filepath.Join("..", "..", "examples", "quickstart", "schema.json"),
		filepath.Join("..", "..", "examples", "erp-demo", "schema.json"),
		filepath.Join("..", "..", "examples", "model-lab", "*.json"),
		filepath.Join("..", "..", "examples", "aigen", "*.json"),
	}
	for _, g := range exampleGlobs {
		matches, _ := filepath.Glob(g)
		for _, path := range matches {
			b, rerr := os.ReadFile(path)
			if rerr != nil {
				continue
			}
			var m map[string]any
			if json.Unmarshal(b, &m) != nil {
				continue // not a plain schema file (e.g. a README dropped in)
			}
			if _, ok := m["resources"]; !ok {
				continue
			}
			out["example:"+path] = m
		}
	}

	if len(out) == 0 {
		t.Fatal("no test schemas found")
	}
	return out
}

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// TestIRRoundTripIdentity is the core property: map → IR → map == identity, over
// EVERY corpus gold and repo example. A lossy or order-unstable transform fails here.
func TestIRRoundTripIdentity(t *testing.T) {
	schemas := loadTestSchemas(t)
	for name, m := range schemas {
		ir := MapToIR(m)
		back := IRToMap(ir)
		if !reflect.DeepEqual(m, back) {
			t.Errorf("%s: map→IR→map is NOT identity\n  want: %s\n  got:  %s", name, mustJSON(m), mustJSON(back))
		}
	}
}

// TestIRReverseRoundTripIdentity: IR → map → IR == identity (the IR's deterministic
// ordering makes it stable).
func TestIRReverseRoundTripIdentity(t *testing.T) {
	schemas := loadTestSchemas(t)
	for name, m := range schemas {
		ir1 := MapToIR(m)
		ir2 := MapToIR(IRToMap(ir1))
		if !reflect.DeepEqual(ir1, ir2) {
			t.Errorf("%s: IR→map→IR is NOT identity", name)
		}
	}
}

// TestIRTransformedSchemasStillValidate: the map recovered from the IR must validate
// exactly as the original gold did (the transform changes representation, not meaning).
func TestIRTransformedSchemasStillValidate(t *testing.T) {
	schemas := loadTestSchemas(t)
	for name, m := range schemas {
		back := IRToMap(MapToIR(m))
		rep := schema.ValidateReport(mustJSONBytes(back))
		if !rep.Valid {
			t.Errorf("%s: IR-recovered schema does not validate: %+v", name, rep.Errors)
		}
	}
}

// TestIRProducesArrays sanity-checks that the arbitrary-keyed maps actually became
// arrays in the IR (the whole point — strict can constrain arrays of fixed items).
func TestIRProducesArrays(t *testing.T) {
	m := decodeMap(t, []byte(`{
		"$schema":"https://appitools.dev/schema/v1","version":"1","name":"x",
		"resources":{"b":{"fields":{"z":{"type":"string"}}},"a":{"fields":{"y":{"type":"int"}}}},
		"rbac":{"roles":{"admin":{"resources":"*","actions":["*"]}}}
	}`))
	ir := MapToIR(m)
	res, ok := ir["resources"].([]any)
	if !ok {
		t.Fatalf("resources is not an array: %T", ir["resources"])
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(res))
	}
	// Deterministic alphabetical order by name: "a" before "b".
	if asMap(res[0])["name"] != "a" || asMap(res[1])["name"] != "b" {
		t.Errorf("resources not sorted by name: %v, %v", asMap(res[0])["name"], asMap(res[1])["name"])
	}
	if _, ok := asMap(res[0])["fields"].([]any); !ok {
		t.Errorf("fields is not an array")
	}
	roles, ok := asMap(ir["rbac"])["roles"].([]any)
	if !ok || len(roles) != 1 || asMap(roles[0])["name"] != "admin" {
		t.Errorf("roles not an array of named objects: %v", asMap(ir["rbac"])["roles"])
	}
}

// TestTranslateMapPathToIR checks the error-path translation on deep paths, using a
// realistic IR document (resources/fields/roles/permissions as arrays).
func TestTranslateMapPathToIR(t *testing.T) {
	m := decodeMap(t, []byte(`{
		"$schema":"https://appitools.dev/schema/v1","version":"1","name":"x",
		"resources":{
			"accounts":{"fields":{"name":{"type":"string"}}},
			"entries":{"fields":{"amount":{"type":"float64"},"status":{"type":"string",
				"state_machine":{"initial":"draft","transitions":{"draft":["posted"],"posted":[]}}}},
				"relations":{"account":{"type":"belongs_to","target":"accounts","fk":"account_id"}}}
		},
		"rbac":{"roles":{
			"admin":{"resources":"*","actions":["*"]},
			"member":{"permissions":{"entries":{"actions":["read"]}}}
		}}
	}`))
	ir := MapToIR(m)
	// alphabetical order: resources[0]=accounts, resources[1]=entries;
	// entries.fields sorted: amount(0), status(1); roles[0]=admin, roles[1]=member.
	cases := []struct{ in, want string }{
		{"resources.entries.fields.status.type", "resources[1].fields[1].type"},
		{"resources.entries.fields.amount", "resources[1].fields[0]"},
		{"resources.accounts.fields.name.type", "resources[0].fields[0].type"},
		{"resources.entries.relations.account.target", "resources[1].relations[0].target"},
		{"rbac.roles.member.permissions.entries.actions", "rbac.roles[1].permissions[0].actions"},
		{"resources.entries.fields.status.state_machine.transitions.draft", "resources[1].fields[1].state_machine.transitions[0]"},
		{"$", "$"},
		{"resources.nonexistent.fields.x", "resources.nonexistent.fields.x"}, // unresolved → verbatim
	}
	for _, c := range cases {
		got := TranslateMapPathToIR(c.in, ir)
		if got != c.want {
			t.Errorf("TranslateMapPathToIR(%q)\n  got:  %q\n  want: %q", c.in, got, c.want)
		}
	}
}

// TestSemanticFaultPathTranslates: the harness injects a broken relation (the
// simulator's semantic fault); its validator path must translate to a coherent IR
// path (so the model corrects in its own space).
func TestSemanticFaultPathTranslates(t *testing.T) {
	m := decodeMap(t, []byte(`{
		"$schema":"https://appitools.dev/schema/v1","version":"1","name":"x",
		"resources":{"items":{"fields":{"brokenref_id":{"type":"uuid","relation":"nope"}}}}
	}`))
	ir := MapToIR(m)
	got := TranslateMapPathToIR("resources.items.fields.brokenref_id.relation", ir)
	want := "resources[0].fields[0].relation"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// TestIROutputSchemaIsStrictSubset walks the entire IR output schema and asserts it
// is inside the structured-outputs strict subset: no disallowed keyword anywhere,
// every object additionalProperties:false with `required` == all its property keys.
// This is the property the map-form meta-schema CANNOT satisfy — the reason the IR
// exists.
func TestIROutputSchemaIsStrictSubset(t *testing.T) {
	walkStrict(t, "$", IROutputSchema())
}

func walkStrict(t *testing.T, path string, node map[string]any) {
	t.Helper()
	for _, bad := range disallowedInStrictSubset {
		if _, present := node[bad]; present {
			t.Errorf("%s: uses disallowed strict-subset keyword %q", path, bad)
		}
	}
	props, hasProps := node["properties"].(map[string]any)
	if hasProps {
		if ap, ok := node["additionalProperties"].(bool); !ok || ap {
			t.Errorf("%s: object with properties must set additionalProperties:false", path)
		}
		// required must list EVERY property key (strict has no optional properties).
		req := map[string]bool{}
		for _, r := range node["required"].([]any) {
			req[r.(string)] = true
		}
		var missing []string
		for k := range props {
			if !req[k] {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("%s: properties not in required: %v", path, missing)
		}
		for k, v := range props {
			if sub, ok := v.(map[string]any); ok {
				walkStrict(t, path+"."+k, sub)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		walkStrict(t, path+".items", items)
	}
}

func mustJSON(v any) string { b, _ := json.MarshalIndent(v, "", " "); return string(b) }
func mustJSONBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
