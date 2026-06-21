package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/miguelangel/appitools/pkg/aigen"
	"github.com/miguelangel/appitools/pkg/aigen/eval"
	"github.com/spf13/cobra"
)

// ai-eval is the scientific measurement instrument (AI-F2-S1): it runs the curated
// NL→schema gold test set through a paired ablation (plain vs structured decoding,
// and any future technique as a new arm) and emits the statistics the research
// requires — p_sem with Wilson CIs, EMPIRICAL E[iterations], and McNemar / Cochran-Q
// + Holm — being explicit about its own statistical power (a seed corpus yields
// wide CIs and inconclusive tests; that is correct, not a defect).
//
// By default it runs in SIMULATED mode (deterministic, no API key) to demonstrate
// the instrument end-to-end. With --live (requires ANTHROPIC_API_KEY) it measures a
// real model. The instrument is the deliverable; the techniques it scores come later.
var aiEvalCmd = &cobra.Command{
	Use:   "ai-eval",
	Short: "Mide la capa de IA: ablation pareado sobre el test set gold con estadística rigurosa (McNemar/Wilson/Cochran-Q+Holm)",
	Long: `Runs the curated NL→schema gold test set through a paired ablation harness and
emits a rigorous statistical report: p_sem (first-try semantic success) with Wilson
95% intervals, the EMPIRICAL E[iterations] distribution (never the geometric
1/p_sem — the validator-guided loop is not i.i.d.), and paired-significance tests
(McNemar, exact when discordants < 25 else Edwards χ²; Cochran's Q + Holm for >2
arms). It is honest about power: an underpowered comparison is flagged
INCONCLUSIVE, never reported as significant.

Default mode is SIMULATED (deterministic, no API key) — it demonstrates the whole
instrument and is fully reproducible. With --live and ANTHROPIC_API_KEY it measures
a real model.

Examples:
  appitools ai-eval                       # simulated demonstration (deterministic)
  appitools ai-eval --out outcomes.json   # persist the paired outcomes
  appitools ai-eval --json                # machine-readable analysis
  appitools ai-eval --live --model claude-haiku-4-5   # measure a real model`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		live, _ := cmd.Flags().GetBool("live")
		model, _ := cmd.Flags().GetString("model")
		out, _ := cmd.Flags().GetString("out")
		jsonOut, _ := cmd.Flags().GetBool("json")
		maxIter, _ := cmd.Flags().GetInt("max-iterations")

		cases, err := eval.LoadCorpus()
		if err != nil {
			return fmt.Errorf("load corpus: %w", err)
		}

		conds := eval.BaselineConditions()
		if maxIter > 0 {
			for i := range conds {
				conds[i].Options.MaxIterations = maxIter
			}
		}

		mode := "SIMULATED"
		var factory eval.ClientFactory
		if live {
			client, cerr := aigen.NewAnthropicClient(model)
			if cerr != nil {
				if errors.Is(cerr, aigen.ErrNoAPIKey) {
					fmt.Fprintln(os.Stderr, "Error: --live requiere ANTHROPIC_API_KEY (export it; never hardcode).")
					os.Exit(2)
				}
				return cerr
			}
			model = client.Model()
			mode = "LIVE"
			// The same stateless client serves every (case, condition); the
			// condition's Options control structured-vs-plain. (For full live
			// reproducibility, pin temperature 0 on models that accept it.)
			factory = func(c eval.Case, cond eval.Condition) aigen.ModelClient { return client }
		} else {
			if model == "" {
				model = aigen.DefaultModel
			}
			factory = func(c eval.Case, cond eval.Condition) aigen.ModelClient {
				return eval.NewSimulatedClient(c, cond)
			}
		}

		outcomes, err := eval.RunAblation(context.Background(), cases, conds, factory, model)
		if err != nil {
			return fmt.Errorf("run ablation: %w", err)
		}

		analysis := eval.Analyze(outcomes, conds, mode, model, cases)

		if out != "" {
			b, _ := json.MarshalIndent(outcomes, "", "  ")
			if werr := os.WriteFile(out, b, 0o644); werr != nil {
				return fmt.Errorf("write %s: %w", out, werr)
			}
			fmt.Fprintf(os.Stderr, "paired outcomes written to %s (%d rows)\n", out, len(outcomes))
		}

		if jsonOut {
			b, _ := json.MarshalIndent(analysis, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		fmt.Print(eval.Format(analysis))
		return nil
	},
}

func init() {
	aiEvalCmd.Flags().Bool("live", false, "medir un modelo real (requiere ANTHROPIC_API_KEY); default = simulado determinístico")
	aiEvalCmd.Flags().String("model", aigen.DefaultModel, "modelo (para la etiqueta de costo y, en --live, el modelo real)")
	aiEvalCmd.Flags().String("out", "", "persistir los outcomes pareados como JSON (reproducibilidad)")
	aiEvalCmd.Flags().Bool("json", false, "emitir el análisis estadístico completo como JSON")
	aiEvalCmd.Flags().Int("max-iterations", 0, "override del budget de iteraciones (0 = default del loop)")
	rootCmd.AddCommand(aiEvalCmd)
}
