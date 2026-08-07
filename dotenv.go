package appximo

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv loads a `.env` file from the current working directory into the
// process environment — field report F1: the missing-config message used to say
// "or a .env you source", but the binary never read one, `source` does not
// exist on Windows, and the shell workarounds introduced their own failure
// class (F1-bis: a PowerShell-written BOM glued itself to the first variable
// NAME, visually identical to the right one and broken).
//
// Contract (deliberately boring):
//
//   - The REAL environment always wins: a variable already set is never
//     overridden. `.env` fills gaps only.
//   - A UTF-8 BOM at the start of the file is stripped (F1-bis dies here).
//   - Lines: `KEY=VALUE`, optional `export ` prefix, blank lines and `#`
//     comments ignored, CRLF tolerated, single or double quotes around the
//     value removed (no escape processing — a literal file, not a shell).
//   - No file, or an unreadable file, is a no-op — nothing to load is a valid
//     state, not an error.
//
// It returns the number of variables actually set. The engine CLI calls it
// before every subcommand; ParseServeArgs calls it for consumer binaries — so
// `appximo serve` and a custom backend behave identically. It is exported for
// consumers that build their own flag handling and still want the behavior.
func LoadDotEnv() int {
	f, err := os.Open(".env")
	if err != nil {
		return 0
	}
	defer f.Close() //nolint:errcheck

	loaded := 0
	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			line = strings.TrimPrefix(line, "\uFEFF") // the F1-bis BOM
			first = false
		}
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			continue
		}
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if _, exists := os.LookupEnv(key); exists {
			continue // the environment wins — .env only fills gaps
		}
		if os.Setenv(key, val) == nil {
			loaded++
		}
	}
	return loaded
}
