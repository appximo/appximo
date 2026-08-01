package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/spf13/cobra"
)

var tokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Genera un JWT de desarrollo para pruebas con curl",
	Long: `Emite un JWT firmado con HS256 listo para pegar en:
  curl -H "Authorization: Bearer <token>" ...

Con --schema, el rol se valida contra los roles que ese schema declara y un rol
inexistente se RECHAZA (un token con un rol que ningún rol del schema declara
recibe el mismo 403 "forbidden" que un rol sin permiso — deny-by-default — y el
porqué solo aparece en el log del servidor, ENG-27). Sin --schema no se valida
nada: el default "super_admin" es solo una convención y puede no existir en tu
schema.`,
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
