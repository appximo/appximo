package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var blueprintsCmd = &cobra.Command{
	Use:   "blueprints",
	Short: "Gestiona blueprints de proyectos Appximo",
}

var blueprintsListCmd = &cobra.Command{
	Use:   "list",
	Short: "Lista los blueprints disponibles en blueprints/",
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := os.ReadDir("blueprints")
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No blueprints/ directory found in current path.")
				return nil
			}
			return fmt.Errorf("leer blueprints/: %w", err)
		}

		found := false
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			found = true
			name := strings.TrimSuffix(e.Name(), ".json")
			apiName := blueprintAPIName(filepath.Join("blueprints", e.Name()))
			if apiName != "" {
				fmt.Printf("  %-20s  %s\n", name, apiName)
			} else {
				fmt.Printf("  %s\n", name)
			}
		}
		if !found {
			fmt.Println("No blueprints found in blueprints/.")
		}
		return nil
	},
}

// blueprintAPIName reads the "name" field from a blueprint JSON file.
func blueprintAPIName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var meta struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.Name
}

func init() {
	blueprintsCmd.AddCommand(blueprintsListCmd)
	rootCmd.AddCommand(blueprintsCmd)
}
