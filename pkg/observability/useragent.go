package observability

import "strings"

// ParseUserAgent extracts a coarse browser and OS from a User-Agent string using
// stdlib-only substring matching — no external dependency, ~1µs. Unknown values
// fall back to "Unknown".
//
// Note: Android UAs also contain "Linux" (Android is Linux-based) and iOS UAs
// contain "like Mac OS X", so Android/iOS are matched BEFORE Linux/macOS.
func ParseUserAgent(ua string) (browser, os string) {
	switch {
	case strings.Contains(ua, "Windows NT 10"):
		os = "Windows 10"
	case strings.Contains(ua, "Windows NT 11"):
		os = "Windows 11"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		os = "iOS"
	case strings.Contains(ua, "Mac OS X"):
		os = "macOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	default:
		os = "Unknown"
	}

	switch {
	case strings.Contains(ua, "Edg/"): // Edge UAs also contain "Chrome/" → check first
		browser = "Edge"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/") && !strings.Contains(ua, "Chrome"):
		browser = "Safari"
	case strings.Contains(ua, "curl"):
		browser = "curl"
	case strings.Contains(ua, "k6"):
		browser = "k6"
	default:
		browser = "Unknown"
	}
	return
}
