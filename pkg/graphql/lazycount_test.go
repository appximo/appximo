package graphql

import (
	"testing"

	gql "github.com/graphql-go/graphql"
)

// TestLazyCountResolvers proves SEC-AUDIT-V2 Hallazgo C plumbing: total /
// total_pages / last are resolved from the memoized COUNT closure stored under
// "__count" only when invoked, and the page math is correct. The COUNT closure is
// NOT called unless one of these resolvers runs (so a list query that selects none
// of them never counts).
func TestLazyCountResolvers(t *testing.T) {
	// countFromSource: absent closure → 0; present → its value.
	if n, err := countFromSource(map[string]any{}); err != nil || n != 0 {
		t.Fatalf("absent __count should yield 0, got %d %v", n, err)
	}
	if n, _ := countFromSource("not-a-map"); n != 0 {
		t.Fatalf("non-map source should yield 0, got %d", n)
	}

	calls := 0
	src := map[string]any{
		"per_page": 10,
		"__count":  func() (int64, error) { calls++; return 25, nil },
	}

	if n, err := countFromSource(src); err != nil || n != 25 {
		t.Fatalf("countFromSource: got %d %v, want 25", n, err)
	}
	if tot, _ := resolveTotal(gql.ResolveParams{Source: src}); tot.(int) != 25 {
		t.Fatalf("resolveTotal: got %v, want 25", tot)
	}
	if tp, _ := resolveTotalPages(gql.ResolveParams{Source: src}); tp.(int) != 3 {
		t.Fatalf("resolveTotalPages: got %v, want 3 (25/10)", tp)
	}
	if calls == 0 {
		t.Fatal("the COUNT closure should have been invoked by the resolvers")
	}

	// Empty result set → total_pages clamps to 1 (never 0).
	empty := map[string]any{"per_page": 10, "__count": func() (int64, error) { return 0, nil }}
	if tp, _ := resolveTotalPages(gql.ResolveParams{Source: empty}); tp.(int) != 1 {
		t.Fatalf("resolveTotalPages on empty set: got %v, want 1", tp)
	}

	// last link is lazy: it calls the "__last" closure (which would run the COUNT).
	lastCalls := 0
	withLast := map[string]any{"__last": func() (any, error) { lastCalls++; return "/graphql?page=3", nil }}
	if v, _ := resolveLastLink(gql.ResolveParams{Source: withLast}); v != "/graphql?page=3" || lastCalls != 1 {
		t.Fatalf("resolveLastLink: got %v (calls=%d)", v, lastCalls)
	}
}
