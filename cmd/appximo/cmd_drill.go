package main

// appximo drill — repeat any operational scenario with ONE command
// (MANUAL-OPERACION-S1).
//
// The scenarios existed before this file, scattered: the CAOS-S1 experiments
// were ssh heredocs typed in a session, the eight Module C provocations lived
// in an evidence directory that is not in the repository, the `probe500`
// tenant and the poisoned binary were one-off scripts. An operator who wanted
// to see a 500 explained, or the admission control shedding, had to rebuild
// the harness from a report. Every drill here prints the SAME three things
// before it runs — what it will provoke, what should happen, and WHERE in the
// panel to look at it — so the command teaches the panel while it exercises
// the engine.
//
// Safety, by construction and not by promise:
//   - a drill that loads, breaks or restores REFUSES a target that looks like
//     production (a public domain in Caddy, an address in the lab's protected
//     belt) unless --production is passed explicitly;
//   - `drill error` works on an EPHEMERAL tenant it creates and deletes itself
//     (DROP SCHEMA CASCADE + every control-plane row, the same path as
//     `tenant delete`), never on a real tenant's data;
//   - the box-level experiments (chaos, restore rehearsal) run through the
//     companion `scripts/drill.sh`, which restores every fault it injects
//     (iptables rule, tc qdisc, clock, disk filler) on exit — also on Ctrl-C.
//
// Language: the explanatory blocks follow --lang (default: the LANG of the
// shell, `es*` → Spanish); the progress lines stay English like the rest of
// the CLI (A7).

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/platformadmin"
	"github.com/appximo/appximo/pkg/schema"
)

// ── the command tree ────────────────────────────────────────────────────────

var drillCmd = &cobra.Command{
	Use:   "drill",
	Short: "Repeat an operational scenario (a 500, load, saturation, chaos, restore, audit) and see where it shows",
	Long: `Repeat an operational scenario with one command, and be told where to look.

Every drill prints, BEFORE running: what it will provoke, what should happen,
and where in the panel (/admin) it becomes visible. Then it runs, and prints
the evidence it found.

  appximo drill list                    the catalogue, with the safety class of each
  appximo drill error                   a real 500 on an ephemeral tenant → Observability → Issues
  appximo drill load                    steady load → the self-monitor's verdict, live
  appximo drill saturate                a ladder past the ceiling → who sheds (limiter / admission / breaker)
  appximo drill probe --url …           a 10 rps probe with an outage summary (from another machine)
  appximo drill chaos <1-10>            one of the ten CAOS-S1 experiments, ON the box, restored on exit
  appximo drill restore                 a timed restore REHEARSAL into a scratch database (--real = the real thing)
  appximo drill audit                   fleet-audit.sh: what is MISSING on this box

Target: --app=NAME reads /etc/NAME/NAME.env + the unit (an installed box);
without it, --url + the environment (DATABASE_URL, JWT_SECRET, ADMIN_KEY —
a .env in the working directory is loaded automatically).

Safety: load/saturate/chaos/restore --real refuse a target that looks like
production (a public domain in Caddy, an address in the lab's protected belt)
unless --production is given. 'error' uses a tenant it creates and deletes
itself. 'audit', 'list' and 'probe' change nothing.`,
}

var drillListCmd = &cobra.Command{
	Use:   "list",
	Short: "The catalogue: every drill, its safety class, and where its evidence shows",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		lang := drillLang(cmd)
		fmt.Println(drillT(lang, "catalogue.title"))
		for _, name := range drillOrder {
			tx := drillTexts[name][lang]
			fmt.Printf("\n  %-10s %s\n", name, tx.title)
			fmt.Printf("  %-10s %s %s\n", "", drillT(lang, "label.safety"), drillT(lang, "safety."+drillSafety[name]))
			fmt.Printf("  %-10s %s %s\n", "", drillT(lang, "label.where"), firstLine(tx.where))
		}
		fmt.Println()
		fmt.Println(drillT(lang, "catalogue.chaos"))
		for i := 1; i <= 10; i++ {
			fmt.Printf("  %2d  %s\n", i, drillChaos[i][lang].title)
		}
		fmt.Println()
		fmt.Println(drillT(lang, "catalogue.footer"))
	},
}

func init() {
	pf := drillCmd.PersistentFlags()
	pf.String("app", "", "installed app name: reads /etc/NAME/NAME.env, the unit's port/schema, /opt/NAME/scripts")
	pf.String("url", "", "engine base URL (default http://127.0.0.1:<port of the app>, or :8080 without --app)")
	pf.Int("control-port", 0, "control-plane port (default APPXIMO_CONTROL_PORT, else 9090)")
	pf.String("schema", "", "schema file (default: the app's boot schema, or GET /editor/current-schema)")
	pf.String("domain", "", "the app's public domain — only used to decide whether the target looks like production")
	pf.String("lang", "", "language of the explanations: en | es (default: from LANG)")
	pf.Bool("production", false, "I know this target is production — run anyway (load/saturate/chaos/restore --real)")
	pf.String("scripts", "", "directory holding drill.sh / fleet-audit.sh (default /opt/<app>/scripts, then the repo)")
	pf.Bool("yes", false, "no interactive pauses (delete the ephemeral tenant immediately, etc.)")

	drillErrorCmd.Flags().String("resource", "", "resource to break (default: the first one whose required fields can be filled)")
	drillErrorCmd.Flags().Bool("keep", false, "keep the ephemeral tenant (prints the delete command)")

	for _, c := range []*cobra.Command{drillLoadCmd, drillSaturateCmd} {
		c.Flags().String("tenant", "", "EXISTING tenant to load (required)")
		c.Flags().String("resource", "", "resource to read (default: the first readable one)")
		c.Flags().String("role", "", "JWT role (default: the broadest declared role)")
		c.Flags().String("token", "", "use this Bearer instead of minting one (no JWT_SECRET needed)")
		c.Flags().String("path", "", "request path override; {n} is replaced by a random number (cache-busting)")
	}
	drillLoadCmd.Flags().Float64("rate", 100, "offered requests per second")
	drillLoadCmd.Flags().Duration("duration", 30*time.Second, "run length")
	drillSaturateCmd.Flags().String("rates", "200,400,800,1600,3200", "the ladder, requests per second per level")
	drillSaturateCmd.Flags().Duration("step", 10*time.Second, "seconds per level")
	drillSaturateCmd.Flags().Bool("all-levels", false, "do not stop at the first level that sheds")

	drillProbeCmd.Flags().Float64("rate", 10, "requests per second")
	drillProbeCmd.Flags().Duration("duration", 60*time.Second, "how long to probe")
	drillProbeCmd.Flags().String("path", "/healthz", "path to probe (an /api/ path needs --token + --host)")
	drillProbeCmd.Flags().String("host", "", "Host header (the tenant subdomain) for an /api/ path")
	drillProbeCmd.Flags().String("token", "", "Bearer token for an /api/ path")
	drillProbeCmd.Flags().Bool("insecure", false, "skip TLS verification (a lab box with an internal certificate)")

	drillChaosCmd.Flags().String("tenant", "", "tenant the probe reads (default: the first registered one)")
	drillChaosCmd.Flags().String("resource", "", "resource the probe reads / D9 writes (default: the first one)")
	drillChaosCmd.Flags().Bool("full", false, "D4: fill the disk to 100 % (default stops after crossing the alert floor)")
	drillChaosCmd.Flags().Bool("yes-reboot", false, "D3: actually reboot the box (default prints the recipe only)")

	drillRestoreCmd.Flags().String("set", "", "backup set prefix (default: the newest in the app's backup dir)")
	drillRestoreCmd.Flags().Bool("real", false, "run the REAL restore.sh (stops the app, replaces the database) instead of the scratch rehearsal")

	for _, c := range []*cobra.Command{drillErrorCmd, drillLoadCmd, drillSaturateCmd, drillProbeCmd, drillChaosCmd, drillRestoreCmd, drillAuditCmd} {
		c.SilenceUsage = true
	}
	drillCmd.AddCommand(drillListCmd, drillErrorCmd, drillLoadCmd, drillSaturateCmd, drillProbeCmd,
		drillChaosCmd, drillRestoreCmd, drillAuditCmd)
	rootCmd.AddCommand(drillCmd)
}

// ── texts: what / expected / where, in two languages ────────────────────────

type drillText struct{ title, what, expect, where string }

var drillOrder = []string{"error", "load", "saturate", "probe", "chaos", "restore", "audit"}

// safety class per drill — printed in the catalogue and enforced in code.
var drillSafety = map[string]string{
	"error":    "ephemeral", // creates + deletes its own tenant
	"load":     "load",      // real traffic on a real tenant → --production on prod
	"saturate": "load",
	"probe":    "readonly",
	"chaos":    "box", // injects faults on the box → --production on prod
	"restore":  "scratch",
	"audit":    "readonly",
}

var drillStrings = map[string]map[string]string{
	"en": {
		"catalogue.title":  "appximo drill — the scenarios you can repeat, and where each one shows:",
		"catalogue.chaos":  "  chaos <n> — the ten CAOS-S1 experiments (each restores what it breaks):",
		"catalogue.footer": "Run `appximo drill <name> --help` for the flags. Explanations follow --lang (en|es).",
		"label.safety":     "safety:",
		"label.where":      "where:  ",
		"safety.ephemeral": "creates its own tenant and deletes it — allowed anywhere",
		"safety.load":      "real load on a real tenant — refuses production unless --production",
		"safety.readonly":  "changes nothing",
		"safety.box":       "injects a fault on the box and restores it — refuses production unless --production",
		"safety.scratch":   "restores into a scratch database (the app keeps running); --real needs --production on production",
		"hdr.what":         "What it will provoke:",
		"hdr.expect":       "What should happen:  ",
		"hdr.where":        "Where to look:       ",
		"refuse":           "REFUSED: this target looks like PRODUCTION (%s).\n  Run it against the laboratory (tools/lab) or an ephemeral tenant, or pass --production if you really mean it.",
		"prod.note":        "note: this target looks like production (%s) — proceeding because --production was given.",
	},
	"es": {
		"catalogue.title":  "appximo drill — los escenarios que puede repetir, y dónde se ve cada uno:",
		"catalogue.chaos":  "  chaos <n> — los diez experimentos de CAOS-S1 (cada uno restaura lo que rompe):",
		"catalogue.footer": "Ejecute `appximo drill <nombre> --help` para las banderas. Las explicaciones siguen --lang (en|es).",
		"label.safety":     "seguridad:",
		"label.where":      "dónde:    ",
		"safety.ephemeral": "crea su propio tenant y lo borra — permitido en cualquier caja",
		"safety.load":      "carga real sobre un tenant real — se niega en producción salvo --production",
		"safety.readonly":  "no cambia nada",
		"safety.box":       "inyecta una falla en la caja y la restaura — se niega en producción salvo --production",
		"safety.scratch":   "restaura en una base de prueba (la app sigue arriba); --real exige --production en producción",
		"hdr.what":         "Qué va a provocar:  ",
		"hdr.expect":       "Qué debería pasar:  ",
		"hdr.where":        "Dónde mirarlo:      ",
		"refuse":           "RECHAZADO: este destino parece PRODUCCIÓN (%s).\n  Córralo contra el laboratorio (tools/lab) o un tenant efímero, o pase --production si de verdad quiere.",
		"prod.note":        "nota: este destino parece producción (%s) — sigue porque se pasó --production.",
	},
}

