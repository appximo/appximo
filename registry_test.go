package appitools

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func namedApp(name string) *compiledApp {
	return &compiledApp{name: name, handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(name)) //nolint:errcheck
	})}
}

// With no domain table (the S2 single-app shape), EVERY Host resolves to the
// default app — today's "any Host reaches the engine" contract, preserved.
func TestRegistrySingleAppResolvesEveryHost(t *testing.T) {
	def := namedApp("boot")
	r := NewRegistry(def, nil)
	for _, host := range []string{
		"acme.localhost", "acme.localhost:8080", "localhost", "127.0.0.1:8080",
		"[::1]:8080", "weird.example.com", "ACME.LOCALHOST",
	} {
		if got := r.Resolve(host); got != def {
			t.Fatalf("host %q resolved to %v, want the default app", host, got)
		}
	}
}

func TestRegistryDomainRouting(t *testing.T) {
	crm, shop, def := namedApp("crm"), namedApp("shop"), namedApp("def")
	r := NewRegistry(def, map[string]*compiledApp{
		"crm.example.com": crm,
		"example.com":     shop, // shorter suffix — must NOT shadow crm.example.com
		"tienda.local":    shop,
	})
	cases := []struct {
		host string
		want *compiledApp
	}{
		{"crm.example.com", crm},           // exact
		{"acme.crm.example.com", crm},      // tenant subdomain
		{"acme.crm.example.com:8443", crm}, // port stripped
		{"ACME.CRM.EXAMPLE.COM", crm},      // Host is case-insensitive
		{"api.crm.example.com", crm},       // longest domain wins over example.com
		{"example.com", shop},              // the shorter domain still works
		{"other.example.com", shop},        // subdomain of the shorter one
		{"tienda.local", shop},             // alias domain
		{"nadie.example.org", def},         // unmatched → default (S2 contract)
		{"[2001:db8::1]:8080", def},        // IPv6 literal → default
	}
	for _, tc := range cases {
		if got := r.Resolve(tc.host); got != tc.want {
			t.Fatalf("host %q → app %q, want %q", tc.host, got.name, tc.want.name)
		}
	}
}

// The hot path must not allocate: the single-app resolve (the S2 shipped
// shape) and the domain-walk resolve are both zero-alloc for lowercase hosts.
func TestRegistryResolveZeroAlloc(t *testing.T) {
	single := NewRegistry(namedApp("boot"), nil)
	multi := NewRegistry(namedApp("def"), map[string]*compiledApp{
		"crm.example.com": namedApp("crm"),
	})
	if n := testing.AllocsPerRun(1000, func() { single.Resolve("acme.localhost:8080") }); n != 0 {
		t.Fatalf("single-app Resolve allocates %.1f/op, want 0", n)
	}
	if n := testing.AllocsPerRun(1000, func() { multi.Resolve("acme.crm.example.com:8080") }); n != 0 {
		t.Fatalf("domain Resolve allocates %.1f/op, want 0", n)
	}
	if n := testing.AllocsPerRun(1000, func() { multi.Resolve("unmatched.example.org") }); n != 0 {
		t.Fatalf("unmatched Resolve allocates %.1f/op, want 0", n)
	}
}

func TestRegistryServeHTTPDispatches(t *testing.T) {
	crm, def := namedApp("crm"), namedApp("def")
	r := NewRegistry(def, map[string]*compiledApp{"crm.local": crm})
	for host, want := range map[string]string{
		"acme.crm.local": "crm",
		"whatever.local": "def",
	} {
		req := httptest.NewRequest("GET", "http://placeholder/x", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Body.String() != want {
			t.Fatalf("host %q served by %q, want %q", host, rec.Body.String(), want)
		}
	}
}

// BenchmarkRegistryResolve measures the S2 hot-path addition in isolation:
// the shipped single-app shape and the future domain walks.
func BenchmarkRegistryResolve(b *testing.B) {
	b.Run("single-app", func(b *testing.B) {
		r := NewRegistry(namedApp("boot"), nil)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			r.Resolve("acme.localhost:8080")
		}
	})
	for _, n := range []int{4, 50, 500} {
		domains := make(map[string]*compiledApp, n)
		for i := 0; i < n; i++ {
			domains[fmt.Sprintf("app%03d.example.com", i)] = namedApp("x")
		}
		r := NewRegistry(namedApp("def"), domains)
		b.Run(fmt.Sprintf("domains-%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				r.Resolve("tenant.app007.example.com:8080")
			}
		})
	}
}
