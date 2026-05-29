package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

//go:embed templates/schema.json
var starterSchema []byte

var initCmd = &cobra.Command{
	Use:   "init [nombre]",
	Short: "Inicializa un nuevo proyecto Appitools",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		if err := os.MkdirAll(name, 0755); err != nil {
			return fmt.Errorf("crear directorio %q: %w", name, err)
		}

		schemaPath := filepath.Join(name, "schema.json")
		if err := os.WriteFile(schemaPath, starterSchema, 0644); err != nil {
			return fmt.Errorf("escribir schema.json: %w", err)
		}

		goMod := fmt.Sprintf("module %s\n\ngo 1.22\n", name)
		if err := os.WriteFile(filepath.Join(name, "go.mod"), []byte(goMod), 0644); err != nil {
			return fmt.Errorf("escribir go.mod: %w", err)
		}

		fmt.Printf("Proyecto %q inicializado.\n", name)
		fmt.Println("  → Edita schema.json y corre:")
		fmt.Println("  → appitools validate schema.json")
		fmt.Println("  → appitools generate schema.json")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