func drillT(lang, key string) string {
	if s, ok := drillStrings[lang][key]; ok {
		return s
	}
	return drillStrings["en"][key]
}

var drillTexts = map[string]map[string]drillText{
	"error": {
		"en": {
			title:  "a real 500 that explains itself",
			what:   "creates an EPHEMERAL tenant with this app's schema, breaks one table underneath the engine (a BEFORE INSERT trigger that RAISEs, or the table dropped) and sends two requests that hit it.",
			expect: "both answer HTTP 500 with an X-Trace-ID; the trace carries the message, the failed statement, the user and role; the two occurrences collapse into ONE problem group; the first occurrence raises an alert (a journal line, or Slack if SLACK_WEBHOOK_URL is set).",
			where: "/admin → pick the tenant (top right) → Observability → Issues → \"Problems (24 h)\": one row, 2 events, 1 user.\n" +
				"                       → Traces → the 500 → Waterfall: the failing stage marked ✗, \"Failed statement\", \"Stack\", \"Copy as curl\".\n" +
				"                       journal: journalctl -u <unit> -o cat | grep '\"level\":\"error\"'   (one JSON line per 500: trace_id, sql, site)\n" +
				"                       alert:   journalctl -u <unit> -o cat | grep -i 'alert'",
		},
		"es": {
			title:  "un 500 real que se explica solo",
			what:   "crea un tenant EFÍMERO con el schema de esta app, rompe una tabla por debajo del motor (un trigger BEFORE INSERT que hace RAISE, o la tabla borrada) y manda dos requests que la tocan.",
			expect: "los dos responden HTTP 500 con un X-Trace-ID; la traza lleva el mensaje, la sentencia que falló, el usuario y el rol; las dos ocurrencias se agrupan en UN problema; la primera dispara una alerta (una línea de journal, o Slack si SLACK_WEBHOOK_URL está puesto).",
			where: "/admin → elija el tenant (arriba a la derecha) → Observabilidad → Problemas → «Problemas (24 h)»: una fila, 2 eventos, 1 usuario.\n" +
				"                       → Trazas → el 500 → Cascada: la etapa que falló marcada ✗, «Sentencia que falló», «Pila», «Copiar como curl».\n" +
				"                       journal: journalctl -u <unidad> -o cat | grep '\"level\":\"error\"'   (una línea JSON por 500: trace_id, sql, site)\n" +
				"                       alerta:  journalctl -u <unidad> -o cat | grep -i 'alert'",
		},
	},
	"load": {
		"en": {
			title:  "steady load, and the engine's own verdict about it",
			what:   "sends a fixed rate of cache-busting reads (open model: the schedule never waits for the server) against one tenant for the given duration, and reads the self-monitor every second.",
			expect: "a healthy box answers every request 200 at a stable p99 and the verdict stays `healthy`; if something gives, the verdict names it (cpu_saturated, pool_exhausted, db_bound, …) and who owns it (app / database / host).",
			where: "/admin → Resources → Live: the verdict banner and the request/pool/CPU cards move while this runs (the collector switches to 1 s ticks).\n" +
				"                       /admin → Resources → Load test: the attribution strip per tick + the five charts of THIS run.\n" +
				"                       every read answers a Server-Timing header (query;dur=…) — the database stage of each request.",
		},
		"es": {
			title:  "carga sostenida, y el veredicto del propio motor",
			what:   "manda una tasa fija de lecturas sin caché (modelo abierto: el calendario nunca espera al servidor) contra un tenant durante el tiempo indicado, y lee el auto-monitor cada segundo.",
			expect: "una caja sana responde todo 200 con un p99 estable y el veredicto queda en `healthy`; si algo cede, el veredicto lo nombra (cpu_saturated, pool_exhausted, db_bound, …) y dice de quién es (app / base / host).",
			where: "/admin → Recursos → En vivo: el veredicto y las tarjetas de requests/pool/CPU se mueven mientras esto corre (el colector pasa a ticks de 1 s).\n" +
				"                       /admin → Recursos → Prueba de carga: la franja de atribución por tick + las cinco gráficas de ESTA corrida.\n" +
				"                       cada lectura responde un encabezado Server-Timing (query;dur=…) — la etapa de base de datos de cada request.",
		},
	},
	"saturate": {
		"en": {
			title:  "past the ceiling: who sheds, and how",
			what:   "climbs a ladder of offered rates (200, 400, 800, … rps) against one tenant, 10 s per level, and stops at the first level where the engine starts refusing.",
			expect: "below the ceiling every request is 200; past it the engine SHEDS instead of tipping — 429 \"rate limit exceeded\" from the per-tenant limiter (RATE_LIMIT_RPS, default 350 × vCPU), 429 \"server at capacity\" from the admission control (APPXIMO_MAX_INFLIGHT), 503 from the breaker/memory guard — and the accepted requests keep a sane latency.",
			where: "/admin → Resources → Live: requests/s, the 429 and 503 counters, in-flight, the verdict.\n" +
				"                       /metrics (X-Admin-Key): appximo_admission_rejected_total, appximo_selfmon_request_status_429.\n" +
				"                       boot log: `rate limiter: N RPS (derived …)` and `admission: max N` — the two ceilings this drill hits.",
		},
		"es": {
			title:  "pasado el techo: quién recorta, y cómo",
			what:   "sube una escalera de tasas ofrecidas (200, 400, 800, … rps) contra un tenant, 10 s por nivel, y se detiene en el primer nivel donde el motor empieza a rechazar.",
			expect: "bajo el techo todo es 200; pasado el techo el motor RECORTA en vez de volcarse — 429 «rate limit exceeded» del limitador por tenant (RATE_LIMIT_RPS, default 350 × vCPU), 429 «server at capacity» del control de admisión (APPXIMO_MAX_INFLIGHT), 503 del breaker o del guardia de memoria — y lo que sí entra conserva una latencia sana.",
			where: "/admin → Recursos → En vivo: requests/s, los contadores 429 y 503, en vuelo, el veredicto.\n" +
				"                       /metrics (X-Admin-Key): appximo_admission_rejected_total, appximo_selfmon_request_status_429.\n" +
				"                       log de arranque: `rate limiter: N RPS (derived …)` y `admission: max N` — los dos techos que este drill toca.",
		},
	},
	"probe": {
		"en": {
			title:  "an outside probe with an outage summary",
			what:   "sends a low fixed rate (10 rps) of requests to the URL for the given time and records every outcome with a timestamp — the instrument the chaos experiments are measured with, from ANOTHER machine.",
			expect: "a summary: total, per-status counts, the first failure, the outage length, the first success after the last failure, and how fast the failures came (p50).",
			where:  "the summary itself; pair it with `drill chaos 3` (reboot) run on the box, or with a deploy (scripts/deploy-app.sh).",
		},
		"es": {
			title:  "una sonda desde afuera con resumen de corte",
			what:   "manda una tasa baja y fija (10 rps) de requests a la URL durante el tiempo indicado y anota cada resultado con su hora — el instrumento con el que se miden los experimentos de caos, desde OTRA máquina.",
			expect: "un resumen: total, conteo por estado, la primera falla, cuánto duró el corte, el primer éxito después de la última falla, y qué tan rápido llegaron las fallas (p50).",
			where:  "el resumen mismo; combínelo con `drill chaos 3` (reinicio) corrido en la caja, o con un despliegue (scripts/deploy-app.sh).",
		},
	},
	"chaos": {
		"en": {
			title:  "one of the ten CAOS-S1 experiments, on this box",
			what:   "injects one fault ON this box (see the list) under a 10 rps probe, measures the outage and the recovery, and restores the fault on exit — including on Ctrl-C.",
			expect: "the hypothesis printed before each experiment; the verdict after it.",
			where:  "per experiment (printed with it); the common surfaces are /admin → Resources (verdict, pool, 429/503) and the unit's journal.",
		},
		"es": {
			title:  "uno de los diez experimentos de CAOS-S1, en esta caja",
			what:   "inyecta una falla EN esta caja (ver la lista) bajo una sonda de 10 rps, mide el corte y la recuperación, y restaura la falla al salir — también con Ctrl-C.",
			expect: "la hipótesis impresa antes de cada experimento; el veredicto después.",
			where:  "por experimento (se imprime con cada uno); las superficies comunes son /admin → Recursos (veredicto, pool, 429/503) y el journal de la unidad.",
		},
	},
	"restore": {
		"en": {
			title:  "a timed restore rehearsal",
			what:   "takes the newest backup set, restores its dump into a SCRATCH database next to the live one (the app keeps running), verifies every table's row count against the set's manifest, prints the time of each stage, and drops the scratch database. --real runs restore.sh instead: stop → conf → drop/create → load → files → start → verify.",
			expect: "\"REHEARSAL VERIFIED\" with every table matching the manifest, and the number you tell a customer: how long a real restore takes on THIS box (measured: 13.6 s for 251 k rows / 124 MB on 2 vCPU).",
			where: "the stage times printed here; the set files in /var/backups/<app>/ (dump, files, conf, manifest, last-backup.status).\n" +
				"                       /admin → Resources → Live → \"Disk & backup\": last backup age/status and the disk floor; /metrics: appximo_selfmon_backup_ok.",
		},
		"es": {
			title:  "un simulacro de restauración cronometrado",
			what:   "toma el set de backup más reciente, restaura su dump en una base DE PRUEBA al lado de la viva (la app sigue arriba), verifica el conteo de filas de cada tabla contra el manifiesto del set, imprime el tiempo de cada etapa y borra la base de prueba. --real corre restore.sh de verdad: parar → conf → drop/create → cargar → archivos → arrancar → verificar.",
			expect: "«SIMULACRO VERIFICADO» con cada tabla igual al manifiesto, y el número que le dice a un cliente: cuánto tarda una restauración real en ESTA caja (medido: 13,6 s para 251 k filas / 124 MB en 2 vCPU).",
			where: "los tiempos por etapa impresos acá; los archivos del set en /var/backups/<app>/ (dump, files, conf, manifest, last-backup.status).\n" +
				"                       /admin → Recursos → En vivo → «Disco y backup»: edad/estado del último backup y el piso de disco; /metrics: appximo_selfmon_backup_ok.",
		},
	},
	"audit": {
		"en": {
			title:  "what is MISSING on this box",
			what:   "runs fleet-audit.sh: for every installed app — unit policy, binary version, companions, backup timer, last set's age and completeness, off-box copy, alert destination; and the box facts (swap, disk, checksums, PostgreSQL restart policy). Read-only.",
			expect: "✓ lines for what is protected and ✗ lines that say WHAT TO DO; exit 0 = protected, 1 = at least one gap.",
			where:  "the output itself; every ✗ names the file or command that fixes it (docs/PRODUCTION.md §4.5b).",
		},
		"es": {
			title:  "qué FALTA en esta caja",
			what:   "corre fleet-audit.sh: por cada app instalada — política de la unidad, versión del binario, scripts compañeros, timer de backup, edad y completitud del último set, copia fuera de la caja, destino de alertas; y los hechos de la caja (swap, disco, checksums, política de reinicio de PostgreSQL). Solo lectura.",
			expect: "líneas ✓ para lo que está protegido y líneas ✗ que dicen QUÉ HACER; sale 0 = protegida, 1 = al menos una brecha.",
			where:  "la salida misma; cada ✗ nombra el archivo o el comando que lo arregla (docs/PRODUCTION.md §4.5b).",
		},
	},
}

