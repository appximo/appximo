//go:build integration

package appximo

// The overhead of the self-monitor on the REQUEST PATH, stated on the proxy
// that resolves a 1 % (A-54): allocations and bytes per operation of a full
// generated read through the real middleware chain, collector ON vs OFF.
//
//	go test -tags integration -run '^$' -bench 'Request_SelfMon' -benchmem -count 10 . > on-off.txt
//	benchstat -col /SelfMon on-off.txt
//
// Two sub-benchmarks in one function so benchstat compares them as columns of
// the same table. The collector's OWN tick is benchmarked in pkg/observability
// (BenchmarkResourceCollector_Tick).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/tests/helpers"
)

func BenchmarkRequest_SelfMon(b *testing.B) {
	ctx := context.Background()
	quickstart := filepath.Join(helpers.RepoRoot(), "examples", "quickstart", "schema.json")
	s, err := schema.LoadFromFile(quickstart)
	if err != nil {
		b.Fatal(err)
	}
	// One tenant, seeded once, shared by both arms (the data path is identical).
	if _, err := controlplane.RegisterTenant(ctx, itPool, controlplane.RegisterRequest{
		TenantID: "smbench", DisplayName: "smbench", Email: "b@x.co", Plan: "free", Schema: s,
	}); err != nil && !isAlreadyRegistered(err) {
		b.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		_, _ = itPool.Exec(ctx, `INSERT INTO tenant_smbench.tasks (title, status) VALUES ($1, 'open')`, fmt.Sprintf("bench %d", i))
	}
	token, err := auth.GenerateToken(auth.Claims{UserID: "11111111-1111-4111-8111-111111111111", Role: "admin", TenantID: "smbench"}, helpers.JWTSecret)
	if err != nil {
		b.Fatal(err)
	}

	for _, arm := range []struct {
		name string
		off  bool
	}{{"on", false}, {"off", true}} {
		b.Run(arm.name, func(b *testing.B) {
			app, err := New(Config{
				SchemaPath: quickstart, DSN: itConnStr, JWTSecret: helpers.JWTSecret, AdminKey: helpers.AdminKey, Env: "test",
				SelfMonDisabled: arm.off,
			})
			if err != nil {
				b.Fatal(err)
			}
			defer app.pool.Close()
			if !arm.off {
				cctx, cancel := context.WithCancel(ctx)
				defer cancel()
				go app.selfmon.Run(cctx) // the collector ticks in the background, as in production
			}
			srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
			defer srv.Close()
			client := srv.Client()
			do := func() {
				req, _ := http.NewRequest("GET", srv.URL+"/api/tasks?per_page=20", nil)
				req.Host = "smbench.localhost"
				req.Header.Set("Authorization", "Bearer "+token)
				// Bypass the response cache so every request runs the full chain
				// (the cache would turn the arm into a memory read after the first).
				req.Header.Set("Cache-Control", "no-cache")
				res, err := client.Do(req)
				if err != nil {
					b.Fatal(err)
				}
				io.Copy(io.Discard, res.Body) //nolint:errcheck
				res.Body.Close()
				if res.StatusCode != 200 {
					b.Fatalf("status %d", res.StatusCode)
				}
			}
			do()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				do()
			}
		})
	}
}

func isAlreadyRegistered(err error) bool {
	var je interface{ Error() string } = err
	return je != nil && (contains(je.Error(), "already") || contains(je.Error(), "exists"))
}

func contains(s, sub string) bool { return len(sub) > 0 && len(s) >= len(sub) && (func() bool { _, err := json.Marshal(s); return err == nil })() && indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
