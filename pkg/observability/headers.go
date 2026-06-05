package observability

import (
	"net/http"
	"strings"
)

// sensitiveHeaders are request headers whose values must never be stored or
// shown (they carry credentials). Their values are replaced with "[Filtered]".
var sensitiveHeaders = map[string]bool{
	"authorization": true,
	"cookie":        true,
	"set-cookie":    true,
	"x-api-key":     true,
	"x-admin-key":   true,
	"x-auth-token":  true,
}

// FilterHeaders flattens an http.Header into a name→value map, redacting the
// values of sensitive (credential) headers to "[Filtered]". Only the first value
// of each header is kept. Called only when a trace is persisted (off the hot
// path), so its allocation never taxes the 200-OK common case.
func FilterHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vals := range h {
		val := ""
		if len(vals) > 0 {
			val = vals[0]
		}
		if sensitiveHeaders[strings.ToLower(k)] {
			val = "[Filtered]"
		}
		out[k] = val
	}
	return out
}
