package main

import (
	"fmt"
	"os"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/spf13/cobra"
)

// explain is the READ-BACK step of the authoring loop (PHASE4-FIRST-MILE-S1):
// `validate` answers "may this run?", the SCHEMA-5 warnings answer "will it do
// what you meant?" — and this answers "what does it actually say?", in prose the
// app's OWNER can read. It exists so a non-programmer whose schema was written
// by an AI (or a contractor) can confirm the model matches what they asked for,
// deterministically — the text is derived from the parsed schema, never guessed.
var explainCmd = &cobra.Command{
	Use:   "explain [file]",
	Short: "Explain a schema in plain language — what the app manages, its rules, and who can do what",
	Long: `Reads a schema and describes it in plain prose: the kinds of records it
manages, each field's rules in words, record lifecycles (state machines), how
records relate to each other, and what every role is allowed to see and do.

It is the read-back step of the authoring loop: after an AI (or anyone) writes
your schema, this is how the person who ASKED for the app confirms it models
what they meant — without reading JSON.

The schema is validated first; an invalid schema is reported, not explained
(the explanation of a broken schema would itself be broken).

  appximo explain schema.json
  appximo explain schema.json --lang es`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		lang, _ := cmd.Flags().GetString("lang")
		s, err := schema.LoadFromFile(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if errs := schema.Validate(s); len(errs) > 0 {
			fmt.Fprintln(os.Stderr, "The schema is not valid — fix it first (appximo validate), then explain it:")
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, " ", e.Error())
			}
			os.Exit(1)
		}
		fmt.Print(schema.Explain(s, lang))
	},
}

func init() {
	explainCmd.Flags().String("lang", "en", "output language: en | es")
	rootCmd.AddCommand(explainCmd)
}
