package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/miguelangel/appitools"
	"github.com/miguelangel/appitools/pkg/fleet"
	"github.com/miguelangel/appitools/pkg/schema"
)

// fleetCmd is the MT-STRUCT-S1 orchestrator: one server, N DISTINCT apps
// (different schemas → different APIs), as N engine processes behind a
// Host-routing proxy. Each app is today's engine, hot path untouched.
var fleetCmd = &cobra.Command{
	Use:   "fleet",
	Short: "Orquesta N apps (schemas distintos) en un servidor — un motor por app + proxy por dominio",
}

var fleetRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Levanta la fleet: un motor por app, supervisados, detrás del proxy Host→app",
	Run: func(cmd *cobra.Command, args []string) {
		cfgPath, _ := cmd.Flags().GetString("config")
		mf, err := fleet.LoadManifest(cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		bin, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "fleet: resolve own binary:", err)
			os.Exit(1)
		}

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		sup := fleet.NewSupervisor(mf, bin)
		if err := sup.Start(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			sup.Shutdown()
			os.Exit(1)
		}

		proxy, err := fleet.NewProxy(mf, sup.Port)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			sup.Shutdown()
			os.Exit(1)
		}

		statusSrv := &http.Server{Addr: mf.StatusAddr, Handler: fleet.StatusHandler(sup), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			// ENG-34: bind first, announce after (see shutdown.Listen).
			ln, err := net.Listen("tcp", mf.StatusAddr)
			if err != nil {
				log.Printf("fleet: status server: %v", err)
				return
			}
			log.Printf("fleet: status/control API on %s (internal — GET /fleet/status)", mf.StatusAddr)
			if err := statusSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Printf("fleet: status server: %v", err)
			}
		}()

		proxySrv := &http.Server{
			Addr:              mf.Listen,
			Handler:           proxy,
			ReadHeaderTimeout: 10 * time.Second,
			// No WriteTimeout: SSE streams through the proxy are long-lived.
			IdleTimeout: 120 * time.Second,
		}
		go func() {
			// ENG-34: bind first, announce after — the proxy is the process's
			// public face, so a pre-bind announcement here is exactly the
			// stale-binary trap app.go documents.
			ln, err := net.Listen("tcp", mf.Listen)
			if err != nil {
				fmt.Fprintln(os.Stderr, "fleet: proxy:", err)
				return
			}
			fmt.Printf("Fleet proxy serving on %s — %d app(s) — Ctrl+C to stop\n", mf.Listen, len(mf.Apps))
			if err := proxySrv.Serve(ln); err != nil && err != http.ErrServerClosed {
				fmt.Fprintln(os.Stderr, "fleet: proxy:", err)
			}
		}()

		<-ctx.Done()
		log.Println("fleet: shutting down — draining proxy, stopping apps")
		shutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		proxySrv.Shutdown(shutCtx)  //nolint:errcheck
		statusSrv.Shutdown(shutCtx) //nolint:errcheck
		sup.Shutdown()
		log.Println("fleet: shut down cleanly")
	},
}

// fleetServeCmd is the MT-STRUCT-S3 Option-B runtime: the SAME fleet.json,
// but N apps compiled IN ONE PROCESS (~1 MB/app vs ~45 MB/process in `fleet
// run`), dispatched by Host through the in-process registry. Per-app JWT
// secret / RBAC / database / caches hold identically (security-reviewed);
// unmatched Hosts get a clean 404, never an arbitrary app.
var fleetServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Sirve N apps EN UN PROCESO (in-process, Opción B) desde el mismo fleet.json",
	Run: func(cmd *cobra.Command, args []string) {
		cfgPath, _ := cmd.Flags().GetString("config")
		mf, err := fleet.LoadManifest(cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		// --listen overrides the manifest (e.g. `make fleet PORT=9000`) without
		// editing the file — the manifest stays the fleet's persistent truth.
		if listen, _ := cmd.Flags().GetString("listen"); listen != "" {
			mf.Listen = listen
		}
		if err := appitools.ServeFleet(mf, version, debugTracesHTML); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}

var fleetStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Muestra el estado de la fleet (consulta el status API del fleet run)",
	Run: func(cmd *cobra.Command, args []string) {
		addr, _ := cmd.Flags().GetString("addr")
		resp, err := http.Get("http://" + addr + "/fleet/status")
		if err != nil {
			fmt.Fprintf(os.Stderr, "fleet: cannot reach %s — is `appitools fleet run` running? (%v)\n", addr, err)
			os.Exit(1)
		}
		defer resp.Body.Close() //nolint:errcheck
		body, _ := io.ReadAll(resp.Body)
		var st struct {
			Apps []fleet.AppStatus `json:"apps"`
		}
		if err := json.Unmarshal(body, &st); err != nil {
			fmt.Println(string(body))
			return
		}
		fmt.Printf("%-12s %-8s %-18s %-7s %-9s %-8s %s\n", "APP", "PID", "HEALTH", "PORT", "RESTARTS", "UPTIME", "DOMAINS")
		for _, a := range st.Apps {
			fmt.Printf("%-12s %-8d %-18s %-7d %-9d %-8s %s\n",
				a.Name, a.PID, a.Health, a.Port, a.Restarts,
				(time.Duration(a.UptimeS) * time.Second).String(), strings.Join(a.Domains, ","))
		}
	},
}

