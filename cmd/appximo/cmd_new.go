package main

// appximo new — from an idea to a running app (ENG-38, FEEDBACK.md §13):
// ai-generate (the validator-guided loop that already exists) → schema.json →
// the same orchestration as `appximo up`. No piece is new; this is the wiring.
//
// Without ANTHROPIC_API_KEY it does NOT fail: it prints the prompt the user
// pastes into THEIR OWN agent (Claude Code / Cursor — the Quick Start's track
// B), which generates the schema on their subscription, plus the exact `up`
// command to run next. The command's job either way is to leave the user one
// step from a running app.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo/pkg/aigen"
	"github.com/appximo/appximo/pkg/controlplane"
)

var newCmd = &cobra.Command{
	Use:   "new \"<your app idea in one sentence>\"",
	Short: "From an idea to a running app: AI-generate the schema, then `up`",
	Long: `Generates an Appximo schema from your idea (the ai-generate validator-guided
loop, self-correcting until valid), writes it to ./schema.json, and hands it to
` + "`appximo up`" + ` — Postgres, secrets, tenant, first admin, server, in one flow.

  appximo new "class bookings for a gym"
  appximo new "un inventario para una ferretería" --name ferreteria

The AI step needs ANTHROPIC_API_KEY. WITHOUT it the command still helps: it
prints a ready-to-paste prompt for your own coding agent (Claude Code, Cursor)
that produces schema.json with 'appximo validate --json' as the oracle, and
the exact 'appximo up' command to run when it is done.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		idea := strings.TrimSpace(args[0])
		name, _ := cmd.Flags().GetString("name")
		model, _ := cmd.Flags().GetString("model")
		maxIter, _ := cmd.Flags().GetInt("max-iterations")
		if name == "" {
			name = nameFromIdea(idea)
		}
		if !controlplane.ValidTenantID(name) {
			fmt.Fprintf(os.Stderr, "appximo new: app name %q is not a valid tenant id (lowercase letters + digits, starting with a letter)\n", name)
			if s := controlplane.SuggestTenantID(name); s != "" {
				fmt.Fprintf(os.Stderr, "  try: --name %s\n", s)
			}
			os.Exit(1)
		}

		if _, err := os.Stat("schema.json"); err == nil {
			fmt.Fprintln(os.Stderr, "appximo new: ./schema.json already exists — refusing to overwrite your schema.")
			fmt.Fprintln(os.Stderr, "  - to serve the existing schema:  appximo up --name "+name)
			fmt.Fprintln(os.Stderr, "  - to regenerate from the idea:   move it aside first (mv schema.json schema.json.bak)")
			os.Exit(1)
		}

		client, err := aigen.NewAnthropicClient(model)
		if errors.Is(err, aigen.ErrNoAPIKey) {
			printAgentPrompt(idea, name)
			return
		} else if err != nil {
			fmt.Fprintln(os.Stderr, "appximo new:", err)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "  … generating the schema from your idea (%s; self-correcting against the validator)\n", client.Model())
		res, err := aigen.Generate(context.Background(), client, idea, aigen.Options{
			MaxIterations: maxIter, Model: client.Model(), NoStructured: true,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "appximo new: generation failed:", err)
			os.Exit(1)
		}
		printReport(res, false) // the economic instrumentation (stderr), same as ai-generate
		if !res.Converged {
			fmt.Fprintln(os.Stderr, "appximo new: the schema did not converge to valid — nothing written.")
			fmt.Fprintln(os.Stderr, "  Re-run (results vary), simplify the idea, or write schema.json by hand (appximo spec is the grammar).")
			os.Exit(1)
		}
		if err := os.WriteFile("schema.json", res.Schema, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "appximo new: write schema.json:", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "  ✓ schema written to ./schema.json — starting everything (appximo up)")

		opts := upOptions{SchemaPath: "schema.json", Name: name}
		opts.Port, _ = cmd.Flags().GetInt("port")
		opts.ControlPort, _ = cmd.Flags().GetInt("control-port")
		opts.PGImage, _ = cmd.Flags().GetString("pg-image")
		opts.PGContainer, _ = cmd.Flags().GetString("pg-container")
		opts.PGPort, _ = cmd.Flags().GetInt("pg-port")
		opts.NoDocker, _ = cmd.Flags().GetBool("no-docker")
		opts.JSON, _ = cmd.Flags().GetBool("json")
		opts.Yes, _ = cmd.Flags().GetBool("yes")
		if err := runUp(opts); err != nil {
			fmt.Fprintln(os.Stderr, "appximo new:", err)
			os.Exit(1)
		}
	},
}

// printAgentPrompt is the no-API-key path: the §13 prompt with the idea and
// name filled in, ready to paste into the user's own agent. Exit 0 — the
// command did its job (the user is one paste + one command from the app).
func printAgentPrompt(idea, name string) {
	fmt.Printf(`No ANTHROPIC_API_KEY set — no problem. Paste this into your own coding agent
(Claude Code, Cursor, …); it will write the schema on your subscription:

────────────────────────────────────────────────────────────────────
Build an Appximo schema for: %s

1. Run `+"`appximo spec`"+` and read it — it is the complete schema grammar.
2. Write schema.json for the idea above (resources, fields with validation,
   relations, state machines where steps exist, and an rbac block with an
   "admin" role).
3. Correct it with `+"`appximo validate --json schema.json`"+` until it prints
   "valid": true. Warnings deserve a look too.
4. Then run:  appximo up --name %s --schema schema.json --yes --json
   and give me: every URL it prints (/app /docs /admin /editor), the
   credentials, and one curl that ALREADY WORKS against a record you create.

Success = /docs answers 200, that curl answers 201, and /app lists the record.
────────────────────────────────────────────────────────────────────

Prefer the built-in loop? Set the key and re-run:
  export ANTHROPIC_API_KEY=sk-ant-...   (uses claude-haiku-4-5, ~$0.01/schema)
  appximo new "%s"
`, idea, name, idea)
}

// nameFromIdea derives a tenant-valid app name from the idea: the first word
// that survives the tenant-id rule and is not a stopword; "app" otherwise.
func nameFromIdea(idea string) string {
	stop := map[string]bool{
		"a": true, "an": true, "the": true, "for": true, "of": true, "with": true, "and": true,
		"un": true, "una": true, "el": true, "la": true, "los": true, "las": true, "de": true,
		"del": true, "para": true, "con": true, "y": true, "en": true, "que": true, "app": true,
	}
	for _, w := range strings.Fields(strings.ToLower(idea)) {
		s := controlplane.SuggestTenantID(w)
		if len(s) >= 3 && !stop[s] {
			return s
		}
	}
	return "app"
}

func init() {
	newCmd.Flags().String("name", "", "app / tenant name (default: derived from the idea)")
	newCmd.Flags().String("model", aigen.DefaultModel, "model for the AI generation")
	newCmd.Flags().Int("max-iterations", aigen.DefaultMaxIterations, "maximum generate→correct rounds")
	newCmd.Flags().Int("port", 8080, "HTTP port for the app")
	newCmd.Flags().Int("control-port", 0, "control-plane port (0 = APPXIMO_CONTROL_PORT, then 9090)")
	newCmd.Flags().String("pg-image", "postgres:16", "Postgres image for the Docker path")
	newCmd.Flags().String("pg-container", "appximo-pg", "Docker container name for Postgres")
	newCmd.Flags().Int("pg-port", 54329, "host port for the Docker Postgres (loopback-only)")
	newCmd.Flags().Bool("no-docker", false, "never start Docker; require DATABASE_URL")
	newCmd.Flags().Bool("json", false, "print `up`'s final card as ONE JSON object on stdout")
	newCmd.Flags().Bool("yes", false, "no questions: accept every default")
	rootCmd.AddCommand(newCmd)
}
