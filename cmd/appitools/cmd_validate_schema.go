package main

import (
	"fmt"
	"os"

	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/spf13/cobra"
)

// validate-schema validates a schema file against the formal JSON Schema
// meta-schema (AI-F0-S1) — the STRUCTURAL net: types, enums, name patterns,
// allowed keys, required fields, the two RBAC forms. It is engine-free and fast,
// the deterministic check an AI (or an IDE) runs to verify a generated schema is
// structurally valid. `appitools validate` remains the SEMANTIC authority (it also
// checks cross-references the meta-schema cannot express).
var validateSchemaCmd = &cobra.Command{
	Use:   "validate-schema <schema.json>",
	Short: "Validate a schema against the formal JSON Schema meta-schema (structural)",
	Long: `Validates schema.json against the embedded JSON Schema (Draft 2020-12) meta-schema.

This is the STRUCTURAL layer — it checks types, enums, identifier patterns, allowed
keys (strict), required fields, and the two RBAC forms, without booting the engine.
It is the deterministic net for AI-generated schemas (structurally valid JSON, verified
instantly). The SEMANTIC authority is ` + "`appitools validate`" + `, which additionally
checks cross-references the meta-schema cannot express (a relation/FK target or
references column must EXIST and be unique on the target; a condition/allowlist field
must exist on the resource; renamed_from must not still be declared; …).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		raw, err := os.ReadFile(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error reading schema:", err)
			os.Exit(1)
		}
		errs := schema.ValidateAgainstMetaSchema(raw)
		if len(errs) == 0 {
			fmt.Println("Schema structurally valid ✓ (meta-schema) — run `appitools validate` for semantic checks")
			return
		}
		fmt.Fprintln(os.Stderr, "Structural errors (meta-schema):")
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, " ", e.Error())
		}
		os.Exit(1)
	},
}

// meta-schema prints the embedded JSON Schema meta-schema to stdout, for IDE
// integration ($schema reference), external tooling, or piping to an AI.
var metaSchemaCmd = &cobra.Command{
	Use:   "meta-schema",
	Short: "Print the formal JSON Schema (Draft 2020-12) meta-schema to stdout",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		os.Stdout.Write(schema.MetaSchemaJSON())
	},
}

func init() {
	rootCmd.AddCommand(validateSchemaCmd)
	rootCmd.AddCommand(metaSchemaCmd)
}
