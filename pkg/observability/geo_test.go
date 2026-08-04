package observability_test

import (
	"testing"

	"github.com/appximo/appximo/pkg/observability"
)

func TestGeoLookup_Country(t *testing.T) {
	g := observability.DefaultGeoLookup() // embedded GeoLite2
	cases := map[string]string{
		"8.8.8.8":     "US",
		"190.85.0.1":  "CO", // Colombian range
		"201.244.0.1": "CO",
		"127.0.0.1":   "", // loopback → not in db
		"10.0.0.1":    "", // private
		"not-an-ip":   "",
		"":            "",
	}
	for ip, want := range cases {
		if got := g.Country(ip); got != want {
			t.Errorf("Country(%q) = %q, want %q", ip, got, want)
		}
	}
}

// A lookup whose database failed to load (or a nil receiver) must degrade
// gracefully: "" for everything, no panic.
func TestGeoLookup_Graceful(t *testing.T) {
	if got := observability.NewGeoLookup(nil).Country("8.8.8.8"); got != "" {
		t.Errorf("nil-data lookup should return \"\", got %q", got)
	}
	if got := observability.NewGeoLookup([]byte("not a valid mmdb")).Country("8.8.8.8"); got != "" {
		t.Errorf("invalid-data lookup should return \"\", got %q", got)
	}
	var nilLookup *observability.GeoLookup
	if got := nilLookup.Country("8.8.8.8"); got != "" {
		t.Errorf("nil *GeoLookup should return \"\", got %q", got)
	}
}
