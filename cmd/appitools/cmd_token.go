package main

import (
	"fmt"
	"os"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/spf13/cobra"
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Genera un JWT de desarrollo para pruebas con curl",
	Long: `Emite un JWT firmado con HS256 listo para pegar en:
  curl -H "Authorization: Bearer <token>" ...`,
	Run: func(cmd *cobra.Command, args []string) {
		role, _ := cmd.Flags().GetString("role")
		tenantID, _ := cmd.Flags().GetString("tenant")
		secret, _ := cmd.Flags().GetString("secret")
		userID, _ := cmd.Flags().GetString("user-id")

		claims := auth.Claims{
			UserID:   userID,
			Role:     role,
			TenantID: tenantID,
		}
		token, err := auth.GenerateToken(claims, secret)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		fmt.Println(token)
	},
}

func init() {
	tokenCmd.Flags().String("role", "super_admin", "role to embed in the token")
	tokenCmd.Flags().String("tenant", "", "tenant ID to embed in the token")
	tokenCmd.Flags().String("secret", "", "HMAC secret used to sign (required)")
	tokenCmd.Flags().String("user-id", "", "user ID to embed in the token")
	tokenCmd.MarkFlagRequired("secret")
	rootCmd.AddCommand(tokenCmd)
}
