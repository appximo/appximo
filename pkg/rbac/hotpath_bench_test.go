package rbac_test

import (
	"encoding/json"
	"testing"

	"github.com/miguelangel/appitools/pkg/rbac"
)

// The authorization hot path: every /api request evaluates the policy once.
const hotPolicy = `{"roles":{
  "admin":{"resources":"*","actions":["*"]},
  "operator":{"resources":["tasks"],"actions":["read","update"],
              "conditions":{"field":"owner_id","op":"eq","val":"$user_id"}},
  "member":{"permissions":{"tasks":{"actions":["read"],
             "conditions":{"field":"user_id","op":"eq","val":"$user_id"}}}}}}`

func hotPolicyParsed(b *testing.B) *rbac.Policy {
	var p rbac.Policy
	if err := json.Unmarshal([]byte(hotPolicy), &p); err != nil {
		b.Fatal(err)
	}
	return &p
}

func BenchmarkHotPathRoleGlobal(b *testing.B) {
	p := hotPolicyParsed(b)
	ctx := rbac.EvalContext{Role: "operator", UserID: "11111111-1111-1111-1111-111111111111"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.Evaluate(ctx, "tasks", "read").Allowed {
			b.Fatal("denied")
		}
	}
}

func BenchmarkHotPathPerResource(b *testing.B) {
	p := hotPolicyParsed(b)
	ctx := rbac.EvalContext{Role: "member", UserID: "11111111-1111-1111-1111-111111111111"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.Evaluate(ctx, "tasks", "read").Allowed {
			b.Fatal("denied")
		}
	}
}

func BenchmarkHotPathWildcard(b *testing.B) {
	p := hotPolicyParsed(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.Allows("admin", "tasks", "create") {
			b.Fatal("denied")
		}
	}
}
