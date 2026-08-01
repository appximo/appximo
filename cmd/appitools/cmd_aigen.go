package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/miguelangel/appitools/pkg/aigen"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/spf13/cobra"
)

// ai-generate is the AI schema-generation loop as a CLI (AI-F0-S3): a
// natural-language description in, a VALID Appitools schema out, with the model
// self-correcting from the engine's own actionable validation errors. It prints
// the ECONOMIC instrumentation (iterations, tokens, approximate cost) that is the
// data point validating the democratization thesis — "a cheap model is enough".
var aiGenerateCmd = &cobra.Command{
	Use:   "ai-generate [descripción]",
	Short: "Genera un schema válido de Appitools desde lenguaje natural (loop generate→validate→corregir con IA)",
	Long: `Generates an Appitools schema from a natural-language description using an LLM,
then self-corrects it against the engine's own validators until it is VALID (or
the iteration budget is exhausted). This is the AI-F0-S3 democratization loop:
the AI produces bounded, verifiable JSON; the engine guarantees correctness; the
loop converges without a human.

The API key is read from ANTHROPIC_API_KEY (never hardcoded). Without it the
command explains how to set it and exits. The default model is the CHEAP one
(claude-haiku-4-5) — the thesis is that the cheap model is enough.

Examples:
  export ANTHROPIC_API_KEY=sk-ant-...
  appitools ai-generate "un CRM para una óptica: clientes, citas, ventas"
  appitools ai-generate "an e-commerce: products, categories, orders with lines" --out shop.json
  appitools ai-generate "a task board: projects, tasks, statuses" --model claude-sonnet-4-6 --json`,
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
				fmt.Fprintln(os.Stderr, "Error: ANTHROPIC_API_KEY no está configurada.")
				fmt.Fprintln(os.Stderr, "  Exportala antes de usar ai-generate (nunca la pongas en el código):")
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
			fmt.Fprintf(os.Stderr, "Aviso: structured outputs no soportado por %s — usando el loop plano (default)\n", client.Model())
			arrayIR = false
		}
		if useStructured {
			fmt.Fprintln(os.Stderr, "Aviso: structured/array-IR es EXPERIMENTAL — NO engancha contra la API real de Anthropic")
			fmt.Fprintln(os.Stderr, "  (límites del subset strict: objetos abiertos + tope de 16 params union) y cae al loop plano.")
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
			fmt.Fprintf(os.Stderr, "\nSchema escrito en %s\n", out)
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
		fmt.Fprintf(w, "  resultado:   ⚠ RECHAZADO por el modelo (no es un error de schema)\n")
		fmt.Fprintf(w, "  motivo:      %s\n", strings.TrimSpace(res.RefusalText))
		fmt.Fprintf(w, "  modelo:      %s\n", res.Model)
		fmt.Fprintln(w, "─────────────────────────────────────────────────")
		return
	}
	status := "✗ NO convergió"
	if res.Converged {
		status = "✓ VÁLIDO"
		if res.FirstTry {
			status += " (a la primera)"
		} else {
			status += " (tras corrección)"
		}
	}
	decode := "plain loop (validator-guided — the default, AI-F2-S4)"
	switch {
	case res.ArrayIR:
		decode = "array-IR (EXPERIMENTAL; engaged this run)"
	case res.Structured:
		decode = "structured envelope (EXPERIMENTAL; engaged this run)"
	}
	fmt.Fprintf(w, "  resultado:   %s\n", status)
	fmt.Fprintf(w, "  modelo:      %s\n", res.Model)
	fmt.Fprintf(w, "  decoding:    %s\n", decode)
	fmt.Fprintf(w, "  iteraciones: %d\n", res.Iterations)
	fmt.Fprintf(w, "  tokens:      %d in / %d out", res.Usage.InputTokens, res.Usage.OutputTokens)
	if res.Usage.CacheReadTokens > 0 || res.Usage.CacheCreationTokens > 0 {
		fmt.Fprintf(w, " (cache: %d leídos @0.1x, %d escritos)", res.Usage.CacheReadTokens, res.Usage.CacheCreationTokens)
	}
	fmt.Fprintln(w)
	if _, ok := aigen.PricingFor(res.Model); ok {
		fmt.Fprintf(w, "  costo aprox: $%.5f USD\n", res.CostUSD)
	} else {
		fmt.Fprintf(w, "  costo aprox: (modelo sin tarifa conocida)\n")
	}

	if verbose {
		fmt.Fprintln(w, "  rondas:")
		for _, a := range res.Attempts {
			tag := "inválido"
			if a.Valid {
				tag = "válido"
			}
			fmt.Fprintf(w, "    %d. %s — %d estructurales / %d semánticos, %d/%d tokens\n",
				a.Iteration, tag, a.StructuralCount, a.SemanticCount, a.Usage.InputTokens, a.Usage.OutputTokens)
		}
	}

	if !res.Converged && len(res.RemainingErrors) > 0 {
		fmt.Fprintf(w, "  errores restantes (%d):\n", len(res.RemainingErrors))
		for _, e := range res.RemainingErrors {
			fmt.Fprintf(w, "    - %s: %s\n", e.Path, strings.TrimSpace(e.Message))
		}
	}
	// A schema can be valid and still not do what was asked. The validator answers
	// "may this run?"; these answer "will it behave as you meant?" (SCHEMA-5) — and
	// this is the one moment the person who wrote the description is still reading.
	if len(res.Schema) > 0 {
		if rep := schema.ValidateReport(res.Schema); len(rep.Warnings) > 0 {
			fmt.Fprintf(w, "  ⚠ revisá esto (%d) — el schema es válido, pero probablemente no hace lo que pediste:\n", len(rep.Warnings))
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
	aiGenerateCmd.Flags().String("model", aigen.DefaultModel, "modelo a usar (claude-haiku-4-5 | claude-sonnet-4-6 | claude-opus-4-8)")
	aiGenerateCmd.Flags().Int("max-iterations", aigen.DefaultMaxIterations, "máximo de rondas generate→corregir")
	aiGenerateCmd.Flags().String("out", "", "archivo donde escribir el schema válido (default: stdout)")
	aiGenerateCmd.Flags().Bool("json", false, "emitir el resultado completo (schema + métricas) como JSON")
	aiGenerateCmd.Flags().Bool("verbose", false, "mostrar el detalle por ronda (errores estructurales vs semánticos)")
	// EXPERIMENTAL opt-ins (default is the plain validator-guided loop, AI-F2-S4).
	// Both fall back to plain on the real Anthropic API (strict-subset limits); kept
	// for measurement (ai-eval) and future reuse (the IR backs the visual editor).
	aiGenerateCmd.Flags().Bool("structured", false, "EXPERIMENTAL: AI-F1-S1 structured-outputs envelope (no engancha contra la API real; cae a plano)")
	aiGenerateCmd.Flags().Bool("array-ir", false, "EXPERIMENTAL: AI-F2-S2 array-IR (no engancha contra la API real por el tope de 16 union params; cae a plano)")
	// Deprecated no-op: plain is now the default, so --no-structured is redundant.
	aiGenerateCmd.Flags().Bool("no-structured", false, "(obsoleto/no-op: el loop plano ya es el default)")
	_ = aiGenerateCmd.Flags().MarkDeprecated("no-structured", "plain is the default now; this flag is a no-op")
	rootCmd.AddCommand(aiGenerateCmd)
}
