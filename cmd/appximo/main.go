package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo"
)

var rootCmd = &cobra.Command{
	Use:   "appximo",
	Short: "API engine for Go — generate production APIs from a JSON schema",
	// F1: a `.env` in the working directory is loaded for EVERY subcommand
	// (the real environment always wins; a BOM is stripped — F1-bis). Silent
	// here so --json commands keep byte-clean output; `serve` announces what
	// it loaded in its boot log.
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		dotenvLoaded = appximo.LoadDotEnv()
	},
	Long: `API engine for Go — generate production APIs from a JSON schema.

First time here? ONE command starts everything locally:

  appximo up               Postgres + secrets + tenant + admin + server, in one go
  appximo new "<idea>"     same, with the schema AI-generated from your idea

Have an AI agent (Claude Code, Cursor, Copilot…)? Then start here instead:

  appximo prompt           THE prompt: paste it into your agent → from an idea
                           to production with HTTPS, one question block, zero
                           questions after it

The engine also prints the deeper contracts the prompt draws on — paste these
individually when the task is only one layer:

  appximo spec             the schema grammar (the declarative 90%)
  appximo backend-spec     custom Go handlers, hooks, auth, background jobs
  appximo frontend-spec    the API contract a UI consumes, errors→screens, files
  appximo backoffice-spec  a CRUD admin UI generated from /openapi.json
  appximo quickstart       OPERATING it: install → tenant → users → production
  appximo specs            all five at once (one paste = the whole contract)

The agent self-corrects with 'appximo validate --json <schema>' as the oracle.`,
}

// dotenvLoaded is how many variables the .env fill-in actually set this run —
// server commands announce it in their boot log (machine-output commands don't).
var dotenvLoaded int

func main() {
	// Cobra would print the error itself AND main would print it again; with
	// the version hint attached that doubling is loud, so the CLI owns the
	// single rendering here.
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", annotateUnknownCommand(err))
		os.Exit(1)
	}
}

// annotateUnknownCommand is ADR-024 on the VERSION axis (INSTALL-PROMPT-S1).
//
// `appximo prompt` on a v0.1.2 binary answered a bare `unknown command
// "prompt"`, which is indistinguishable from a typo — and the real cause (an
// old binary, the most common state once releases exist) went unnamed. The fix
// deliberately does NOT introduce a catalogue of "command X exists since vY":
// that table would be one more thing to forget to update, and it would be
// wrong in exactly the binaries too old to contain it. Instead the message
// names the OTHER possibility and the two commands that settle it in one line
// each — true forever, for every command, with nothing to maintain.
func annotateUnknownCommand(err error) error {
	if err == nil || !strings.HasPrefix(err.Error(), "unknown command") {
		return err
	}
	return fmt.Errorf(`%w
Run 'appximo --help' for the commands this binary has.

If that command is not a typo, this binary may simply be too old to have it —
new commands ship with new releases, and an already-installed appximo is the
most common reason a documented command "does not exist".

  appximo version    what you have, and whether a newer release exists
  appximo upgrade    replace this binary with the newest one`, err)
}