// drillChaos — the ten experiments, mirrored in scripts/drill.sh.
var drillChaos = map[int]map[string]drillText{
	1: {
		"en": {title: "kill -9 the engine under load", what: "SIGKILLs the engine's main process while a probe runs.", expect: "systemd brings it back in ~2–3 s (RestartSec=2); the probe shows a gap of that size and no half-written rows (every write is one transaction).", where: "the probe summary; `systemctl show -p NRestarts <unit>`; journal."},
		"es": {title: "kill -9 al motor bajo carga", what: "manda SIGKILL al proceso principal del motor mientras corre una sonda.", expect: "systemd lo levanta en ~2–3 s (RestartSec=2); la sonda muestra un hueco de ese tamaño y ninguna fila a medias (cada escritura es una transacción).", where: "el resumen de la sonda; `systemctl show -p NRestarts <unidad>`; el journal."},
	},
	2: {
		"en": {title: "kill -9 PostgreSQL under load", what: "SIGKILLs the postmaster while a probe runs.", expect: "the engine stays up answering fast 503s (breaker); PostgreSQL crash-recovers from WAL and is restarted by systemd (Restart=on-failure, ~5 s); the engine reconnects alone; its NRestarts does not move.", where: "the probe summary; `systemctl status postgresql@*-main`; /admin → Resources (503 counter)."},
		"es": {title: "kill -9 a PostgreSQL bajo carga", what: "manda SIGKILL al postmaster mientras corre una sonda.", expect: "el motor sigue arriba respondiendo 503 rápidos (breaker); PostgreSQL se recupera del WAL y systemd lo reinicia (Restart=on-failure, ~5 s); el motor reconecta solo; su NRestarts no se mueve.", where: "el resumen de la sonda; `systemctl status postgresql@*-main`; /admin → Recursos (contador de 503)."},
	},
	3: {
		"en": {title: "reboot the box under load", what: "reboots the box (only with --yes-reboot; otherwise prints the recipe). Probe from ANOTHER machine with `appximo drill probe --url https://…`.", expect: "a full outage of ~12–40 s, then everything back in the right order (PostgreSQL → engine → Caddy) with no intervention and nothing in `failed`.", where: "the outside probe summary; `systemctl --failed` after the boot; `journalctl -b -u <unit>`."},
		"es": {title: "reiniciar la caja bajo carga", what: "reinicia la caja (solo con --yes-reboot; si no, imprime la receta). Sondee desde OTRA máquina con `appximo drill probe --url https://…`.", expect: "un corte total de ~12–40 s y después todo arriba en el orden correcto (PostgreSQL → motor → Caddy), sin intervención y nada en `failed`.", where: "el resumen de la sonda de afuera; `systemctl --failed` después del arranque; `journalctl -b -u <unidad>`."},
	},
	4: {
		"en": {title: "fill the disk", what: "allocates a file on the backup directory's filesystem until the free space crosses the alert floor (APPXIMO_DISK_MIN_FREE_PCT / _MB); with --full, to 100 %. Deletes it on exit.", expect: "within one collector tick the disk gauge flags LOW and the alert is recorded (journal / Slack); reads keep answering 200; a backup run still succeeds while its set fits (with --full it fails at once naming the cause); at 100 % PostgreSQL may PANIC and restart when it needs a new WAL segment (recovers alone once space is freed).", where: "/admin → Resources → Live → \"Disk & backup\" (LOW); /metrics appximo_selfmon_disk_free_bytes; journal 'alert'."},
		"es": {title: "llenar el disco", what: "reserva un archivo en el sistema de archivos del directorio de backups hasta cruzar el piso de alerta (APPXIMO_DISK_MIN_FREE_PCT / _MB); con --full, hasta el 100 %. Lo borra al salir.", expect: "en un tick del colector la tarjeta de disco marca LOW y la alerta queda registrada (journal / Slack); las lecturas siguen en 200; un backup sigue funcionando mientras el set quepa (con --full falla de inmediato nombrando la causa); al 100 % PostgreSQL puede hacer PANIC y reiniciarse cuando necesite un segmento de WAL nuevo (se recupera solo al liberar espacio).", where: "/admin → Recursos → En vivo → «Disco y backup» (LOW); /metrics appximo_selfmon_disk_free_bytes; journal 'alert'."},
	},
	5: {
		"en": {title: "memory to the OOM edge", what: "a Python process allocates memory until MemAvailable+SwapFree drops under the guard's floor (APPXIMO_MEMORY_GUARD_MIN_MB), holds it 20 s, and exits.", expect: "WRITES answer 503 + Retry-After: 5 (the probe uses a write that changes nothing: a transaction deleting a non-existent id); READS keep answering 200; nothing is OOM-killed while the guard holds; writes come back alone when memory returns. Degrading, not holding: a foreign process can still trigger the kernel's OOM killer.", where: "journal: 'memory guard'; /admin → Resources → Live (503 counter, host memory); `dmesg | grep -i oom` (must be empty)."},
		"es": {title: "memoria hasta el borde del OOM", what: "un proceso Python reserva memoria hasta que MemAvailable+SwapFree cae bajo el piso del guardia (APPXIMO_MEMORY_GUARD_MIN_MB), la sostiene 20 s y sale.", expect: "las ESCRITURAS responden 503 + Retry-After: 5 (la sonda usa una escritura que no cambia nada: una transacción que borra un id inexistente); las LECTURAS siguen en 200; nada muere por OOM mientras el guardia frena; las escrituras vuelven solas cuando vuelve la memoria. Degrada, no aguanta: un proceso ajeno puede igual disparar el OOM killer del kernel.", where: "journal: 'memory guard'; /admin → Recursos → En vivo (contador de 503, memoria del host); `dmesg | grep -i oom` (debe estar vacío)."},
	},
	6: {
		"en": {title: "network to the database black-holed (ENG-59)", what: "drops the app's packets to port 5432 (iptables, scoped to the app's own user) for 25 s under a 10 rps probe, then removes the rule.", expect: "the first failures take the 5 s query deadline; within ~2 s of those the breaker OPENS on 20 consecutive failures and the rest fail in < 0.2 s (p50 of failures ≈ 0.00 s, not 5.00 s); half-open probes every 8 s; recovery < 1 s after the rule is removed. Other apps on the box are untouched.", where: "the probe summary (p50 of failures, % < 200 ms, recovery); journal 'circuit breaker'; /admin → Resources (503 counter)."},
		"es": {title: "red a la base en agujero negro (ENG-59)", what: "descarta los paquetes de la app hacia el puerto 5432 (iptables, acotado al usuario de la app) durante 25 s bajo una sonda de 10 rps, y después quita la regla.", expect: "las primeras fallas pagan el deadline de 5 s de consulta; ~2 s después el breaker ABRE por 20 fallas consecutivas y el resto falla en < 0,2 s (p50 de las fallas ≈ 0,00 s, no 5,00 s); sondas half-open cada 8 s; recuperación < 1 s al quitar la regla. Las otras apps de la caja no se enteran.", where: "el resumen de la sonda (p50 de fallas, % < 200 ms, recuperación); journal 'circuit breaker'; /admin → Recursos (contador de 503)."},
	},
	7: {
		"en": {title: "200 ms of latency to the database", what: "adds 200 ms of netem delay to the packets going to port 5432 (tc on the loopback, filtered by port) for 20 s under a probe with 40 concurrent readers, then removes it.", expect: "DEGRADES, does not tip: every query pays the round trips (hundreds of ms); the pool's effective capacity collapses (10 conns / 0.2 s ≈ 50 qps), so the admission control sheds the excess with 429 and some requests hit the 5 s deadline (503); the verdict says pool_exhausted / db_bound, not CPU; back to baseline alone when the delay goes.", where: "/admin → Resources → Live: query p99, pool acquired/max, 429/503; the verdict."},
		"es": {title: "200 ms de latencia hacia la base", what: "agrega 200 ms de retardo netem a los paquetes hacia el puerto 5432 (tc en loopback, filtrado por puerto) durante 20 s bajo una sonda con 40 lectores concurrentes, y después lo quita.", expect: "DEGRADA, no vuelca: cada consulta paga los round trips (cientos de ms); la capacidad efectiva del pool se derrumba (10 conexiones / 0,2 s ≈ 50 qps), así que la admisión recorta el exceso con 429 y algunas requests llegan al deadline de 5 s (503); el veredicto dice pool_exhausted / db_bound, no CPU; vuelve solo a la línea base al quitar el retardo.", where: "/admin → Recursos → En vivo: p99 de consulta, pool acquired/max, 429/503; el veredicto."},
	},
	8: {
		"en": {title: "clock 2 hours backwards", what: "sets the system clock 2 h back, checks an already-issued token and a freshly minted one, does a write that changes nothing, and restores the clock (+2 h, then the time daemon).", expect: "both tokens are accepted (exp is still in the future; the engine does not validate iat/nbf); the write path works; the engine does not restart; the collector may show one odd tick.", where: "the drill's own lines; `timedatectl` before/after; journal (no restart)."},
		"es": {title: "reloj 2 horas hacia atrás", what: "atrasa el reloj del sistema 2 h, prueba un token ya emitido y uno recién acuñado, hace una escritura que no cambia nada y restaura el reloj (+2 h y luego el demonio de hora).", expect: "los dos tokens son aceptados (exp sigue en el futuro; el motor no valida iat/nbf); el camino de escritura funciona; el motor no se reinicia; el colector puede mostrar un tick raro.", where: "las líneas del propio drill; `timedatectl` antes/después; journal (sin reinicio)."},
	},
	9: {
		"en": {title: "two concurrent writes to the same row", what: "PATCHes the same row twice at the same instant with two different values of one text field; if the resource has a state machine, also fires two conflicting transitions at once.", expect: "plain field: both 200, the row holds the LAST committed value (the row lock serializes them, nothing is torn); state machine: exactly one transition wins, the other is 422 (the guard is in the UPDATE's WHERE) — never a double move.", where: "the drill's own lines (both statuses + the final value); /admin → Data → the resource."},
		"es": {title: "dos escrituras concurrentes a la misma fila", what: "hace PATCH a la misma fila dos veces en el mismo instante con dos valores distintos de un campo de texto; si el recurso tiene máquina de estados, además dispara dos transiciones en conflicto a la vez.", expect: "campo simple: los dos 200, la fila queda con el ÚLTIMO valor commiteado (el lock de fila los serializa, nada queda a medias); máquina de estados: exactamente una transición gana, la otra es 422 (la guarda va en el WHERE del UPDATE) — nunca una doble transición.", where: "las líneas del propio drill (los dos estados + el valor final); /admin → Datos → el recurso."},
	},
	10: {
		"en": {title: "the pool full of slow queries", what: "holds an ACCESS EXCLUSIVE lock on one table for 20 s (psql) while 60 concurrent readers of that table arrive.", expect: "the first 10 readers take every pool connection and wait; the admission control sheds the rest with an immediate 429; the waiters hit the 5 s deadline → 503; the breaker may open on 20 consecutive timeouts; nothing crashes; when the lock goes, the next reads are 200 in milliseconds.", where: "/admin → Resources → Live: pool acquired 10/10, empty-acquire waits, 429/503; the verdict `pool_exhausted`."},
		"es": {title: "el pool lleno de consultas lentas", what: "sostiene un lock ACCESS EXCLUSIVE sobre una tabla durante 20 s (psql) mientras llegan 60 lectores concurrentes de esa tabla.", expect: "los primeros 10 lectores toman todas las conexiones del pool y esperan; la admisión recorta al resto con un 429 inmediato; los que esperan llegan al deadline de 5 s → 503; el breaker puede abrir con 20 timeouts seguidos; nada se cae; al soltar el lock, las siguientes lecturas son 200 en milisegundos.", where: "/admin → Recursos → En vivo: pool acquired 10/10, esperas por conexión, 429/503; el veredicto `pool_exhausted`."},
	},
}

