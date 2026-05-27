package schema

import (
	"encoding/json"
	"fmt"
	"os"
)

func LoadFromFile(path string) (*APISchema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read schema file: %w", err)
	}

	var s APISchema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse schema JSON: %w", err)
	}

	if s.Schema == "" {
		return nil, fmt.Errorf("missing required field \"$schema\"")
	}
	if s.Version == "" {
		return nil, fmt.Errorf("missing required field \"version\"")
	}

	return &s, nil
}
