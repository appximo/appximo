package appximo

import "github.com/appximo/appximo/pkg/dotenv"

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
	return dotenv.Load()
}
