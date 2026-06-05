//go:build go1.18

package schema

import (
	"encoding/json"
	"testing"
)

// FuzzValidateSchema feeds malformed/malicious schema JSON through the real
// parse+validate pipeline (json.Unmarshal → Validate). Neither step may panic:
// schemas are operator-supplied but the control-plane accepts them over HTTP, so
// a panic here is a remote DoS.
func FuzzValidateSchema(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"resources":{}}`,
		`{"resources":{"a":{"fields":{"b":{"type":"string"}}}}}`,
		`{"resources":{"a'; DROP TABLE tenants; --":{"fields":{"b":{"type":"string"}}}}}`,
		`{"resources":{"a":{"fields":{"b":{"type":"'; DROP","relation":"zzz"}}}}}`,
		`{"version":"99999999999999999999999999999999999999"}`,
		`{"resources":{"a":{"fields":null,"hooks":null,"indexes":null}}}`,
		`{"rbac":{"roles":{"r":{"resources":"*","actions":null,"conditions":{"field":"","op":"","val":""}}}}}`,
		`null`, `[]`, ``, `{"resources":{"a":{"fields":{"b":{}}}}}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, data string) {
		var s APISchema
		if json.Unmarshal([]byte(data), &s) != nil {
			return
		}
		_ = Validate(&s) // must never panic on any parseable schema
	})
}
