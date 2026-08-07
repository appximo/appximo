package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo"
)

// FIELD-FEEDBACK-S1 (Part G): the fourth build-side doc. `spec` teaches the
// schema, `backend-spec` the handlers, `frontend-spec` the UI contract — this
// one teaches generating a complete admin CRUD UI from /openapi.json alone
// (tables, forms, validation, permissions by 403-probing), with zero
// resource-specific screens and, since the x-appximo-* extensions, zero
// hardcoded domain knowledge. Distilled from the field evaluation's real,
// verified implementation. Runnable proof: examples/backoffice-guide/.
var backofficeSpecCmd = &cobra.Command{
	Use:   "backoffice-spec",
	Short: "Print the back-office contract: a CRUD admin UI generated from /openapi.json",
	Long: `Print the back-office contract (Markdown, on stdout).

The pattern: one contract reader (~150 lines) turns /openapi.json into tables,
forms, menus and permission-aware navigation for EVERY resource — including
ones added after you shipped. Covers the reader, the field→control mapping,
permission probing via 403, the five generic-form rules (omit empty fields on
create, PATCH partial, null clears, paint the whole 422, offer only legal
state moves), relation selectors that honor x-appximo-references, file
widgets, the overrides registry, and the honest limits.

Single source: docs/BACKOFFICE_SPEC_LLM.md (embedded). Working example:
examples/backoffice-guide/.`,
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Println(appximo.BackofficeSpec())
	},
}

func init() {
	rootCmd.AddCommand(backofficeSpecCmd)
}