// ── target resolution ───────────────────────────────────────────────────────

type drillTarget struct {
	app, service, url, domain string
	controlPort               int
	schemaPath                string
	schemaRaw                 []byte
	schema                    *schema.APISchema
	dsn, jwtSecret, adminKey  string
	scriptsDir                string
	envFile                   string
	env                       map[string]string
	prodReason                string // non-empty ⇒ looks like production
	lang                      string
	production                bool
	yes                       bool
}

func drillLang(cmd *cobra.Command) string {
	l, _ := cmd.Flags().GetString("lang")
	if l == "" {
		for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"} {
			if v := os.Getenv(k); v != "" {
				l = v
				break
			}
		}
	}
	if strings.HasPrefix(strings.ToLower(l), "es") {
		return "es"
	}
	return "en"
}

var execStartPortRe = regexp.MustCompile(`--port[= ](\d+)`)
var execStartSchemaRe = regexp.MustCompile(`--schema[= ]([^ ;]+)`)
var execStartCtrlRe = regexp.MustCompile(`--control-port[= ](\d+)`)

// resolveDrillTarget builds the target from --app (an installed box) or from
// --url + the environment. It never prints a secret.
func resolveDrillTarget(cmd *cobra.Command, needSchema bool) (*drillTarget, error) {
	t := &drillTarget{env: map[string]string{}, lang: drillLang(cmd)}
	t.app, _ = cmd.Flags().GetString("app")
	t.url, _ = cmd.Flags().GetString("url")
	t.controlPort, _ = cmd.Flags().GetInt("control-port")
	t.schemaPath, _ = cmd.Flags().GetString("schema")
	t.domain, _ = cmd.Flags().GetString("domain")
	t.scriptsDir, _ = cmd.Flags().GetString("scripts")
	t.production, _ = cmd.Flags().GetBool("production")
	t.yes, _ = cmd.Flags().GetBool("yes")

	port := 0
	if t.app != "" {
		if !tenantIDRe.MatchString(t.app) && !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`).MatchString(t.app) {
			return nil, fmt.Errorf("--app %q: not an app name", t.app)
		}
		t.service = t.app
		t.envFile = filepath.Join("/etc", t.app, t.app+".env")
		if err := loadEnvFileInto(t.envFile, t.env); err != nil {
			return nil, fmt.Errorf("--app %s: %v (is this an installed box? the installer writes /etc/<app>/<app>.env)", t.app, err)
		}
		// Export for the companion scripts and for our own reads below. The
		// real environment wins (an operator's override is deliberate).
		for k, v := range t.env {
			if os.Getenv(k) == "" {
				_ = os.Setenv(k, v)
			}
		}
		// The unit is the truth for port/schema/binary (deploy-app.sh's rule).
		if out, err := exec.Command("systemctl", "show", "-p", "ExecStart", "--value", t.service).Output(); err == nil {
			s := string(out)
			if m := execStartPortRe.FindStringSubmatch(s); m != nil {
				port, _ = strconv.Atoi(m[1])
			}
			if m := execStartSchemaRe.FindStringSubmatch(s); m != nil && t.schemaPath == "" {
				t.schemaPath = m[1]
			}
			if m := execStartCtrlRe.FindStringSubmatch(s); m != nil && t.controlPort == 0 {
				t.controlPort, _ = strconv.Atoi(m[1])
			}
		}
		if t.schemaPath == "" {
			if p := filepath.Join("/etc", t.app, "schema.json"); fileExists(p) {
				t.schemaPath = p
			}
		}
		if t.scriptsDir == "" {
			if d := filepath.Join("/opt", t.app, "scripts"); dirExists(d) {
				t.scriptsDir = d
			}
		}
		if t.domain == "" {
			t.domain = caddyDomainFor(t.app, port)
		}
	}
	if t.url == "" {
		if port == 0 {
			port = 8080
		}
		t.url = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	t.url = strings.TrimRight(t.url, "/")
	if t.controlPort == 0 {
		t.controlPort = resolveControlPort(0)
		if v := firstNonEmpty(os.Getenv("APPXIMO_CONTROL_PORT"), os.Getenv("APPITOOLS_CONTROL_PORT")); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				t.controlPort = n
			}
		}
	}
	t.dsn = os.Getenv("DATABASE_URL")
	t.jwtSecret = firstNonEmpty(os.Getenv("JWT_SECRET"), os.Getenv("APPITOOLS_JWT_SECRET"))
	t.adminKey = firstNonEmpty(os.Getenv("ADMIN_KEY"), os.Getenv("APPITOOLS_ADMIN_KEY"))
	if t.scriptsDir == "" {
		t.scriptsDir = findRepoScripts()
	}
	if needSchema {
		if err := t.loadSchema(); err != nil {
			return nil, err
		}
	}
	t.prodReason = t.detectProduction()
	return t, nil
}

func (t *drillTarget) loadSchema() error {
	var err error
	if t.schemaPath != "" {
		t.schemaRaw, err = os.ReadFile(t.schemaPath)
		if err != nil {
			return fmt.Errorf("schema %s: %v", t.schemaPath, err)
		}
	} else {
		status, body, herr := doJSONTimeout(http.MethodGet, t.url+"/editor/current-schema", nil, nil, 10*time.Second)
		if herr != nil || status != 200 || !json.Valid(body) {
			return fmt.Errorf("no --schema and GET %s/editor/current-schema did not answer the boot schema (status %d, %v) — is the engine up? pass --url / --schema", t.url, status, herr)
		}
		t.schemaRaw = body
		t.schemaPath = "(served: " + t.url + "/editor/current-schema)"
	}
	t.schema, err = schema.LoadFromBytes(t.schemaRaw)
	if err != nil {
		return fmt.Errorf("schema %s does not load: %v", t.schemaPath, err)
	}
	return nil
}

// detectProduction returns a non-empty reason when the target looks like
// production. It is a HEURISTIC (a public domain, an address in the protected
// belt), deliberately conservative: a false positive costs one flag; a false
// negative costs a customer's afternoon.
func (t *drillTarget) detectProduction() string {
	u, err := url.Parse(t.url)
	if err == nil {
		h := u.Hostname()
		if !isLoopbackHost(h) && hasPublicTLD(h) {
			return fmt.Sprintf("the URL %s has a public domain", t.url)
		}
		if h != "" && inProtectedBelt(h) {
			return fmt.Sprintf("%s is in the protected belt (/root/.applab-protected)", h)
		}
	}
	if t.domain != "" && hasPublicTLD(t.domain) {
		return fmt.Sprintf("the app is served at the public domain %s", t.domain)
	}
	if t.app != "" {
		for _, ip := range localIPs() {
			if inProtectedBelt(ip) {
				return fmt.Sprintf("this box (%s) is in the protected belt (/root/.applab-protected)", ip)
			}
		}
	}
	return ""
}

// guard enforces the safety class: a drill that loads/breaks/restores for real
// refuses a production-looking target without --production.
func (t *drillTarget) guard(class string) error {
	if t.prodReason == "" || class == "readonly" || class == "ephemeral" || class == "scratch" {
		return nil
	}
	if t.production {
		fmt.Fprintf(os.Stderr, drillT(t.lang, "prod.note")+"\n", t.prodReason)
		return nil
	}
	return fmt.Errorf(drillT(t.lang, "refuse"), t.prodReason)
}

func (t *drillTarget) intro(name string, extra ...string) {
	tx := drillTexts[name][t.lang]
	fmt.Printf("▶ appximo drill %s — %s\n", name, tx.title)
	fmt.Printf("  %s %s\n", drillT(t.lang, "hdr.what"), tx.what)
	fmt.Printf("  %s %s\n", drillT(t.lang, "hdr.expect"), tx.expect)
	fmt.Printf("  %s %s\n", drillT(t.lang, "hdr.where"), tx.where)
	for _, e := range extra {
		fmt.Printf("  %s\n", e)
	}
	fmt.Println()
}

func isLoopbackHost(h string) bool {
	if h == "" || h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	return false
}

var nonPublicTLDs = map[string]bool{"localhost": true, "internal": true, "local": true, "lan": true, "test": true,
	"example": true, "invalid": true, "home": true, "intranet": true, "corp": true, "private": true}

func hasPublicTLD(h string) bool {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if net.ParseIP(h) != nil || !strings.Contains(h, ".") {
		return false
	}
	parts := strings.Split(h, ".")
	return !nonPublicTLDs[parts[len(parts)-1]]
}

func inProtectedBelt(host string) bool {
	belt := map[string]bool{}
	add := func(s string) {
		for _, line := range strings.Split(s, "\n") {
			f := strings.Fields(line)
			if len(f) > 0 && !strings.HasPrefix(f[0], "#") {
				belt[f[0]] = true
			}
		}
	}
	if b, err := os.ReadFile("/root/.applab-protected"); err == nil {
		add(string(b))
	}
	if v := os.Getenv("APPLAB_PROTECTED_IPS"); v != "" {
		add(strings.ReplaceAll(v, ",", "\n"))
	}
	if belt[host] {
		return true
	}
	if ips, err := net.LookupHost(host); err == nil {
		for _, ip := range ips {
			if belt[ip] {
				return true
			}
		}
	}
	return false
}

func localIPs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
			out = append(out, ipn.IP.String())
		}
	}
	return out
}

// caddyDomainFor finds the site that proxies to the app's port — in the
// installer's layout (/etc/caddy/sites/<app>.caddy) or in a monolithic
// Caddyfile (the pre-OPS-10 layout still serving the demo box).
func caddyDomainFor(app string, port int) string {
	files := []string{filepath.Join("/etc/caddy/sites", app+".caddy"), "/etc/caddy/Caddyfile"}
	if dir, err := os.ReadDir("/etc/caddy/sites"); err == nil {
		for _, e := range dir {
			files = append(files, filepath.Join("/etc/caddy/sites", e.Name()))
		}
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		site := ""
		for _, line := range strings.Split(string(b), "\n") {
			trim := strings.TrimSpace(line)
			if trim == "" || strings.HasPrefix(trim, "#") {
				continue
			}
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.HasSuffix(trim, "{") {
				// A block-opening line with nothing before the "{" — Caddy's
				// global options block `{`, or a snippet head — leaves no field;
				// Fields("")[0] used to panic here (found by `drill error` on
				// the 58's inline Caddyfile). No site name: skip the block.
				if f := strings.Fields(strings.TrimSuffix(trim, "{")); len(f) > 0 {
					site = f[0]
				} else {
					site = ""
				}
				continue
			}
			if port > 0 && strings.Contains(trim, "reverse_proxy") && strings.Contains(trim, ":"+strconv.Itoa(port)) && site != "" && !strings.HasPrefix(site, ":") {
				return strings.TrimPrefix(strings.TrimPrefix(site, "https://"), "http://")
			}
		}
	}
	return ""
}

func loadEnvFileInto(path string, into map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		into[strings.TrimSpace(k)] = v
	}
	return nil
}

func findRepoScripts() string {
	cands := []string{"scripts"}
	if exe, err := os.Executable(); err == nil {
		d := filepath.Dir(exe)
		cands = append(cands, filepath.Join(d, "scripts"), filepath.Join(d, "..", "scripts"), filepath.Join(d, "..", "..", "scripts"))
	}
	for _, c := range cands {
		if fileExists(filepath.Join(c, "drill.sh")) {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

func (t *drillTarget) script(name string) (string, error) {
	if t.scriptsDir != "" && fileExists(filepath.Join(t.scriptsDir, name)) {
		return filepath.Join(t.scriptsDir, name), nil
	}
	return "", fmt.Errorf("%s not found (looked in %q) — it ships in the repo's scripts/ and the installer places it in /opt/<app>/scripts; pass --scripts=DIR", name, t.scriptsDir)
}

func fileExists(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }
func dirExists(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }
func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

var tenantIDRe = regexp.MustCompile(`^[a-z][a-z0-9]{1,29}$`)

// ── shared HTTP helpers ─────────────────────────────────────────────────────

func (t *drillTarget) mint(tenant, role string) (string, error) {
	if t.jwtSecret == "" {
		return "", errors.New("JWT_SECRET is not set (pass --app, or export it / put it in .env) — needed to mint the probe token")
	}
	return auth.GenerateToken(auth.Claims{UserID: "00000000-0000-4000-8000-00000000d111", Role: role, TenantID: tenant}, t.jwtSecret)
}

func (t *drillTarget) adminGet(path string, timeout time.Duration) (int, []byte, error) {
	if t.adminKey == "" {
		return 0, nil, errors.New("ADMIN_KEY is not set (pass --app, or export it / put it in .env)")
	}
	// Not doJSONTimeout: that helper caps the body at 64 KiB (right for a control-
	// plane answer), and a window of 300 self-monitor ticks is a few MiB.
	req, err := http.NewRequest(http.MethodGet, t.url+path, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Admin-Key", t.adminKey)
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	return resp.StatusCode, b, err
}

// selfmonTick reads the newest self-monitor tick (switching the collector to
// its 1 s live cadence, exactly like the panel's Live tab does).
type selfmonTick struct {
	attr, owner, reason string
	rps, p99, qp99      float64
	acquired, maxConns  int64
	s429, s503, s5xx    int64
	ok                  bool
}

func (t *drillTarget) selfmonTick() selfmonTick {
	var out selfmonTick
	status, body, err := t.adminGet("/admin/resources?live=1&series=1", 4*time.Second)
	if err != nil || status != 200 {
		return out
	}
	var d struct {
		Latest struct {
			Attribution string `json:"attribution"`
			Verdict     struct {
				Owner, Reason string
			} `json:"verdict"`
			Request struct {
				RPS       float64 `json:"rps"`
				P99       float64 `json:"latency_p99_ms"`
				Status429 int64   `json:"status_429"`
				Status503 int64   `json:"status_503"`
				Errors5xx int64   `json:"errors_5xx"`
			} `json:"request"`
			DB struct {
				Acquired int64   `json:"acquired_conns"`
				Max      int64   `json:"max_conns"`
				QP99     float64 `json:"query_latency_p99_ms"`
			} `json:"db_client"`
		} `json:"latest"`
	}
	if json.Unmarshal(body, &d) != nil {
		return out
	}
	l := d.Latest
	return selfmonTick{attr: l.Attribution, owner: l.Verdict.Owner, reason: l.Verdict.Reason, rps: l.Request.RPS, p99: l.Request.P99,
		qp99: l.DB.QP99, acquired: l.DB.Acquired, maxConns: l.DB.Max, s429: l.Request.Status429, s503: l.Request.Status503, s5xx: l.Request.Errors5xx, ok: true}
}

func (t *drillTarget) selfmonWindow(sinceMs int64) (string, bool) {
	status, body, err := t.adminGet(fmt.Sprintf("/admin/resources?since=%d", sinceMs), 6*time.Second)
	if err != nil || status != 200 {
		fmt.Fprintf(os.Stderr, "(no window verdict: GET /admin/resources?since= → %d %v)\n", status, err)
		return "", false
	}
	var d struct {
		Window struct {
			Dominant     string         `json:"dominant"`
			Owner        string         `json:"owner"`
			Reason       string         `json:"reason"`
			Distribution map[string]int `json:"distribution"`
			PeakRPS      float64        `json:"peak_rps"`
			PeakP99      float64        `json:"peak_p99_ms"`
			Ticks        int            `json:"ticks"`
			Shed         int64          `json:"shed_429_503"`
			Errors5xx    int64          `json:"errors_5xx"`
		} `json:"window"`
	}
	if uerr := json.Unmarshal(body, &d); uerr != nil {
		fmt.Fprintf(os.Stderr, "(no window verdict: %v)\n", uerr)
		return "", false
	}
	w := d.Window
	dist := make([]string, 0, len(w.Distribution))
	for k, v := range w.Distribution {
		dist = append(dist, fmt.Sprintf("%s×%d", k, v))
	}
	sort.Strings(dist)
	return fmt.Sprintf("window verdict: %s (owner: %s) over %d ticks — peak %.0f rps, peak p99 %.1f ms, shed %d, 5xx %d\n    %s\n    ticks: %s",
		w.Dominant, w.Owner, w.Ticks, w.PeakRPS, w.PeakP99, w.Shed, w.Errors5xx, w.Reason, strings.Join(dist, " ")), true
}

// ── the open-model load generator ───────────────────────────────────────────

type genStats struct {
	sent, done, inflight int64
	status               sync.Map // int → *int64
	limiter, admission   int64    // the two 429 doors, told apart by body
	netErr               int64
	mu                   sync.Mutex
	lat                  []float64 // ms, completed requests
}

func (g *genStats) count(code int) {
	v, _ := g.status.LoadOrStore(code, new(int64))
	atomic.AddInt64(v.(*int64), 1)
}

func (g *genStats) snapshot() (map[int]int64, []float64) {
	m := map[int]int64{}
	g.status.Range(func(k, v any) bool { m[k.(int)] = atomic.LoadInt64(v.(*int64)); return true })
	g.mu.Lock()
	lat := append([]float64(nil), g.lat...)
	g.mu.Unlock()
	return m, lat
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * p)
	return sorted[i]
}

// runOpenLoop fires requests on a fixed schedule for dur; the schedule never
// waits for the server (coordinated omission is not hidden). In-flight is
// capped at max(64, 4×rate) so a dead server cannot exhaust the client.
func runOpenLoop(ctx context.Context, client *http.Client, mk func(n int) (*http.Request, error), rate float64, dur time.Duration, onSecond func(sec int, g *genStats)) *genStats {
	g := &genStats{}
	cap64 := int64(4 * rate)
	if cap64 < 64 {
		cap64 = 64
	}
	interval := time.Duration(float64(time.Second) / rate)
	deadline := time.Now().Add(dur)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	secTick := time.NewTicker(time.Second)
	defer secTick.Stop()
	var wg sync.WaitGroup
	n, sec := 0, 0
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			wg.Wait()
			return g
		case <-secTick.C:
			sec++
			if onSecond != nil {
				onSecond(sec, g)
			}
		case <-tick.C:
			n++
			if atomic.LoadInt64(&g.inflight) >= cap64 {
				atomic.AddInt64(&g.netErr, 1)
				g.count(0)
				continue
			}
			req, err := mk(n)
			if err != nil {
				continue
			}
			atomic.AddInt64(&g.sent, 1)
			atomic.AddInt64(&g.inflight, 1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer atomic.AddInt64(&g.inflight, -1)
				t0 := time.Now()
				resp, err := client.Do(req)
				ms := float64(time.Since(t0)) / float64(time.Millisecond)
				if err != nil {
					atomic.AddInt64(&g.netErr, 1)
					g.count(0)
					return
				}
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
				resp.Body.Close()
				g.count(resp.StatusCode)
				if resp.StatusCode == 429 {
					if strings.Contains(string(body), "capacity") {
						atomic.AddInt64(&g.admission, 1)
					} else {
						atomic.AddInt64(&g.limiter, 1)
					}
				}
				atomic.AddInt64(&g.done, 1)
				g.mu.Lock()
				g.lat = append(g.lat, ms)
				g.mu.Unlock()
			}()
		}
	}
	wg.Wait()
	return g
}

func statusLine(g *genStats) string {
	m, lat := g.snapshot()
	sort.Float64s(lat)
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		name := strconv.Itoa(k)
		if k == 0 {
			name = "err"
		}
		parts = append(parts, fmt.Sprintf("%s=%d", name, m[k]))
	}
	return fmt.Sprintf("sent=%d %s p50=%.1fms p99=%.1fms", atomic.LoadInt64(&g.sent), strings.Join(parts, " "), pct(lat, 0.5), pct(lat, 0.99))
}

func drillHTTPClient(insecure bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 512
	tr.MaxConnsPerHost = 0
	if insecure {
		tr.TLSClientConfig = tlsInsecure()
	}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

// ── drill error ─────────────────────────────────────────────────────────────

var drillErrorCmd = &cobra.Command{
	Use:   "error",
	Short: "Provoke a real 500 on an ephemeral tenant and show where it is explained",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		t, err := resolveDrillTarget(cmd, true)
		if err != nil {
			return err
		}
		if err := t.guard(drillSafety["error"]); err != nil {
			return err
		}
		if t.dsn == "" || t.adminKey == "" {
			return errors.New("drill error needs DATABASE_URL and ADMIN_KEY (pass --app on an installed box, or export them)")
		}
		resource, _ := cmd.Flags().GetString("resource")
		keep, _ := cmd.Flags().GetBool("keep")
		t.intro("error")

		// 1. an ephemeral tenant with THIS app's schema
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
		cleanup := func() {
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
			var left int
			_ = pool.QueryRow(ctx, "select count(*) from pg_namespace where nspname=$1", "tenant_"+eph).Scan(&left)
			fmt.Printf("✓ tenant %s deleted (schema + control-plane rows; schemas left: %d)\n", eph, left)
		}
		defer cleanup()

		// 2. break one table underneath the engine
		pgSchema := "tenant_" + eph
		res, body, insertable := drillPickInsertable(t.schemaRaw, resource)
		if res == "" {
			names := sortedResourceNames(t.schema)
			if len(names) == 0 {
				return errors.New("the schema declares no resources")
			}
			res = names[0]
		}
		token, err := t.mint(eph, role)
		if err != nil {
			return err
		}
		hdr := map[string]string{"Authorization": "Bearer " + token, "Host": eph + ".localhost", "Content-Type": "application/json"}
		var method, path string
		var reqBody []byte
		if insertable {
			_, err = pool.Exec(ctx, fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s.drill_boom() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'provoked by appximo drill error: storage says no'; END $$;
CREATE TRIGGER drill_boom BEFORE INSERT ON %s.%s FOR EACH ROW EXECUTE FUNCTION %s.drill_boom();`, pgSchema, pgSchema, quoteIdent(res), pgSchema))
			if err != nil {
				return fmt.Errorf("install trigger: %v", err)
			}
			fmt.Printf("• BEFORE INSERT trigger on %s.%s RAISEs — the driver will reject every insert (SQLSTATE P0001)\n", pgSchema, res)
			method, path = http.MethodPost, "/api/"+res
			reqBody, _ = json.Marshal(body)
		} else {
			// No resource whose required fields can be filled blindly (patterns,
			// relations): break the READ instead. A dropped table would be mapped
			// to 400 "invalid tenant" on purpose (42P01 = a missing tenant schema),
			// so the table is renamed and a VIEW with its name calls a function that
			// RAISEs — every SELECT is a real driver error (P0001) with a readable
			// message and the failed statement on the trace.
			if _, err = pool.Exec(ctx, fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s.drill_boom_read() RETURNS boolean LANGUAGE plpgsql STABLE AS $$ BEGIN RAISE EXCEPTION 'provoked by appximo drill error: storage says no'; END $$;
ALTER TABLE %s.%s RENAME TO %s;
CREATE VIEW %s.%s AS SELECT * FROM %s.%s WHERE %s.drill_boom_read();`,
				pgSchema, pgSchema, quoteIdent(res), quoteIdent(res+"_drill_real"), pgSchema, quoteIdent(res), pgSchema, quoteIdent(res+"_drill_real"), pgSchema)); err != nil {
				return fmt.Errorf("break table: %v", err)
			}
			fmt.Printf("• %s.%s now RAISEs on every read (a view over the renamed table — no resource has required fields a drill can fill blindly, so the READ path is broken instead)\n", pgSchema, res)
			method, path = http.MethodGet, "/api/"+res+"?per_page=1"
		}

		// 3. two requests that hit it
		traces := []string{}
		for i := 1; i <= 2; i++ {
			req, _ := http.NewRequest(method, t.url+path, strings.NewReader(string(reqBody)))
			for k, v := range hdr {
				if k == "Host" {
					req.Host = v
				} else {
					req.Header.Set(k, v)
				}
			}
			resp, err := drillHTTPClient(false).Do(req)
			if err != nil {
				return fmt.Errorf("%s %s: %v", method, path, err)
			}
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
			resp.Body.Close()
			tid := resp.Header.Get("X-Trace-ID")
			traces = append(traces, tid)
			mark := "✓"
			if resp.StatusCode != 500 {
				mark = "✗"
			}
			fmt.Printf("%s %s %s → %d  trace %s  body %s\n", mark, method, path, resp.StatusCode, tid, firstLine(string(b)))
			if resp.StatusCode != 500 {
				return fmt.Errorf("expected a 500, got %d — the engine did not treat this as a server error (report it: docs/BACKLOG.md)", resp.StatusCode)
			}
		}

		// 4. the evidence, from the same API the panel reads
		fmt.Println("• reading /admin/observability/tenants/" + eph + " (what the panel's Issues tab shows)…")
		var found bool
		for i := 0; i < 20 && !found; i++ {
			status, ob, err := t.adminGet("/admin/observability/tenants/"+eph, 6*time.Second)
			if err == nil && status == 200 {
				var d struct {
					Groups []map[string]any `json:"error_groups"`
					Traces []map[string]any `json:"recent_traces"`
				}
				if json.Unmarshal(ob, &d) == nil && len(d.Groups) > 0 {
					found = true
					for _, g := range d.Groups {
						fmt.Printf("✓ problem group: route=%v status=%v count=%v users=%d\n    message: %v\n    top frame: %v\n",
							g["route"], g["status"], g["count"], lenAny(g["users"]), trunc(fmt.Sprint(g["message"]), 160), g["top_frame"])
					}
					for _, tr := range d.Traces {
						if st, _ := tr["status"].(float64); st >= 500 {
							fmt.Printf("✓ 5xx trace %v: route=%v user=%v role=%v\n    error: %v\n", tr["trace_id"], tr["route"], tr["user_id"], tr["role"], trunc(fmt.Sprint(tr["error_msg"]), 160))
							if sql, ok := tr["sql"].(string); ok && sql != "" {
								fmt.Printf("    failed statement: %v\n", trunc(sql, 160))
							}
							break
						}
					}
				}
			}
			if !found {
				time.Sleep(500 * time.Millisecond)
			}
		}
		if !found {
			fmt.Println("✗ no error group appeared within 10 s — check that ADMIN_KEY matches the running engine and that OBS_DB_PATH is writable")
		}
		fmt.Printf("\n%s\n", drillWhereNow(t, eph, traces))
		if !keep && !t.yes && stdinIsTTY() {
			fmt.Printf("\nThe tenant %s stays until you press Enter (go look at the panel now); Enter deletes it. ", eph)
			readLine()
		}
		return nil
	},
}

func drillWhereNow(t *drillTarget, eph string, traces []string) string {
	unit := t.service
	if unit == "" {
		unit = "<unit>"
	}
	if t.lang == "es" {
		return fmt.Sprintf("Mírelo ahora:\n  %s/admin → tenant «%s» (arriba a la derecha) → Observabilidad → Problemas\n  → Trazas → %s → Cascada\n  journalctl -u %s -o cat --since -2min | grep '\"level\":\"error\"' | head -2", t.url, eph, strings.Join(traces, " / "), unit)
	}
	return fmt.Sprintf("Look at it now:\n  %s/admin → tenant \"%s\" (top right) → Observability → Issues\n  → Traces → %s → Waterfall\n  journalctl -u %s -o cat --since -2min | grep '\"level\":\"error\"' | head -2", t.url, eph, strings.Join(traces, " / "), unit)
}

func lenAny(v any) int {
	if s, ok := v.([]any); ok {
		return len(s)
	}
	return 0
}

func trunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// drillPickInsertable finds a resource whose REQUIRED fields can all be filled
// without a reference (no required relation/file), and builds a valid body for
// it. Generic over the raw schema so it never depends on internal field types.
func drillPickInsertable(raw []byte, want string) (string, map[string]any, bool) {
	var s struct {
		Resources map[string]struct {
			Fields map[string]map[string]any `json:"fields"`
		} `json:"resources"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return "", nil, false
	}
	names := make([]string, 0, len(s.Resources))
	for n := range s.Resources {
		names = append(names, n)
	}
	sort.Strings(names)
	if want != "" {
		names = []string{want}
	}
	for _, name := range names {
		r, ok := s.Resources[name]
		if !ok {
			continue
		}
		body := map[string]any{}
		fillable := true
		for fname, f := range r.Fields {
			req, _ := f["required"].(bool)
			if _, hasDefault := f["default"]; hasDefault || f["auto"] != nil || !req {
				continue
			}
			typ, _ := f["type"].(string)
			if f["relation"] != nil || typ == "file" {
				fillable = false
				break
			}
			if sm, ok := f["state_machine"].(map[string]any); ok {
				switch init := sm["initial"].(type) {
				case string:
					body[fname] = init
				case []any:
					if len(init) > 0 {
						body[fname] = init[0]
					}
				}
				if body[fname] != nil {
					continue
				}
			}
			if enum, ok := f["enum"].([]any); ok && len(enum) > 0 {
				body[fname] = enum[0]
				continue
			}
			switch typ {
			case "string", "text":
				v := "drill"
				if fmtt, _ := f["format"].(string); fmtt == "email" {
					v = "drill@example.com"
				} else if fmtt == "url" {
					v = "https://example.com/drill"
				} else if fmtt == "uuid" {
					v = "00000000-0000-4000-8000-00000000d111"
				} else if fmtt == "date" {
					v = "2026-01-01"
				} else if f["pattern"] != nil {
					fillable = false
				} else if min, ok := f["minLength"].(float64); ok && min > 5 {
					v = strings.Repeat("d", int(min))
				}
				body[fname] = v
			case "int", "int64", "float64":
				v := 1.0
				if min, ok := f["min"].(float64); ok && min > v {
					v = min
				}
				body[fname] = v
			case "bool":
				body[fname] = true
			case "uuid":
				body[fname] = "00000000-0000-4000-8000-00000000d111"
			case "time":
				body[fname] = time.Now().UTC().Format(time.RFC3339)
			case "json", "jsonb":
				body[fname] = map[string]any{}
			default:
				fillable = false
			}
			if !fillable {
				break
			}
		}
		if fillable {
			return name, body, true
		}
	}
	if want != "" {
		return want, nil, false
	}
	return "", nil, false
}

