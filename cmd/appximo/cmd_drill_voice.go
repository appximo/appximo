package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/platformadmin"
	"github.com/appximo/appximo/pkg/schema"
)

// The voice drills (CAPACIDADES-VISIBLES-S1): the question channel, the write
// channel and the spend card existed for four sessions and had no drill — an
// operator who wanted to see «cuántas órdenes hay» answered by the parser, or
// a voice write confirmed and executed, rebuilt the harness from a report.
//
//   appximo drill ask   --tenant t --role r "cuántas órdenes hay hoy"   one question, who answered, what it cost
//   appximo drill voice                                                the whole channel on an EPHEMERAL tenant
//   appximo drill spend --tenant t --role r                            the spend card (useful vs wasted)
//
// Safety: `ask` and `spend` change nothing (a question MAY spend one model
// call if the parser is not sure — it says so); `voice` creates its own
// tenant with this app's schema and deletes it.

var drillAskCmd = &cobra.Command{
	Use:   "ask [question]",
	Short: "Ask ONE question through POST /api/ask and see who answered (parser / cache / model), the plan and the cost",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := resolveDrillTarget(cmd, true)
		if err != nil {
			return err
		}
		tenant, _ := cmd.Flags().GetString("tenant")
		role, _ := cmd.Flags().GetString("role")
		token, _ := cmd.Flags().GetString("token")
		if tenant == "" {
			return errors.New("--tenant is required (an EXISTING tenant of this app)")
		}
		if role == "" {
			role = pickRole(t.schema)
		}
		t.intro("ask")
		if token == "" {
			if token, err = t.mint(tenant, role); err != nil {
				return err
			}
		}
		r, err := drillAsk(t.url, tenant, token, map[string]any{"q": args[0]})
		if err != nil {
			return err
		}
		printAskReply(t.lang, args[0], r)
		return nil
	},
}

var drillSpendCmd = &cobra.Command{
	Use:   "spend",
	Short: "The tenant's model spend card (GET /api/ask/spend): today, the month, useful vs wasted, the phrases that cost",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		t, err := resolveDrillTarget(cmd, true)
		if err != nil {
			return err
		}
		tenant, _ := cmd.Flags().GetString("tenant")
		role, _ := cmd.Flags().GetString("role")
		token, _ := cmd.Flags().GetString("token")
		if tenant == "" {
			return errors.New("--tenant is required (an EXISTING tenant of this app)")
		}
		if role == "" {
			role = pickRole(t.schema)
		}
		t.intro("spend")
		if token == "" {
			if token, err = t.mint(tenant, role); err != nil {
				return err
			}
		}
		req, _ := http.NewRequest(http.MethodGet, t.url+"/api/ask/spend", nil)
		req.Host = tenant + ".localhost"
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := drillHTTPClient(false).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET /api/ask/spend → %d: %s", resp.StatusCode, firstLine(string(body)))
		}
		var dig struct {
			Text  string `json:"text"`
			Share struct {
				Questions int     `json:"questions"`
				Parser    int     `json:"parser"`
				Cache     int     `json:"cache"`
				Model     int     `json:"model"`
				CostUSD   float64 `json:"cost_usd"`
				UsefulUSD float64 `json:"useful_usd"`
				WastedUSD float64 `json:"wasted_usd"`
				Wasted    int     `json:"wasted"`
				ParserPct float64 `json:"parser_pct"`
			} `json:"share_30d"`
		}
		_ = json.Unmarshal(body, &dig)
		fmt.Println(stripTags(dig.Text))
		fmt.Println()
		if t.lang == "es" {
			fmt.Printf("✓ 30 días: %d preguntas · parser %.0f %% · útil US$ %.3f · desperdiciado US$ %.3f (%d que no sirvieron)\n", dig.Share.Questions, dig.Share.ParserPct, dig.Share.UsefulUSD, dig.Share.WastedUSD, dig.Share.Wasted)
		} else {
			fmt.Printf("✓ 30 days: %d questions · parser %.0f %% · useful US$ %.3f · wasted US$ %.3f (%d bought nothing)\n", dig.Share.Questions, dig.Share.ParserPct, dig.Share.UsefulUSD, dig.Share.WastedUSD, dig.Share.Wasted)
		}
		return nil
	},
}

