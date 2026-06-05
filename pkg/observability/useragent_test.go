package observability_test

import (
	"testing"

	"github.com/miguelangel/appitools/pkg/observability"
)

func TestParseUserAgent(t *testing.T) {
	cases := []struct {
		name, ua, wantBrowser, wantOS string
	}{
		{"chrome windows10",
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36",
			"Chrome", "Windows 10"},
		{"edge windows10",
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36 Edg/148.0.0.0",
			"Edge", "Windows 10"},
		{"firefox linux",
			"Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0",
			"Firefox", "Linux"},
		{"safari ios",
			"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
			"Safari", "iOS"},
		{"chrome android",
			"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Mobile Safari/537.36",
			"Chrome", "Android"},
		{"safari macos",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
			"Safari", "macOS"},
		{"curl", "curl/8.5.0", "curl", "Unknown"},
		{"k6", "k6/0.55.0 (https://k6.io/)", "k6", "Unknown"},
		{"empty", "", "Unknown", "Unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, o := observability.ParseUserAgent(c.ua)
			if b != c.wantBrowser {
				t.Errorf("browser = %q, want %q", b, c.wantBrowser)
			}
			if o != c.wantOS {
				t.Errorf("os = %q, want %q", o, c.wantOS)
			}
		})
	}
}