// ── drill load / saturate ───────────────────────────────────────────────────

func (t *drillTarget) loadTarget(cmd *cobra.Command) (mk func(n int) (*http.Request, error), desc string, err error) {
	tenant, _ := cmd.Flags().GetString("tenant")
	resource, _ := cmd.Flags().GetString("resource")
	role, _ := cmd.Flags().GetString("role")
	token, _ := cmd.Flags().GetString("token")
	path, _ := cmd.Flags().GetString("path")
	if tenant == "" {
		return nil, "", errors.New("--tenant is required: an EXISTING tenant of this app (appximo tenant list)")
	}
	if role == "" {
		role = pickRole(t.schema)
	}
	if resource == "" {
		names := sortedResourceNames(t.schema)
		if len(names) == 0 {
			return nil, "", errors.New("the schema declares no resources")
		}
		resource = names[0]
	}
	if token == "" {
		if token, err = t.mint(tenant, role); err != nil {
			return nil, "", err
		}
	}
	if path == "" {
		path = "/api/" + resource + "?per_page=20&cb={n}"
	}
	host := tenant + ".localhost"
	if t.domain != "" {
		host = tenant + "." + t.domain
	}
	if u, perr := url.Parse(t.url); perr == nil && !isLoopbackHost(u.Hostname()) {
		host = "" // a public URL already carries the tenant in its own Host
	}
	mk = func(n int) (*http.Request, error) {
		p := strings.ReplaceAll(path, "{n}", strconv.Itoa(n))
		req, err := http.NewRequest(http.MethodGet, t.url+p, nil)
		if err != nil {
			return nil, err
		}
		if host != "" {
			req.Host = host
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Cache-Control", "no-cache")
		return req, nil
	}
	return mk, fmt.Sprintf("GET %s%s  (tenant %s, role %s)", t.url, path, tenant, role), nil
}

var drillLoadCmd = &cobra.Command{
	Use:   "load",
	Short: "Steady load against one tenant while the self-monitor's verdict is read live",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		t, err := resolveDrillTarget(cmd, true)
		if err != nil {
			return err
		}
		if err := t.guard(drillSafety["load"]); err != nil {
			return err
		}
		rate, _ := cmd.Flags().GetFloat64("rate")
		dur, _ := cmd.Flags().GetDuration("duration")
		mk, desc, err := t.loadTarget(cmd)
		if err != nil {
			return err
		}
		t.intro("load", fmt.Sprintf("target: %s · %.0f rps × %s", desc, rate, dur))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		start := time.Now().UnixMilli()
		client := drillHTTPClient(false)
		last := ""
		g := runOpenLoop(ctx, client, mk, rate, dur, func(sec int, g *genStats) {
			tk := t.selfmonTick()
			v := "verdict=? (ADMIN_KEY / self-monitor not reachable)"
			if tk.ok {
				v = fmt.Sprintf("verdict=%s (%s) pool=%d/%d q99=%.1fms", tk.attr, tk.owner, tk.acquired, tk.maxConns, tk.qp99)
			}
			line := fmt.Sprintf("t=%02ds %s | %s", sec, statusLine(g), v)
			if sec%5 == 0 || (tk.ok && tk.attr != last) {
				fmt.Println(line)
			}
			if tk.ok {
				last = tk.attr
			}
		})
		fmt.Printf("\nfinal: %s\n", statusLine(g))
		if w, ok := t.selfmonWindow(start); ok {
			fmt.Println(w)
		}
		return nil
	},
}