var drillVoiceCmd = &cobra.Command{
	Use:   "voice",
	Short: "The whole voice channel on an EPHEMERAL tenant: a parser question, a refused delete, a write confirmed and executed, the spend card",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		t, err := resolveDrillTarget(cmd, true)
		if err != nil {
			return err
		}
		if t.dsn == "" || t.adminKey == "" {
			return errors.New("drill voice needs DATABASE_URL and ADMIN_KEY (pass --app on an installed box, or export them)")
		}
		keep, _ := cmd.Flags().GetBool("keep")
		t.intro("voice")

		suffix := make([]byte, 3)
		_, _ = rand.Read(suffix)
		eph := "drill" + hex.EncodeToString(suffix)
		role := pickRole(t.schema)
		fmt.Printf("• ephemeral tenant %s (schema: %s, role: %s)\n", eph, t.schemaPath, role)
		if _, err := registerTenant(t.controlPort, t.adminKey, eph, eph+"@drill.invalid", t.schemaRaw, 90*time.Second, 0); err != nil {
			return fmt.Errorf("register: %v\n  (the control plane must be reachable at 127.0.0.1:%d — --control-port)", err, t.controlPort)
		}
		ctx := context.Background()
		pool, err := db.NewPool(ctx, t.dsn)
		if err != nil {
			return fmt.Errorf("connect DATABASE_URL: %v", err)
		}
		defer pool.Close()
		defer func() {
			if keep {
				fmt.Printf("\n• tenant %s KEPT (--keep). Delete it when done:  appximo tenant delete %s --yes\n", eph, eph)
				return
			}
			svc := platformadmin.NewService(platformadmin.NewStore(pool), nil, controlplane.NewService(pool, nil), pool,
				platformadmin.Config{JWTSecret: t.jwtSecret})
			if err := svc.DeleteTenant(ctx, eph, eph); err != nil {
				fmt.Fprintf(os.Stderr, "✗ delete tenant %s: %v — delete it by hand: appximo tenant delete %s --yes\n", eph, err, eph)
				return
			}
			fmt.Printf("✓ tenant %s deleted (schema + control-plane rows)\n", eph)
		}()
		token, err := t.mint(eph, role)
		if err != nil {
			return err
		}

		// 1. seed one row of the first insertable resource so a question has
		// something to count, and pick the words the schema itself declares.
		res, body, insertable := drillPickInsertable(t.schemaRaw, "")
		if res == "" {
			names := sortedResourceNames(t.schema)
			if len(names) == 0 {
				return errors.New("the schema declares no resources")
			}
			res = names[0]
		}
		if insertable {
			b, _ := json.Marshal(body)
			req, _ := http.NewRequest(http.MethodPost, t.url+"/api/"+res, bytes.NewReader(b))
			req.Host = eph + ".localhost"
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			if resp, err := drillHTTPClient(false).Do(req); err == nil {
				resp.Body.Close()
				fmt.Printf("• seeded one %s (POST → %d)\n", res, resp.StatusCode)
			}
		}
		word := res
		if rs, ok := t.schema.Resources[res]; ok && len(rs.Aliases) > 0 {
			word = rs.Aliases[0]
		}
		ok := 0
		check := func(label string, want []string, r map[string]any) {
			kind, _ := r["kind"].(string)
			src, _ := r["source"].(string)
			hit := false
			for _, w := range want {
				if kind == w {
					hit = true
				}
			}
			mark := "✗"
			if hit {
				mark = "✓"
				ok++
			}
			cost, _ := r["cost_usd"].(float64)
			fmt.Printf("%s %-34s kind=%-14s source=%-8s cost=US$ %.4f  %s\n", mark, label, kind, src, cost, oneLine(r["headline"]))
		}
		// 2. a question in the schema's own words (or its declared alias) → the parser
		r, err := drillAsk(t.url, eph, token, map[string]any{"q": "cuántos " + strings.ReplaceAll(word, "_", " ") + " hay"})
		if err != nil {
			return err
		}
		check("question («cuántos "+word+" hay»)", []string{"answer"}, r)
		parserSure := r["source"] == "parser"
		// 3. a delete verb → refused by the parser on every role, no model
		r, _ = drillAsk(t.url, eph, token, map[string]any{"q": "borrá todos los " + strings.ReplaceAll(word, "_", " ")})
		check("delete verb («borrá…»)", []string{"write_refused"}, r)
		// 4. a stray answer to a confirmation → settled by the parser, no model
		r, _ = drillAsk(t.url, eph, token, map[string]any{"q": "sí pero mejor el viernes"})
		check("stray «sí pero…»", []string{"unclear"}, r)
		// 5. a state transition (the ONE write shape the parser settles) when
		// the schema has a state machine on that resource; else a create
		// through the model when a key is set; else say why it is skipped.
		wrote := false
		if sm := stateFieldOf(t.schema, res); sm != "" {
			if to := firstTransitionTarget(t.schema, res, sm); to != "" {
				q := "marca como " + strings.ReplaceAll(to, "_", " ") + " " + singularWord(word)
				r, _ = drillAsk(t.url, eph, token, map[string]any{"q": q})
				check("transition («"+q+"»)", []string{"confirm", "ambiguous", "answer", "forbidden"}, r)
				if pid, _ := r["pending_id"].(string); pid != "" && r["stage"] == "confirm" {
					r2, _ := drillAsk(t.url, eph, token, map[string]any{"pending_id": pid, "answer": "sí"})
					check("confirmed («sí» by id)", []string{"written"}, r2)
					wrote = r2["kind"] == "written"
				}
			}
		}
		if !wrote && os.Getenv("ANTHROPIC_API_KEY") == "" {
			fmt.Println("• a create by voice needs the model (ANTHROPIC_API_KEY unset here): skipped — the transition above is the parser's own write")
		}
		// 6. the spend card
		req, _ := http.NewRequest(http.MethodGet, t.url+"/api/ask/spend", nil)
		req.Host = eph + ".localhost"
		req.Header.Set("Authorization", "Bearer "+token)
		if resp, err := drillHTTPClient(false).Do(req); err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			var dig struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(b, &dig)
			if resp.StatusCode == http.StatusOK {
				ok++
				fmt.Printf("✓ spend card (GET /api/ask/spend → 200)\n%s\n", indentLines(stripTags(dig.Text), "    "))
			} else {
				fmt.Printf("✗ spend card → %d (%s is not an admin-grade role?)\n", resp.StatusCode, role)
			}
		}
		fmt.Println()
		if t.lang == "es" {
			fmt.Printf("✓ %d verificaciones pasaron; el parser resolvió la pregunta: %v\n", ok, parserSure)
		} else {
			fmt.Printf("✓ %d checks passed; the parser settled the question: %v\n", ok, parserSure)
		}
		return nil
	},
}

