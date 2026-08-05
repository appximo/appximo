package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/appximo/appximo/pkg/aigen"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/spf13/cobra"
)

// ai-generate is the AI schema-generation loop as a CLI (AI-F0-S3): a
// natural-language description in, a VALID Appximo schema out, with the model
// self-correcting from the engine's own actionable validation errors. It prints
// the ECONOMIC instrumentation (iterations, tokens, approximate cost) that is the
// data point validating the democratization thesis — "a cheap model is enough".
var aiGenerateCmd = &cobra.Command{
	Use:   "ai-generate [description]",
	Short: "Generate a valid Appximo schema from natural language (generate→validate→self-correct AI loop)",
	Long: `Generates an Appximo schema from a natural-language description using an LLM,
then self-corrects it against the engine's own validators until it is VALID (or
the iteration budget is exhausted). This is the AI-F0-S3 democratization loop:
the AI produces bounded, verifiable JSON; the engine guarantees correctness; the
loop converges without a human.

The API key is read from ANTHROPIC_API_KEY (never hardcoded). Without it the
command explains how to set it and exits. The default model is the CHEAP one
(claude-haiku-4-5) — the thesis is that the cheap model is enough.

Examples:
  export ANTHROPIC_API_KEY=sk-ant-...
  appximo ai-generate "un CRM para una óptica: clientes, citas, ventas"
  appximo ai-generate "an e-commerce: products, categories, orders with lines" --out shop.json
  appximo ai-generate "a task board: projects, tasks, statuses" --model claude-sonnet-4-6 --json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		model, _ := cmd.Flags().GetString("model")
		maxIter, _ := cmd.Flags().GetInt("max-iterations")
		out, _ := cmd.Flags().GetString("out")
		jsonOut, _ := cmd.Flags().GetBool("json")
		verbose, _ := cmd.Flags().GetBool("verbose")
		structured, _ := cmd.Flags().GetBool("structured")
		arrayIR, _ := cmd.Flags().GetBool("array-ir")

		client, err := aigen.NewAnthropicClient(model)
		if err != nil {
			if errors.Is(err, aigen.ErrNoAPIKey) {
				fmt.Fprintln(os.Stderr, "Error: ANTHROPIC_API_KEY is not set.")
				fmt.Fprintln(os.Stderr, "  Export it before using ai-generate (never put it in code):")
				fmt.Fprintln(os.Stderr, "    export ANTHROPIC_API_KEY=sk-ant-...")
				os.Exit(2)
			}
			return err
		}

		// AI-F2-S4 — the DEFAULT is the plain validator-guided loop. The real
		// measurement (AI-F2-S3, docs §The first real measurement) showed the cheap
		// model + the validator oracle reach ~90% first-try / 100% convergence /
		// ~$0.006 per schema, and that the constrained-decoding modes do NOT engage on
		// the real Anthropic API (the strict-outputs subset rejects the schema's open
		// objects and >16 union params, so they silently fall back to plain). So they
		// are EXPERIMENTAL opt-ins, kept for measurement + future reuse, not the
		// default. --array-ir implies structured.
		useStructured := (structured || arrayIR) && aigen.SupportsStructuredOutput(client.Model())
		if (structured || arrayIR) && !useStructured {
			fmt.Fprintf(os.Stderr, "Note: structured outputs not supported by %s — using the plain loop (default)\n", client.Model())
			arrayIR = false
		}
		if useStructured {
			fmt.Fprintln(os.Stderr, "Note: structured/array-IR is EXPERIMENTAL — it does NOT engage against the real Anthropic API")
			fmt.Fprintln(os.Stderr, "  (strict-subset limits: open objects + the 16-union-param cap) and falls back to the plain loop.")
		}

		res, err := aigen.Generate(context.Background(), client, args[0], aigen.Options{
			MaxIterations: maxIter,
			Model:         client.Model(),
			NoStructured:  !useStructured,
			ArrayIR:       arrayIR,
		})
		if err != nil {
			return err
		}

		if jsonOut {
			b, _ := json.MarshalIndent(res, "", "  ")
			fmt.Println(string(b))
			if !res.Converged {
				os.Exit(1)
			}
			return nil
		}

		// Human report: the economic instrumentation first, then the schema.
		printReport(res, verbose)

		if out != "" {
			if werr := os.WriteFile(out, res.Schema, 0o644); werr != nil {
				return fmt.Errorf("write %s: %w", out, werr)
			}
			fmt.Fprintf(os.Stderr, "\nSchema written to %s\n", out)
		} else {
			fmt.Println()
			fmt.Println(string(res.Schema))
		}

		if !res.Converged {
			os.Exit(1)
		}
		return nil
	},
}

// printReport writes the cost/convergence summary to stderr (so stdout stays the
// schema when no --out is given).
func printReport(res *aigen.Result, verbose bool) {
	w := os.Stderr
	fmt.Fprintln(w, "── AI schema generation ─────────────────────────")
	if res.Refused {
		fmt.Fprintf(w, "  result:      ⚠ REFUSED by the model (not a schema error)\n")
		fmt.Fprintf(w, "  reason:      %s\n", strings.TrimSpace(res.RefusalText))
		fmt.Fprintf(w, "  model:       %s\n", res.Model)
		fmt.Fprintln(w, "─────────────────────────────────────────────────")
		return
	}
	status := "✗ did NOT converge"
	if res.Converged {
		status = "✓ VALID"
		if res.FirstTry {
			status += " (first try)"
		} else {
			status += " (after correction)"
		}
	}
	decode := "plain loop (validator-guided — the default, AI-F2-S4)"
	switch {
	case res.ArrayIR:
		decode = "array-IR (EXPERIMENTAL; engaged this run)"
	case res.Structured:
		decode = "structured envelope (EXPERIMENTAL; engaged this run)"
	}
	fmt.Fprintf(w, "  result:      %s\n", status)
	fmt.Fprintf(w, "  model:       %s\n", res.Model)
	fmt.Fprintf(w, "  decoding:    %s\n", decode)
	fmt.Fprintf(w, "  iterations:  %d\n", res.Iterations)
	fmt.Fprintf(w, "  tokens:      %d in / %d out", res.Usage.InputTokens, res.Usage.OutputTokens)
	if res.Usage.CacheReadTokens > 0 || res.Usage.CacheCreationTokens > 0 {
		fmt.Fprintf(w, " (cache: %d read @0.1x, %d written)", res.Usage.CacheReadTokens, res.Usage.CacheCreationTokens)
	}
	fmt.Fprintln(w)
	if _, ok := aigen.PricingFor(res.Model); ok {
		fmt.Fprintf(w, "  approx cost: $%.5f USD\n", res.CostUSD)
	} else {
		fmt.Fprintf(w, "  approx cost: (no known pricing for this model)\n")
	}

	if verbose {
		fmt.Fprintln(w, "  rounds:")
		for _, a := range res.Attempts {
			tag := "invalid"
			if a.Valid {
				tag = "valid"
			}
			fmt.Fprintf(w, "    %d. %s — %d structural / %d semantic, %d/%d tokens\n",
				a.Iteration, tag, a.StructuralCount, a.SemanticCount, a.Usage.InputTokens, a.Usage.OutputTokens)
		}
	}

	if !res.Converged && len(res.RemainingErrors) > 0 {
		fmt.Fprintf(w, "  remaining errors (%d):\n", len(res.RemainingErrors))
		for _, e := range res.RemainingErrors {
			fmt.Fprintf(w, "    - %s: %s\n", e.Path, strings.TrimSpace(e.Message))
		}
	}
	// A schema can be valid and still not do what was asked. The validator answers
	// "may this run?"; these answer "will it behave as you meant?" (SCHEMA-5) — and
	// this is the one moment the person who wrote the description is still reading.
	if len(res.Schema) > 0 {
		if rep := schema.ValidateReport(res.Schema); len(rep.Warnings) > 0 {
			fmt.Fprintf(w, "  ⚠ review this (%d) — the schema is valid, but probably does not do what you asked:\n", len(rep.Warnings))
			for _, warn := range rep.Warnings {
				fmt.Fprintf(w, "    - %s\n      %s\n", warn.Path, strings.TrimSpace(warn.Message))
				if warn.Fix != "" {
					fmt.Fprintf(w, "      → %s\n", strings.TrimSpace(warn.Fix))
				}
			}
		}
	}
	fmt.Fprintln(w, "─────────────────────────────────────────────────")
}

func init() {
	aiGenerateCmd.Flags().String("model", aigen.DefaultModel, "model to use (claude-haiku-4-5 | claude-sonnet-4-6 | claude-opus-4-8)")
	aiGenerateCmd.Flags().Int("max-iterations", aigen.DefaultMaxIterations, "maximum generate→correct rounds")
	aiGenerateCmd.Flags().String("out", "", "file to write the valid schema to (default: stdout)")
	aiGenerateCmd.Flags().Bool("json", false, "emit the full result (schema + metrics) as JSON")
	aiGenerateCmd.Flags().Bool("verbose", false, "show per-round detail (structural vs semantic errors)")
	// EXPERIMENTAL opt-ins (default is the plain validator-guided loop, AI-F2-S4).
	// Both fall back to plain on the real Anthropic API (strict-subset limits); kept
	// for measurement (ai-eval) and future reuse (the IR backs the visual editor).
	aiGenerateCmd.Flags().Bool("structured", false, "EXPERIMENTAL: AI-F1-S1 structured-outputs envelope (does not engage against the real API; falls back to plain)")
	aiGenerateCmd.Flags().Bool("array-ir", false, "EXPERIMENTAL: AI-F2-S2 array-IR (does not engage against the real API — 16-union-param cap; falls back to plain)")
	// Deprecated no-op: plain is now the default, so --no-structured is redundant.
	aiGenerateCmd.Flags().Bool("no-structured", false, "(deprecated/no-op: the plain loop is the default now)")
	_ = aiGenerateCmd.Flags().MarkDeprecated("no-structured", "plain is the default now; this flag is a no-op")
	rootCmd.AddCommand(aiGenerateCmd)
}
