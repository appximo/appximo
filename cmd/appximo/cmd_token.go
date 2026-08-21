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
			// FRESH-AGENT-GAPS-S1: with the schema in hand we can be SPECIFIC —
			// if the chosen role row-scopes by $user_id and no --user-id was
			// given, every list/get for those resources returns empty, with no
			// error anywhere. Name the resources, not just the possibility.
			if rp, declared := s.RBAC.Roles[role]; declared && userID == "" {
				var scoped []string
				if rp.Conditions != nil && rp.Conditions.Val == "$user_id" {
					scoped = append(scoped, "(all of this role's resources)")
				}
				for resName, perm := range rp.Permissions {
					if perm.Conditions != nil && perm.Conditions.Val == "$user_id" {
						scoped = append(scoped, resName)
					}
				}
				if len(scoped) > 0 {
					sort.Strings(scoped)
					fmt.Fprintf(os.Stderr,
						"WARNING: role %q row-scopes %s by $user_id and this token has NO user id —\n"+
							"those reads will match ZERO rows and writes will be attributed to nobody, with no\n"+
							"error at any layer. Mint with --user-id <uuid> (e.g. a real auth user's id).\n",
						role, strings.Join(scoped, ", "))
				}
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

		// FRESH-AGENT-GAPS-S1 (second field appearance — first recorded in the
		// commerce field report, GAPS.md 1B-3, and never surfaced here): a token
		// without --user-id carries an EMPTY identity. Every role whose row
		// condition compares against $user_id matches ZERO rows, an
		// optionally-authenticated route reads the caller as a guest, and a
		// custom handler's Claims().UserID is "". Say it at mint time, where the
		// operator can act — not at request time, where it looks like RBAC.
		if userID == "" {
			fmt.Fprintln(os.Stderr,
				"note: no --user-id — this token has an EMPTY user identity. Roles with a $user_id row\n"+
					"condition will match no rows, optional-auth routes will treat the caller as a guest,\n"+
					"and custom handlers see Claims().UserID == \"\". Add --user-id <uuid> if any of those apply.")
		}
		// FRESH-AGENT-GAPS-S1: a token with an EMPTY tenant is a cross-tenant
		// WILDCARD on the data plane — the tenant-match check is skipped for an
		// empty claim (see OPS-30), so it works against EVERY tenant, not one.
		// That is almost never what a dev token is for. Warn loudly; the fix is
		// one flag.
		if tenantID == "" {
			fmt.Fprintln(os.Stderr,
				"WARNING: no --tenant — this token has an EMPTY tenant and the data plane's tenant-match\n"+
					"check is SKIPPED for it, so it authenticates against ANY tenant (a cross-tenant wildcard).\n"+
					"Pass --tenant <id> to scope it to one tenant, which is almost certainly what you want.")
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
