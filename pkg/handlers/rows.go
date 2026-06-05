package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
)

var bufPool = sync.Pool{
	New: func() any {
		return bytes.NewBuffer(make([]byte, 0, 4096))
	},
}

// WriteJSON encodes v to JSON and writes it to w using a pooled buffer,
// avoiding per-request buffer allocations for the JSON encoder.
// Callers must set Content-Type and status code before calling.
func WriteJSON(w http.ResponseWriter, v any) error {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufPool.Put(buf)
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

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
// from a JSON-decoded body map. Keys are sorted for deterministic output and each
// column identifier is quoted with pgx.Identifier.Sanitize so a key can never break
// out of the identifier position (defence-in-depth alongside ValidateWritableColumns).
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
		colList[i] = pgx.Identifier{k}.Sanitize()
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
