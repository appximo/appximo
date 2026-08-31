package graphql

import "testing"

// TestIsMutationRequest pins the ENG-60 contract: a GraphQL READ is not a write,
// a mutation is, and anything unreadable counts as a write (conservative).
func TestIsMutationRequest(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"shorthand query", `{"query":"{ guides { data { id } } }"}`, false},
		{"named query", `{"query":"query List { guides { data { id } } }"}`, false},
		{"mutation", `{"query":"mutation { createGuide(input:{code:\"x\"}) { id } }"}`, true},
		{"named mutation", `{"query":"mutation Add { createGuide(input:{code:\"x\"}) { id } }"}`, true},
		{"operationName picks the query", `{"query":"query L { guides { data { id } } } mutation M { deleteGuide(id:\"x\") }","operationName":"L"}`, false},
		{"operationName picks the mutation", `{"query":"query L { guides { data { id } } } mutation M { deleteGuide(id:\"x\") }","operationName":"M"}`, true},
		{"operationName matches nothing", `{"query":"query L { guides { data { id } } }","operationName":"Nope"}`, true},
		{"mixed anonymous document has a mutation", `{"query":"{ guides { data { id } } } mutation { deleteGuide(id:\"x\") }"}`, true},
		{"subscription is not a mutation", `{"query":"subscription { guides { id } }"}`, false},
		{"bad JSON", `{"query": `, true},
		{"empty query", `{"query":""}`, true},
		{"parse error", `{"query":"{ guides { "}`, true},
		{"fragment only", `{"query":"fragment F on Guide { id }"}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsMutationRequest([]byte(c.body)); got != c.want {
				t.Fatalf("IsMutationRequest(%s) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}
