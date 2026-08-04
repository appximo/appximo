package observability_test

import (
	"net/http"
	"testing"

	"github.com/appximo/appximo/pkg/observability"
)

func TestFilterHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer super-secret")
	h.Set("Cookie", "session=abc123")
	h.Set("X-Admin-Key", "benchadmin")
	h.Set("X-Api-Key", "key123")
	h.Set("X-Auth-Token", "tok123")
	h.Set("Content-Type", "application/json")
	h.Set("User-Agent", "curl/8.5.0")
	h.Add("Accept", "application/json")
	h.Add("Accept", "text/html") // multi-value: only the first is kept

	out := observability.FilterHeaders(h)

	for _, k := range []string{"Authorization", "Cookie", "X-Admin-Key", "X-Api-Key", "X-Auth-Token"} {
		if out[k] != "[Filtered]" {
			t.Errorf("%s should be [Filtered], got %q", k, out[k])
		}
	}
	if out["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q", out["Content-Type"])
	}
	if out["User-Agent"] != "curl/8.5.0" {
		t.Errorf("User-Agent = %q", out["User-Agent"])
	}
	if out["Accept"] != "application/json" {
		t.Errorf("Accept should keep only first value, got %q", out["Accept"])
	}
	// The real secret value must never appear.
	for _, v := range out {
		if v == "Bearer super-secret" || v == "benchadmin" || v == "session=abc123" {
			t.Fatalf("a sensitive value leaked: %q", v)
		}
	}
}