// ── FLEET-LIFECYCLE-S1: add / remove / list ──────────────────────────────────
// The lifecycle commands keep the MANIFEST (persistent source of truth) and
// the LIVE fleet (the in-process registry, when `fleet serve` is running)
// coherent: against a live fleet they call the operator-gated lifecycle API
// (the app goes live/offline HOT, and the server persists the manifest);
// without one they edit the manifest for the next start. `fleet run`
// (multi-process) has no hot lifecycle — the manifest edit applies on restart.

// liveFleetBase probes whether an in-process fleet (`fleet serve`) is running
// on the manifest's listen address. The process-level /health carries a
// "fleet_apps" marker only that runtime serves — a single engine or the
// `fleet run` proxy never matches, so the CLI falls back to manifest-only.
func liveFleetBase(mf *fleet.Manifest) (string, bool) {
	host, port, err := net.SplitHostPort(mf.Listen)
	if err != nil {
		return "", false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	base := "http://" + net.JoinHostPort(host, port)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(base + "/health")
	if err != nil {
		return "", false
	}
	defer resp.Body.Close() //nolint:errcheck
	var h struct {
		FleetApps *int `json:"fleet_apps"`
	}
	if json.NewDecoder(resp.Body).Decode(&h) != nil || h.FleetApps == nil {
		return "", false
	}
	return base, true
}

// fleetAPI calls the live fleet's operator-gated lifecycle API. A 404 means
// the operator key was rejected (or the console is disabled) — surfaced as an
// ERROR, never a silent manifest-only fallback (that would create live↔manifest
// drift).
func fleetAPI(method, url, key string, body any) (int, map[string]any, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Fleet-Key", key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		json.Unmarshal(raw, &out) //nolint:errcheck
	}
	return resp.StatusCode, out, nil
}

func requireOperatorKey(mf *fleet.Manifest) string {
	if mf.OperatorKey == "" {
		fmt.Fprintln(os.Stderr, "fleet: a LIVE fleet is running but no operator key is configured — set operator_key in the manifest (or APPITOOLS_FLEET_OPERATOR_KEY) to manage it hot")
		os.Exit(1)
	}
	return mf.OperatorKey
}

var fleetListCmd = &cobra.Command{
	Use:   "list",
	Short: "Lista las apps del fleet — el estado VIVO si `fleet serve` corre, o el manifiesto",
	Run: func(cmd *cobra.Command, args []string) {
		cfgPath, _ := cmd.Flags().GetString("config")
		mf, err := fleet.LoadManifest(cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if base, live := liveFleetBase(mf); live {
			key := requireOperatorKey(mf)
			code, body, err := fleetAPI(http.MethodGet, base+"/fleet/api/apps", key, nil)
			if err != nil || code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "fleet: live fleet at %s rejected the inventory call (HTTP %d, err %v) — wrong operator key?\n", base, code, err)
				os.Exit(1)
			}
			apps, _ := body["apps"].([]any)
			fmt.Printf("LIVE fleet (%s) — %d app(s):\n", base, len(apps))
			fmt.Printf("%-14s %-34s %-10s %-8s %s\n", "APP", "DOMAINS", "RESOURCES", "TENANTS", "HOT-SWAPS")
			for _, e := range apps {
				a, _ := e.(map[string]any)
				doms, _ := a["domains"].([]any)
				res, _ := a["resources"].([]any)
				tens, _ := a["tenants"].([]any)
				ds := make([]string, 0, len(doms))
				for _, d := range doms {
					ds = append(ds, fmt.Sprint(d))
				}
				fmt.Printf("%-14v %-34s %-10d %-8d %v\n", a["name"], strings.Join(ds, ","), len(res), len(tens), a["hot_swaps"])
			}
			return
		}
		fmt.Printf("MANIFEST %s (fleet not running) — %d app(s):\n", cfgPath, len(mf.Apps))
		fmt.Printf("%-14s %-34s %s\n", "APP", "DOMAINS", "SCHEMA")
		for i := range mf.Apps {
			fmt.Printf("%-14s %-34s %s\n", mf.Apps[i].Name, strings.Join(mf.Apps[i].Domains, ","), mf.Apps[i].Schema)
		}
	},
}

var fleetAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Agrega una app al fleet — EN CALIENTE si `fleet serve` corre (sin reiniciar, otras apps intactas) + persiste el manifiesto",
	Run: func(cmd *cobra.Command, args []string) {
		cfgPath, _ := cmd.Flags().GetString("config")
		name, _ := cmd.Flags().GetString("name")
		schemaPath, _ := cmd.Flags().GetString("schema")
		domainsF, _ := cmd.Flags().GetStringSlice("domain")
		envPairs, _ := cmd.Flags().GetStringSlice("env")
		envFile, _ := cmd.Flags().GetString("env-file")

		mf, err := fleet.LoadManifest(cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		env := map[string]string{}
		for _, kv := range envPairs {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				fmt.Fprintf(os.Stderr, "fleet add: --env %q is not KEY=VALUE\n", kv)
				os.Exit(1)
			}
			env[k] = v
		}

		// Paths are CLI-relative (the operator's cwd), not manifest-relative.
		if schemaPath != "" {
			if schemaPath, err = filepath.Abs(schemaPath); err != nil {
				fmt.Fprintln(os.Stderr, "fleet add:", err)
				os.Exit(1)
			}
		}
		if envFile != "" {
			if envFile, err = filepath.Abs(envFile); err != nil {
				fmt.Fprintln(os.Stderr, "fleet add:", err)
				os.Exit(1)
			}
		}

		// Local fast-fail: the schema through the REAL validator (the same
		// oracle the server re-runs), then the fleet composition rules.
		raw, err := os.ReadFile(schemaPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fleet add: read schema:", err)
			os.Exit(1)
		}
		if rep := schema.ValidateReport(raw); !rep.Valid {
			fmt.Fprintf(os.Stderr, "fleet add: schema %s is INVALID (%d errors):\n", schemaPath, len(rep.Errors))
			for _, e := range rep.Errors {
				fmt.Fprintf(os.Stderr, "  %s [%s] %s\n", e.Path, e.Rule, e.Message)
			}
			os.Exit(1)
		}
		spec := &fleet.AppSpec{Name: name, Schema: schemaPath, Domains: domainsF, Env: env, EnvFile: envFile}
		if err := mf.ValidateNewApp(spec); err != nil {
			fmt.Fprintln(os.Stderr, "fleet add:", err)
			os.Exit(1)
		}

		if base, live := liveFleetBase(mf); live {
			key := requireOperatorKey(mf)
			code, body, err := fleetAPI(http.MethodPost, base+"/fleet/api/apps", key, map[string]any{
				"name": name, "domains": spec.Domains, "schema_path": schemaPath, "env": env, "env_file": envFile,
			})
			if err != nil {
				fmt.Fprintln(os.Stderr, "fleet add: live fleet:", err)
				os.Exit(1)
			}
			if code != http.StatusCreated {
				fmt.Fprintf(os.Stderr, "fleet add: live fleet refused (HTTP %d): %v\n", code, body["error"])
				os.Exit(1)
			}
			fmt.Printf("app %q added HOT to the live fleet — serving %s now (no restart, other apps untouched); manifest persisted\n",
				name, strings.Join(spec.Domains, ", "))
			return
		}

		if err := fleet.AddAppToManifestFile(mf.Path(), spec); err != nil {
			fmt.Fprintln(os.Stderr, "fleet add:", err)
			os.Exit(1)
		}
		fmt.Printf("app %q added to the manifest %s — it will serve on the next fleet start (no live fleet detected)\n", name, cfgPath)
	},
}

var fleetRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Quita una app del fleet — EN CALIENTE si `fleet serve` corre; su base de datos NO se borra",
	Run: func(cmd *cobra.Command, args []string) {
		cfgPath, _ := cmd.Flags().GetString("config")
		name, _ := cmd.Flags().GetString("name")
		yes, _ := cmd.Flags().GetBool("yes")

		mf, err := fleet.LoadManifest(cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		spec := mf.AppByName(name)
		if spec == nil {
			fmt.Fprintf(os.Stderr, "fleet remove: no app named %q in %s\n", name, cfgPath)
			os.Exit(1)
		}
		if !yes {
			fmt.Fprintf(os.Stderr, `fleet remove: this would remove app %q from the fleet:
  - its domains stop serving immediately (%s)
  - the OTHER apps are untouched
  - its DATABASE is NOT deleted (re-adding with the same DATABASE_URL restores it intact)
Re-run with --yes to confirm.
`, name, strings.Join(spec.Domains, ", "))
			os.Exit(1)
		}

		if base, live := liveFleetBase(mf); live {
			key := requireOperatorKey(mf)
			code, body, err := fleetAPI(http.MethodDelete,
				base+"/fleet/api/apps/"+name+"?confirm="+name, key, nil)
			if err != nil {
				fmt.Fprintln(os.Stderr, "fleet remove: live fleet:", err)
				os.Exit(1)
			}
			if code != http.StatusOK {
				fmt.Fprintf(os.Stderr, "fleet remove: live fleet refused (HTTP %d): %v\n", code, body["error"])
				os.Exit(1)
			}
			fmt.Printf("app %q removed HOT from the live fleet — its domains now 404, other apps untouched; manifest persisted. Its database was NOT deleted.\n", name)
			return
		}

		if err := fleet.RemoveAppFromManifestFile(mf.Path(), name); err != nil {
			fmt.Fprintln(os.Stderr, "fleet remove:", err)
			os.Exit(1)
		}
		fmt.Printf("app %q removed from the manifest %s — applies on the next fleet start. Its database was NOT deleted.\n", name, cfgPath)
	},
}

func init() {
	fleetRunCmd.Flags().String("config", "fleet.json", "path to the fleet manifest")
	fleetServeCmd.Flags().String("config", "fleet.json", "path to the fleet manifest")
	fleetServeCmd.Flags().String("listen", "", "override the manifest's listen address (e.g. :9000)")
	fleetStatusCmd.Flags().String("addr", "127.0.0.1:9601", "fleet status API address")
	fleetListCmd.Flags().String("config", "fleet.json", "path to the fleet manifest")
	fleetAddCmd.Flags().String("config", "fleet.json", "path to the fleet manifest")
	fleetAddCmd.Flags().String("name", "", "app name (^[a-z][a-z0-9_-]*$, unique)")
	fleetAddCmd.Flags().String("schema", "", "path to the app's schema JSON (validated before adding)")
	fleetAddCmd.Flags().StringSlice("domain", nil, "domain routed to the app (repeatable)")
	fleetAddCmd.Flags().StringSlice("env", nil, "app env KEY=VALUE (repeatable; DATABASE_URL, JWT_SECRET, ADMIN_KEY required)")
	fleetAddCmd.Flags().String("env-file", "", "KEY=VALUE file with the app's env/secrets")
	fleetAddCmd.MarkFlagRequired("name")   //nolint:errcheck
	fleetAddCmd.MarkFlagRequired("schema") //nolint:errcheck
	fleetAddCmd.MarkFlagRequired("domain") //nolint:errcheck
	fleetRemoveCmd.Flags().String("config", "fleet.json", "path to the fleet manifest")
	fleetRemoveCmd.Flags().String("name", "", "app to remove")
	fleetRemoveCmd.Flags().Bool("yes", false, "confirm the removal (required — the gate)")
	fleetRemoveCmd.MarkFlagRequired("name") //nolint:errcheck
	fleetCmd.AddCommand(fleetRunCmd, fleetServeCmd, fleetStatusCmd, fleetListCmd, fleetAddCmd, fleetRemoveCmd)
	rootCmd.AddCommand(fleetCmd)
}
