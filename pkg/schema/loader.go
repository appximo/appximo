package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

	// Unknown keys are typos, not extensions: json.Unmarshal drops them
	// silently, so a misspelled "hooks" would become a quietly dead feature.
	// Reject at load with the valid keys listed (CheckUnknownKeys).
	if errs := CheckUnknownKeys(data); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return nil, fmt.Errorf("schema has unknown keys:\n  %s", strings.Join(msgs, "\n  "))
	}

	if s.Schema == "" {
		return nil, fmt.Errorf("missing required field \"$schema\"")
	}
	if s.Version == "" {
		return nil, fmt.Errorf("missing required field \"version\"")
	}

	return &s, nil
}
