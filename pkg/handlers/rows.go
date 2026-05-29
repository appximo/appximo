package handlers

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// RowsToMaps converts pgx.Rows to a slice of string-keyed maps.
// UUID columns ([16]byte) are converted to hyphenated UUID strings.
func RowsToMaps(rows pgx.Rows) ([]map[string]any, error) {
	descs := rows.FieldDescriptions()
	result := make([]map[string]any, 0)
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(descs))
		for i, desc := range descs {
			row[string(desc.Name)] = normalizeValue(vals[i])
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// BuildInsertArgs builds the column list, placeholder list ($1,$2…), and args slice
// from a JSON-decoded body map. Keys are sorted for deterministic output.
func BuildInsertArgs(body map[string]any) (cols, placeholders string, args []any) {
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	colList := make([]string, len(keys))
	phList := make([]string, len(keys))
	args = make([]any, len(keys))
	for i, k := range keys {
		colList[i] = k
		phList[i] = fmt.Sprintf("$%d", i+1)
		args[i] = body[k]
	}
	return strings.Join(colList, ", "), strings.Join(phList, ", "), args
}

// FilterFields returns a copy of record containing only the keys in allowed.
// If allowed is empty, record is returned unchanged.
func FilterFields(record map[string]any, allowed []string) map[string]any {
	if len(allowed) == 0 {
		return record
	}
	out := make(map[string]any, len(allowed))
	for _, f := range allowed {
		if v, ok := record[f]; ok {
			out[f] = v
		}
	}
	return out
}

// normalizeValue converts pgx-specific types to JSON-friendly Go types.
func normalizeValue(v any) any {
	switch val := v.(type) {
	case [16]byte: // UUID
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
			val[0:4], val[4:6], val[6:8], val[8:10], val[10:16])
	default:
		return val
	}
}
