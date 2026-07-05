package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/miguelangel/appitools/pkg/fleet"
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
			log.Printf("fleet: status/control API on %s (internal — GET /fleet/status)", mf.StatusAddr)
			if err := statusSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
			fmt.Printf("Fleet proxy serving on %s — %d app(s) — Ctrl+C to stop\n", mf.Listen, len(mf.Apps))
			if err := proxySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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

func init() {
	fleetRunCmd.Flags().String("config", "fleet.json", "path to the fleet manifest")
	fleetStatusCmd.Flags().String("addr", "127.0.0.1:9601", "fleet status API address")
	fleetCmd.AddCommand(fleetRunCmd, fleetStatusCmd)
	rootCmd.AddCommand(fleetCmd)
}
