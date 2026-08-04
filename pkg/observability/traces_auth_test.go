package observability_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/appximo/appximo/pkg/observability"
)

// The /debug/traces HTML route accepts auth via ?key= OR X-Admin-Key (so it opens
// directly in a browser), while the JSON debug APIs stay header-only.
func TestDebugRouter_TracesQueryParamAuth(t *testing.T) {
	const key = "secret-xyz"
	obs := observability.NewObsServer(nil, nil, nil, nil, nil, nil, nil)
	// Mirror the handler injected by cmd_serve: query param OR header.
	obs.SetTracesHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := r.URL.Query().Get("key")
		if k == "" {
			k = r.Header.Get("X-Admin-Key")
		}
		if k != key {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<html>ok</html>")) //nolint:errcheck
	}))

	srv := httptest.NewServer(obs.DebugRouter(key))
	defer srv.Close()

	get := func(path string, hdr map[string]string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	// /traces — the three required cases plus header still works.
	if c := get("/traces?key="+key, nil); c != http.StatusOK {
		t.Errorf("/traces?key=correct = %d, want 200", c)
	}
	if c := get("/traces?key=wrong", nil); c != http.StatusUnauthorized {
		t.Errorf("/traces?key=wrong = %d, want 401", c)
	}
	if c := get("/traces", nil); c != http.StatusUnauthorized {
		t.Errorf("/traces (no key) = %d, want 401", c)
	}
	if c := get("/traces", map[string]string{"X-Admin-Key": key}); c != http.StatusOK {
		t.Errorf("/traces with header = %d, want 200", c)
	}

	// JSON APIs must NOT accept the query param — header only. With no header this
	// is rejected by the admin group before the (nil-field) handler runs.
	if c := get("/tenant/10?key="+key, nil); c != http.StatusUnauthorized {
		t.Errorf("/tenant/10?key= must be 401 (query param not accepted for APIs), got %d", c)
	}
	if c := get("/synthetic?key="+key, nil); c != http.StatusUnauthorized {
		t.Errorf("/synthetic?key= must be 401 (query param not accepted for APIs), got %d", c)
	}
}
