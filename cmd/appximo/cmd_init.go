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
	Use:   "init [name]",
	Short: "Initialize a new Appximo project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		blueprint, _ := cmd.Flags().GetString("blueprint")

		schemaBytes, err := resolveSchema(blueprint)
		if err != nil {
			return err
		}

		if err := os.MkdirAll(name, 0755); err != nil {
			return fmt.Errorf("create directory %q: %w", name, err)
		}

		schemaPath := filepath.Join(name, "schema.json")
		if err := os.WriteFile(schemaPath, schemaBytes, 0644); err != nil {
			return fmt.Errorf("write schema.json: %w", err)
		}

		goMod := fmt.Sprintf("module %s\n\ngo 1.22\n", name)
		if err := os.WriteFile(filepath.Join(name, "go.mod"), []byte(goMod), 0644); err != nil {
			return fmt.Errorf("write go.mod: %w", err)
		}

		if blueprint != "" {
			fmt.Printf("Project %q initialized with blueprint %q.\n", name, blueprint)
		} else {
			fmt.Printf("Project %q initialized.\n", name)
		}
		fmt.Println("  → Edit schema.json and run:")
		fmt.Println("  → appximo validate schema.json")
		fmt.Println("  → appximo generate schema.json")
		return nil
	},
}

// resolveSchema returns the bytes to write as schema.json: blueprint file if a
// name is given, otherwise the embedded starter template.
func resolveSchema(blueprint string) ([]byte, error) {
	if blueprint == "" {
		return starterSchema, nil
	}
	path := filepath.Join("blueprints", blueprint+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("blueprint %q not found at %s\n  Run 'appximo blueprints list' to see available blueprints", blueprint, path)
		}
		return nil, fmt.Errorf("read blueprint %q: %w", blueprint, err)
	}
	return data, nil
}

func init() {
	initCmd.Flags().String("blueprint", "", "blueprint to use (e.g. fintech, ecommerce, crm)")
	rootCmd.AddCommand(initCmd)
}
