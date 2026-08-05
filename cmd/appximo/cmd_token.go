package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/appximo/appximo"
	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/spf13/cobra"
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Generate a development JWT for curl testing",
	Long: `Mints an HS256-signed JWT ready to paste into:
  curl -H "Authorization: Bearer <token>" ...

With --schema, the role is validated against the roles that schema declares and a
nonexistent role is REFUSED (a token carrying a role no schema role declares gets
the same 403 "forbidden" as a role without permission — deny-by-default — and the
why appears only in the server log, ENG-27). Without --schema nothing is
validated: the default "super_admin" is just a convention and may not exist in
your schema.`,
	Run: func(cmd *cobra.Command, args []string) {
		role, _ := cmd.Flags().GetString("role")
		tenantID, _ := cmd.Flags().GetString("tenant")
		secret, _ := cmd.Flags().GetString("secret")
		userID, _ := cmd.Flags().GetString("user-id")
		schemaPath, _ := cmd.Flags().GetString("schema")

		// ENG-27: a token minted with a role no schema role declares is
		// indistinguishable from a denied one at the API (deliberately — see
		// rbac.Policy.DenyDetail). The place to catch the typo is HERE, where the
		// operator can be told the truth without building an enumeration oracle.
		if schemaPath != "" {
			s, err := schema.LoadFromFile(schemaPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: cannot load schema %s: %v\n", schemaPath, err)
				os.Exit(1)
			}
			if _, declared := s.RBAC.Roles[role]; !declared {
				declaredRoles := make([]string, 0, len(s.RBAC.Roles))
				for r := range s.RBAC.Roles {
					declaredRoles = append(declaredRoles, r)
				}
				sort.Strings(declaredRoles)
				fmt.Fprintf(os.Stderr,
					"Error: role %q is not declared by %s (declared roles: %s)\n"+
						"A token with an undeclared role is denied everything (deny-by-default),\n"+
						"and the API's 403 will not tell you why — only the server log does.\n",
					role, schemaPath, strings.Join(declaredRoles, ", "))
				os.Exit(1)
			}
		}

		// SEC-6: the engine refuses to BOOT with a secret under the floor, so a
		// token minted with one can never be validated by a running engine. The
		// mint still happens (a dev tool stays a dev tool) but says so loudly.
		if len(secret) < appximo.MinJWTSecretLen {
			fmt.Fprintf(os.Stderr,
				"WARNING: secret is %d characters — the engine refuses to boot below %d (SEC-6),\n"+
					"so no running engine will accept this token. Use the same ≥%d-char JWT_SECRET the engine boots with.\n",
				len(secret), appximo.MinJWTSecretLen, appximo.MinJWTSecretLen)
		}

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
	tokenCmd.Flags().String("schema", "", "schema file to validate --role against (refuses an undeclared role)")
	tokenCmd.MarkFlagRequired("secret")
	rootCmd.AddCommand(tokenCmd)
}
