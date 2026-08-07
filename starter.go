package appximo

import _ "embed"

// starterSchema is the canonical first schema (examples/quickstart/schema.json,
// the todo-api the README and QUICKSTART teach) embedded so `appximo up` can
// hand a brand-new project a working schema without network or repo access.
// Single source: the embed IS the example file — they cannot diverge.
//
//go:embed examples/quickstart/schema.json
var starterSchema []byte

// StarterSchema returns the embedded quickstart schema (todo-api): one `tasks`
// resource, two roles. It is what `appximo up` writes to ./schema.json when the
// project has none — a real, valid schema the user is meant to replace.
func StarterSchema() []byte {
	out := make([]byte, len(starterSchema))
	copy(out, starterSchema)
	return out
}