var drillSaturateCmd = &cobra.Command{
	Use:   "saturate",
	Short: "Climb a ladder of rates past the ceiling and report who sheds (limiter / admission / breaker)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		t, err := resolveDrillTarget(cmd, true)
		if err != nil {
			return err
		}
		if err := t.guard(drillSafety["saturate"]); err != nil {
			return err
		}
		ratesS, _ := cmd.Flags().GetString("rates")
		step, _ := cmd.Flags().GetDuration("step")
		all, _ := cmd.Flags().GetBool("all-levels")
		mk, desc, err := t.loadTarget(cmd)
		if err != nil {
			return err
		}
		var rates []float64
		for _, s := range strings.Split(ratesS, ",") {
			if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && v > 0 {
				rates = append(rates, v)
			}
		}
		if len(rates) == 0 {
			return errors.New("--rates: give at least one positive number")
		}
		t.intro("saturate", fmt.Sprintf("target: %s · ladder %s rps × %s per level", desc, ratesS, step))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		client := drillHTTPClient(false)
		start := time.Now().UnixMilli()
		for _, r := range rates {
			fmt.Printf("── level %.0f rps\n", r)
			g := runOpenLoop(ctx, client, mk, r, step, func(sec int, g *genStats) {
				tk := t.selfmonTick()
				v := ""
				if tk.ok {
					v = fmt.Sprintf(" | verdict=%s pool=%d/%d", tk.attr, tk.acquired, tk.maxConns)
				}
				fmt.Printf("   t=%02ds %s%s\n", sec, statusLine(g), v)
			})
			m, lat := g.snapshot()
			sort.Float64s(lat)
			total := atomic.LoadInt64(&g.sent)
			ok := m[200]
			shed := total - ok
			fmt.Printf("   level %.0f rps: %d sent, %d ok (%.0f%%), 429 limiter=%d admission=%d, 503=%d, err=%d, p50 %.1f ms, p99 %.1f ms\n",
				r, total, ok, 100*float64(ok)/float64(max64(total, 1)), atomic.LoadInt64(&g.limiter), atomic.LoadInt64(&g.admission), m[503], m[0], pct(lat, 0.5), pct(lat, 0.99))
			if ctx.Err() != nil {
				break
			}
			if !all && total > 0 && float64(shed)/float64(total) >= 0.05 {
				who := "the engine"
				switch {
				case atomic.LoadInt64(&g.limiter) > 0 && atomic.LoadInt64(&g.limiter) >= atomic.LoadInt64(&g.admission):
					who = "the per-tenant LIMITER (RATE_LIMIT_RPS — fairness; raise it to see the admission control)"
				case atomic.LoadInt64(&g.admission) > 0:
					who = "the ADMISSION CONTROL (APPXIMO_MAX_INFLIGHT — the box's capacity)"
				case m[503] > 0:
					who = "the BREAKER / memory guard (503)"
				case m[0] > 0:
					who = "the network / client (errors, no answer)"
				}
				fmt.Printf("\n→ shedding began at %.0f rps: %s\n", r, who)
				break
			}
		}
		if w, ok := t.selfmonWindow(start); ok {
			fmt.Println(w)
		}
		return nil
	},
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// ── drill probe ─────────────────────────────────────────────────────────────

