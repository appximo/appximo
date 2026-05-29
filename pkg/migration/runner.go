package migration

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/schema"
)

// ApplyTenantMigration creates (or idempotently verifies) all resource tables
// in pgSchema by executing CREATE TABLE IF NOT EXISTS for each resource.
// Fully qualified table names (pgSchema.resource) are used throughout — no
// search_path mutation required.
func ApplyTenantMigration(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	names := make([]string, 0, len(s.Resources))
	for n := range s.Resources {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, resName := range names {
		res := s.Resources[resName]
		ddl := buildCreateTable(pgSchema, resName, res)
		if _, err := conn.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("create table %s.%s: %w", pgSchema, resName, err)
		}
	}
	return nil
}

// buildCreateTable generates a CREATE TABLE IF NOT EXISTS statement for one resource.
// Column order: id (implicit) → regular fields (sorted) → auto fields (sorted).
func buildCreateTable(pgSchema, resName string, res schema.ResourceSchema) string {
	cols := []string{"id UUID DEFAULT gen_random_uuid() PRIMARY KEY"}

	// Collect and sort regular field names for deterministic output.
	regular := make([]string, 0, len(res.Fields))
	auto := make([]string, 0)
	for name := range res.Fields {
		if name == "id" {
			continue
		}
		if res.Fields[name].Auto {
			auto = append(auto, name)
		} else {
			regular = append(regular, name)
		}
	}
	sort.Strings(regular)
	sort.Strings(auto)

	for _, name := range regular {
		f := res.Fields[name]
		col := fmt.Sprintf("%s %s", name, fieldTypeToPG(f.Type))
		if f.Required {
			col += " NOT NULL"
		}
		if f.Unique {
			col += " UNIQUE"
		}
		cols = append(cols, col)
	}

	for _, name := range auto {
		cols = append(cols, fmt.Sprintf("%s TIMESTAMPTZ DEFAULT now()", name))
	}

	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s.%s (\n  %s\n)",
		pgSchema, resName, strings.Join(cols, ",\n  "),
	)
}

func fieldTypeToPG(t string) string {
	switch t {
	case "string", "text":
		return "TEXT"
	case "int":
		return "INTEGER"
	case "int64":
		return "BIGINT"
	case "float64":
		return "DOUBLE PRECISION"
	case "bool":
		return "BOOLEAN"
	case "uuid":
		return "UUID"
	case "time":
		return "TIMESTAMPTZ"
	default:
		return "TEXT"
	}
}
