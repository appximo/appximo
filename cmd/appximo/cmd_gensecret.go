package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// gen-secret closes field report W2: the config errors used to say `openssl
// rand -hex 32`, which Windows does not ship — the binary itself is the one
// tool guaranteed present on every platform. Output is the hex string alone on
// stdout (newline-terminated), so it composes: JWT_SECRET=$(appximo gen-secret).
var genSecretCmd = &cobra.Command{
	Use:   "gen-secret",
	Short: "Generate a cryptographically random secret (for JWT_SECRET, ADMIN_KEY, …)",
	Long: `Generate a cryptographically random secret, hex-encoded, on stdout.

Defaults to 32 random bytes (64 hex characters) — the right size for
JWT_SECRET. Use --bytes 16 for ADMIN_KEY. Works identically on every platform
(no openssl needed).`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		n, _ := cmd.Flags().GetInt("bytes")
		if n < 16 || n > 1024 {
			fmt.Fprintln(os.Stderr, "gen-secret: --bytes must be between 16 and 1024 (16 is already the floor for a usable secret)")
			os.Exit(2)
		}
		buf := make([]byte, n)
		if _, err := rand.Read(buf); err != nil {
			fmt.Fprintf(os.Stderr, "gen-secret: system randomness unavailable: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(hex.EncodeToString(buf))
	},
}

func init() {
	genSecretCmd.Flags().Int("bytes", 32, "random bytes to generate (hex output is twice this length)")
	rootCmd.AddCommand(genSecretCmd)
}