var drillProbeCmd = &cobra.Command{
	Use:   "probe",
	Short: "A low-rate probe with an outage summary — run it from ANOTHER machine during a chaos experiment or a deploy",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		lang := drillLang(cmd)
		u, _ := cmd.Flags().GetString("url")
		if u == "" {
			return errors.New("--url is required (e.g. --url https://app.example.com)")
		}
		u = strings.TrimRight(u, "/")
		rate, _ := cmd.Flags().GetFloat64("rate")
		dur, _ := cmd.Flags().GetDuration("duration")
		path, _ := cmd.Flags().GetString("path")
		host, _ := cmd.Flags().GetString("host")
		token, _ := cmd.Flags().GetString("token")
		insecure, _ := cmd.Flags().GetBool("insecure")
		t := &drillTarget{lang: lang, url: u}
		t.intro("probe", fmt.Sprintf("target: %s%s · %.0f rps × %s", u, path, rate, dur))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		type sample struct {
			at   time.Time
			code int
			ms   float64
		}
		var mu sync.Mutex
		var samples []sample
		client := drillHTTPClient(insecure)
		client.Timeout = 8 * time.Second
		mk := func(n int) (*http.Request, error) {
			p := path
			if strings.HasPrefix(p, "/api/") {
				sep := "?"
				if strings.Contains(p, "?") {
					sep = "&"
				}
				p += sep + "cb=" + strconv.Itoa(n)
			}
			req, err := http.NewRequest(http.MethodGet, u+p, nil)
			if err != nil {
				return nil, err
			}
			if host != "" {
				req.Host = host
			}
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			req.Header.Set("Cache-Control", "no-cache")
			return req, nil
		}
		// a thin wrapper that records timestamps per sample
		rec := func(n int) (*http.Request, error) {
			req, err := mk(n)
			if err != nil {
				return nil, err
			}
			at := time.Now()
			req = req.WithContext(context.WithValue(req.Context(), probeKey{}, at))
			return req, nil
		}
		client.Transport = &recordingTransport{next: client.Transport, on: func(at time.Time, code int, ms float64) {
			mu.Lock()
			samples = append(samples, sample{at, code, ms})
			mu.Unlock()
		}}
		runOpenLoop(ctx, client, rec, rate, dur, func(sec int, g *genStats) {
			if sec%10 == 0 {
				fmt.Printf("t=%03ds %s\n", sec, statusLine(g))
			}
		})
		mu.Lock()
		defer mu.Unlock()
		sort.Slice(samples, func(i, j int) bool { return samples[i].at.Before(samples[j].at) })
		fmt.Println("\n" + probeSummary(samples, func(s sample) (time.Time, int, float64) { return s.at, s.code, s.ms }))
		return nil
	},
}

