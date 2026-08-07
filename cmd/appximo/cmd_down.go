package main

// appximo down — the explicit counterpart of `up`'s Docker step (ENG-38): stop
// and remove the Postgres container `up` started. The DATA VOLUME survives by
// default (a re-run of `up` restores the same database); destroying it is a
// separate, named consent (--destroy-data). The server itself is not down's
// business — `up` runs it in the foreground, Ctrl+C stops it.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop and remove the Docker Postgres that `appximo up` started",
	Long: `Stops and removes the Postgres container appximo up started (default name
appximo-pg). The data volume is KEPT unless you pass --destroy-data — so
` + "`up` → `down` → `up`" + ` round-trips with your data intact.

It never touches ./.env or ./schema.json (your files), and never touches
containers it did not create (the com.appximo.up label is checked).`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		name, _ := cmd.Flags().GetString("pg-container")
		destroy, _ := cmd.Flags().GetBool("destroy-data")
		if err := runDown(name, destroy); err != nil {
			fmt.Fprintln(os.Stderr, "appximo down:", err)
			os.Exit(1)
		}
	},
}

func init() {
	downCmd.Flags().String("pg-container", "appximo-pg", "container name (same as `up`'s --pg-container)")
	downCmd.Flags().Bool("destroy-data", false, "ALSO remove the data volume (irreversible)")
	rootCmd.AddCommand(downCmd)
}

func runDown(name string, destroy bool) error {
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Println("nothing to do: `docker` is not on the PATH, so `up` cannot have started a container here")
		return nil
	}
	volume := name + "-data"
	out, err := exec.Command("docker", "inspect", "-f", `{{index .Config.Labels "com.appximo.up"}}`, name).Output()
	switch {
	case err != nil:
		fmt.Printf("nothing to stop: no container named %q\n", name)
	case strings.TrimSpace(string(out)) != "1":
		return fmt.Errorf("container %q exists but was NOT created by `appximo up` (missing the com.appximo.up label) — refusing to touch it.\n"+
			"  If it is yours and you want it gone: docker rm -f %s", name, name)
	default:
		if out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput(); err != nil {
			return fmt.Errorf("docker rm -f %s: %s", name, firstLine(string(out)))
		}
		fmt.Printf("✓ stopped and removed container %q\n", name)
	}

	if destroy {
		if err := exec.Command("docker", "volume", "inspect", volume).Run(); err != nil {
			fmt.Printf("no data volume %q to remove\n", volume)
		} else if out, err := exec.Command("docker", "volume", "rm", volume).CombinedOutput(); err != nil {
			return fmt.Errorf("docker volume rm %s: %s", volume, firstLine(string(out)))
		} else {
			fmt.Printf("✓ removed data volume %q (the database is gone)\n", volume)
		}
	} else if exec.Command("docker", "volume", "inspect", volume).Run() == nil {
		fmt.Printf("data volume %q KEPT — `appximo up` will reuse it; destroy it with: appximo down --destroy-data\n", volume)
	}
	fmt.Println("./.env and ./schema.json are untouched (your files)")
	return nil
}
