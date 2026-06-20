package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/miguelangel/appitools/pkg/aigen"
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
		noStructured, _ := cmd.Flags().GetBool("no-structured")

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

		// Structured outputs (constrained decoding) by default when the model
		// supports it; --no-structured forces plain generation for comparison.
		useStructured := !noStructured && aigen.SupportsStructuredOutput(client.Model())

		res, err := aigen.Generate(context.Background(), client, args[0], aigen.Options{
			MaxIterations: maxIter,
			Model:         client.Model(),
			NoStructured:  !useStructured,
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
	decode := "plano (sin structured outputs)"
	if res.Structured {
		decode = "structured outputs (estructura garantizada en el decoding)"
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
	fmt.Fprintln(w, "─────────────────────────────────────────────────")
}

func init() {
	aiGenerateCmd.Flags().String("model", aigen.DefaultModel, "modelo a usar (claude-haiku-4-5 | claude-sonnet-4-6 | claude-opus-4-8)")
	aiGenerateCmd.Flags().Int("max-iterations", aigen.DefaultMaxIterations, "máximo de rondas generate→corregir")
	aiGenerateCmd.Flags().String("out", "", "archivo donde escribir el schema válido (default: stdout)")
	aiGenerateCmd.Flags().Bool("json", false, "emitir el resultado completo (schema + métricas) como JSON")
	aiGenerateCmd.Flags().Bool("verbose", false, "mostrar el detalle por ronda (errores estructurales vs semánticos)")
	aiGenerateCmd.Flags().Bool("no-structured", false, "desactivar structured outputs (generación plana, para comparar)")
	rootCmd.AddCommand(aiGenerateCmd)
}