type probeKey struct{}

type recordingTransport struct {
	next http.RoundTripper
	on   func(at time.Time, code int, ms float64)
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	at, _ := req.Context().Value(probeKey{}).(time.Time)
	if at.IsZero() {
		at = time.Now()
	}
	resp, err := r.next.RoundTrip(req)
	ms := float64(time.Since(at)) / float64(time.Millisecond)
	code := 0
	if err == nil {
		code = resp.StatusCode
	}
	r.on(at, code, ms)
	return resp, err
}

// probeSummary is the same arithmetic the CAOS-S1 probes used (probe-drop.sh):
// outage = first failure → first success after the last failure; failure p50 =
// how fast the failures came (the ENG-59 number).
func probeSummary[S any](samples []S, get func(S) (time.Time, int, float64)) string {
	if len(samples) == 0 {
		return "probe: no samples"
	}
	counts := map[int]int{}
	var failMs []float64
	var firstFail, lastFail, firstOKAfter time.Time
	var t0 time.Time
	for i, s := range samples {
		at, code, ms := get(s)
		if i == 0 {
			t0 = at
		}
		counts[code]++
		if code < 200 || code >= 400 {
			failMs = append(failMs, ms)
			if firstFail.IsZero() {
				firstFail = at
			}
			lastFail = at
			firstOKAfter = time.Time{}
		} else if !lastFail.IsZero() && firstOKAfter.IsZero() {
			firstOKAfter = at
		}
	}
	keys := make([]int, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	parts := []string{}
	for _, k := range keys {
		name := strconv.Itoa(k)
		if k == 0 {
			name = "no-answer"
		}
		parts = append(parts, fmt.Sprintf("%s=%d", name, counts[k]))
	}
	out := fmt.Sprintf("probe: %d requests over %.0f s; %s", len(samples), time.Since(t0).Seconds(), strings.Join(parts, " "))
	if len(failMs) == 0 {
		return out + "\nno failures — no outage observed."
	}
	sort.Float64s(failMs)
	fast := 0
	for _, m := range failMs {
		if m < 200 {
			fast++
		}
	}
	out += fmt.Sprintf("\nfailures: %d · first at +%.1f s · last at +%.1f s · p50 %.2f s · p90 %.2f s · <200 ms: %d (%.0f%%)",
		len(failMs), firstFail.Sub(t0).Seconds(), lastFail.Sub(t0).Seconds(), pct(failMs, 0.5)/1000, pct(failMs, 0.9)/1000, fast, 100*float64(fast)/float64(len(failMs)))
	if firstOKAfter.IsZero() {
		out += "\nno success after the last failure — the target was still down when the probe ended."
	} else {
		out += fmt.Sprintf("\noutage: %.1f s (first failure → first success after the last failure); first 200 after the last failure: +%.2f s",
			firstOKAfter.Sub(firstFail).Seconds(), firstOKAfter.Sub(lastFail).Seconds())
	}
	return out
}

// ── drill chaos / restore / audit — the box-level companions ───────────────

var drillChaosCmd = &cobra.Command{
	Use:   "chaos <1-10>",
	Short: "Run one of the ten CAOS-S1 experiments on this box (restored on exit)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || n > 10 {
			return fmt.Errorf("chaos takes a number 1–10 (appximo drill list shows them)")
		}
		t, err := resolveDrillTarget(cmd, false)
		if err != nil {
			return err
		}
		if err := t.guard(drillSafety["chaos"]); err != nil {
			return err
		}
		if t.app == "" {
			return errors.New("drill chaos needs --app=NAME (it acts on the installed unit, its PostgreSQL and the box)")
		}
		script, err := t.script("drill.sh")
		if err != nil {
			return err
		}
		x := drillChaos[n][t.lang]
		fmt.Printf("▶ appximo drill chaos %d — %s\n", n, x.title)
		fmt.Printf("  %s %s\n", drillT(t.lang, "hdr.what"), x.what)
		fmt.Printf("  %s %s\n", drillT(t.lang, "hdr.expect"), x.expect)
		fmt.Printf("  %s %s\n\n", drillT(t.lang, "hdr.where"), x.where)
		tenant, _ := cmd.Flags().GetString("tenant")
		resource, _ := cmd.Flags().GetString("resource")
		full, _ := cmd.Flags().GetBool("full")
		reboot, _ := cmd.Flags().GetBool("yes-reboot")
		sargs := []string{script, "chaos", strconv.Itoa(n), "--app=" + t.app, "--url=" + t.url}
		if tenant != "" {
			sargs = append(sargs, "--tenant="+tenant)
		}
		if resource != "" {
			sargs = append(sargs, "--resource="+resource)
		}
		if full {
			sargs = append(sargs, "--full")
		}
		if reboot {
			sargs = append(sargs, "--yes-reboot")
		}
		if t.yes {
			sargs = append(sargs, "--yes")
		}
		return runCompanion(sargs)
	},
}

var drillRestoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "A timed restore rehearsal into a scratch database (--real runs restore.sh for real)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		t, err := resolveDrillTarget(cmd, false)
		if err != nil {
			return err
		}
		real, _ := cmd.Flags().GetBool("real")
		class := drillSafety["restore"]
		if real {
			class = "box"
		}
		if err := t.guard(class); err != nil {
			return err
		}
		if t.app == "" {
			return errors.New("drill restore needs --app=NAME (it reads /var/backups/<app> and the app's env)")
		}
		script, err := t.script("drill.sh")
		if err != nil {
			return err
		}
		t.intro("restore")
		set, _ := cmd.Flags().GetString("set")
		sargs := []string{script, "restore", "--app=" + t.app, "--url=" + t.url}
		if set != "" {
			sargs = append(sargs, "--set="+set)
		}
		if real {
			sargs = append(sargs, "--real")
		}
		if t.yes {
			sargs = append(sargs, "--yes")
		}
		return runCompanion(sargs)
	},
}

var drillAuditCmd = &cobra.Command{
	Use:   "audit",
	Short: "fleet-audit.sh — what is MISSING on this box (read-only)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		t, err := resolveDrillTarget(cmd, false)
		if err != nil && (t == nil || t.app != "") {
			return err
		}
		if t == nil {
			t = &drillTarget{lang: drillLang(cmd)}
		}
		script, err := t.script("fleet-audit.sh")
		if err != nil {
			return err
		}
		t.intro("audit")
		if t.lang == "es" {
			fmt.Print("  Leyenda: ✓ protegido · ✗ falta (la línea dice qué hacer) · ! aviso. Sale 0 = caja protegida, 1 = al menos una brecha.\n\n")
		} else {
			fmt.Print("  Legend: ✓ protected · ✗ missing (the line says what to do) · ! warning. Exit 0 = protected, 1 = at least one gap.\n\n")
		}
		sargs := []string{script}
		if t.app != "" {
			sargs = append(sargs, "--app="+t.app)
		}
		return runCompanion(sargs)
	},
}

// runCompanion runs a bash companion with our stdio, forwarding Ctrl-C so the
// script's own trap restores what it changed. The exit code is propagated.
func runCompanion(args []string) error {
	c := exec.Command("bash", args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	c.Env = os.Environ()
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

func tlsInsecure() *tls.Config { return &tls.Config{InsecureSkipVerify: true} } //nolint:gosec // a lab box with an internal certificate, by explicit flag