// drillAsk posts to /api/ask as the tenant and decodes the reply.
func drillAsk(base, tenant, token string, body map[string]any) (map[string]any, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, base+"/api/ask", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Host = tenant + ".localhost"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	cl := drillHTTPClient(false)
	cl.Timeout = 40 * time.Second
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("POST /api/ask → %d: %s", resp.StatusCode, firstLine(string(raw)))
	}
	out["_status"] = resp.StatusCode
	return out, nil
}

func printAskReply(lang, q string, r map[string]any) {
	kind, _ := r["kind"].(string)
	src, _ := r["source"].(string)
	cost, _ := r["cost_usd"].(float64)
	ms, _ := r["total_ms"].(float64)
	fmt.Printf("• %s\n", q)
	fmt.Printf("  kind=%s source=%s cost=US$ %.4f total=%.0f ms", kind, src, cost, ms)
	if fb, _ := r["fallback_es"].(string); fb != "" && src != "parser" {
		fmt.Printf(" · el parser pasó: %s", fb)
	}
	fmt.Println()
	if d, _ := r["display"].(string); d != "" {
		fmt.Println(indentLines(d, "  "))
	} else if t, _ := r["text"].(string); t != "" {
		fmt.Println(indentLines(stripTags(t), "  "))
	}
	if p, ok := r["plan"]; ok && p != nil {
		b, _ := json.Marshal(p)
		fmt.Printf("  plan: %s\n", b)
	}
	if st, _ := r["_status"].(int); st == http.StatusServiceUnavailable {
		if lang == "es" {
			fmt.Println("  (503: esa pregunta necesita el modelo y ANTHROPIC_API_KEY no está en el env de esta app)")
		} else {
			fmt.Println("  (503: that question needs the model and ANTHROPIC_API_KEY is not in this app's env)")
		}
	}
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

func stripTags(s string) string {
	s = htmlTagRe.ReplaceAllString(s, "")
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(s)
}

func indentLines(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

func oneLine(v any) string {
	s, _ := v.(string)
	return firstLine(s)
}

// stateFieldOf returns the resource's state-machine field, "" when none.
func stateFieldOf(s *schema.APISchema, res string) string {
	rs, ok := s.Resources[res]
	if !ok {
		return ""
	}
	for name, fd := range rs.Fields {
		if fd.StateMachine != nil {
			return name
		}
	}
	return ""
}

// firstTransitionTarget is a state reachable from an initial state.
func firstTransitionTarget(s *schema.APISchema, res, field string) string {
	sm := s.Resources[res].Fields[field].StateMachine
	for _, from := range sm.Initial {
		if tos := sm.Transitions[from]; len(tos) > 0 {
			return tos[0]
		}
	}
	return ""
}

func singularWord(w string) string {
	return "el " + schema.SingularES(strings.ReplaceAll(schema.NormalizeText(w), "_", " "))
}

func init() {
	for _, c := range []*cobra.Command{drillAskCmd, drillSpendCmd} {
		c.Flags().String("tenant", "", "EXISTING tenant to ask as (required)")
		c.Flags().String("role", "", "JWT role (default: the broadest declared role)")
		c.Flags().String("token", "", "use this Bearer instead of minting one (no JWT_SECRET needed)")
		c.SilenceUsage = true
	}
	drillVoiceCmd.Flags().Bool("keep", false, "keep the ephemeral tenant (prints the delete command)")
	drillVoiceCmd.SilenceUsage = true
	drillCmd.AddCommand(drillAskCmd, drillVoiceCmd, drillSpendCmd)
	drillOrder = append(drillOrder, "ask", "voice", "spend")
	drillSafety["ask"] = "question"
	drillSafety["voice"] = "ephemeral"
	drillSafety["spend"] = "readonly"
	for lang, s := range map[string]string{
		"en": "changes nothing — but a question the parser is not sure of spends ONE model call (≈ US$ 0.003), and says so",
		"es": "no cambia nada — pero una pregunta que el parser no resuelve gasta UNA llamada al modelo (≈ US$ 0,003), y lo dice",
	} {
		drillStrings[lang]["safety.question"] = s
	}
	drillTexts["ask"] = map[string]drillText{
		"en": {
			title:  "one question, and who answered it",
			what:   "posts ONE question to POST /api/ask as the given tenant + role — exactly what the Telegram bot and a Siri shortcut do — and prints the reply with its accounting.",
			expect: "source=parser (US$ 0, milliseconds) for a question in the schema's own words or its declared aliases; source=cache for a repeat; source=model (≈ US$ 0.003, ~1 s) for the rest, with «el parser pasó: …» naming the word it did not know; a delete verb is write_refused; a write order answers a confirmation (kind=confirm) and nothing is written.",
			where: "the reply itself (display = the text with the ⚙︎ trace when APPXIMO_ASK_TRACE=on).\n" +
				"                       GET /admin/ask?tenant=<t>  (platform key): share, top_cost, model_fallbacks with the parser's reason.\n" +
				"                       Telegram: `gasto`  ·  /metrics: appximo_ask_questions{source}",
		},
		"es": {
			title:  "una pregunta, y quién la respondió",
			what:   "manda UNA pregunta a POST /api/ask como el tenant + rol dados — exactamente lo que hacen el bot de Telegram y un atajo de Siri — e imprime la respuesta con su contabilidad.",
			expect: "source=parser (US$ 0, milisegundos) para una pregunta en las palabras del schema o sus alias declarados; source=cache si se repite; source=model (≈ US$ 0,003, ~1 s) para el resto, con «el parser pasó: …» nombrando la palabra que no conoció; un verbo de borrar es write_refused; una orden de escritura responde una confirmación (kind=confirm) y no escribí nada.",
			where: "la propia respuesta (display = el texto con la traza ⚙︎ si APPXIMO_ASK_TRACE=on).\n" +
				"                       GET /admin/ask?tenant=<t>  (clave de plataforma): share, top_cost, model_fallbacks con la razón del parser.\n" +
				"                       Telegram: `gasto`  ·  /metrics: appximo_ask_questions{source}",
		},
	}
	drillTexts["voice"] = map[string]drillText{
		"en": {
			title:  "the voice channel, end to end, on a tenant it creates and deletes",
			what:   "registers an EPHEMERAL tenant with this app's schema, seeds one row, and drives POST /api/ask: a count in the schema's word (or its alias), a delete verb, a stray «sí pero…», a state transition confirmed by id (the ONE write the parser settles alone), and the spend card.",
			expect: "the count is source=parser at US$ 0; the delete verb is write_refused without a model call; the stray yes is unclear/parser at US$ 0; the transition answers kind=confirm and, confirmed by id, kind=written (the row moved through the engine's own write cores); the spend card answers 200 for an admin-grade role. A create by voice needs the model and is skipped without ANTHROPIC_API_KEY.",
			where: "the printed checks (✓/✗ per step).\n" +
				"                       /admin → the ephemeral tenant → Data: the row that moved (until the drill deletes the tenant; --keep to look).\n" +
				"                       journal: journalctl -u <unit> -o cat | grep 'ask: question answered'  (source, kind, cost per question)",
		},
		"es": {
			title:  "el canal de voz, de punta a punta, en un tenant que crea y borra",
			what:   "registra un tenant EFÍMERO con el schema de esta app, siembra una fila y maneja POST /api/ask: un conteo con la palabra del schema (o su alias), un verbo de borrar, un «sí pero…» suelto, una transición de estado confirmada por id (LA escritura que el parser resuelve solo), y la tarjeta de gasto.",
			expect: "el conteo es source=parser a US$ 0; el verbo de borrar es write_refused sin llamada al modelo; el «sí pero…» es unclear/parser a US$ 0; la transición responde kind=confirm y, confirmada por id, kind=written (la fila pasó por los cores de escritura del motor); la tarjeta de gasto responde 200 para un rol admin. Un create por voz necesita el modelo y se salta sin ANTHROPIC_API_KEY.",
			where: "las verificaciones impresas (✓/✗ por paso).\n" +
				"                       /admin → el tenant efímero → Datos: la fila que se movió (hasta que el drill borra el tenant; --keep para mirar).\n" +
				"                       journal: journalctl -u <unidad> -o cat | grep 'ask: question answered'  (source, kind, costo por pregunta)",
		},
	}
	drillTexts["spend"] = map[string]drillText{
		"en": {
			title:  "what the questions cost this tenant",
			what:   "reads GET /api/ask/spend as an admin-grade role and prints the card: today, the month, the cap and what is left, who answered how many (parser / cache / model), useful vs wasted spend, the phrases that cost the most and why they went to the model.",
			expect: "200 with the card for a wildcard-resource role (403 for a listed or row-scoped role — the spend of a platform is the administrator's business); numbers identical to Telegram's `gasto` and to /admin/ask.",
			where:  "the printed card.  Telegram: `gasto` (picture + text).  GET /admin/ask?tenant=<t> (platform key).  /metrics: appximo_ask_spend_usd{tenant,window}.",
		},
		"es": {
			title:  "cuánto cuestan las preguntas de este tenant",
			what:   "lee GET /api/ask/spend como un rol admin e imprime la tarjeta: hoy, el mes, el techo y lo que falta, quién respondió cuántas (parser / caché / modelo), gasto útil vs desperdiciado, las frases que más cuestan y por qué fueron al modelo.",
			expect: "200 con la tarjeta para un rol con recursos «*» (403 para un rol acotado — el gasto de una plataforma es del administrador); números idénticos al `gasto` de Telegram y a /admin/ask.",
			where:  "la tarjeta impresa.  Telegram: `gasto` (imagen + texto).  GET /admin/ask?tenant=<t> (clave de plataforma).  /metrics: appximo_ask_spend_usd{tenant,window}.",
		},
	}
}
