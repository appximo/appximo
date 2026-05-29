package migration

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/schema"
)

// ApplyTenantMigration creates or evolves all resource tables in pgSchema.
// For each resource it:
//  1. CREATE TABLE IF NOT EXISTS  — handles new resources
//  2. ALTER TABLE ADD COLUMN IF NOT EXISTS — handles new fields in existing tables
//
// Fully qualified identifiers are used throughout; no search_path mutation required.
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
		if _, err := conn.Exec(ctx, buildCreateTable(pgSchema, resName, res)); err != nil {
			return fmt.Errorf("create table %s.%s: %w", pgSchema, resName, err)
		}
		if err := addMissingColumns(ctx, conn, pgSchema, resName, res); err != nil {
			return fmt.Errorf("evolve table %s.%s: %w", pgSchema, resName, err)
		}
	}
	return nil
}

// addMissingColumns issues ALTER TABLE … ADD COLUMN IF NOT EXISTS for every
// schema field that is absent from the live table. New columns are always
// nullable — adding NOT NULL to an existing table with rows requires a DEFAULT.
func addMissingColumns(ctx context.Context, conn *pgxpool.Conn, pgSchema, resName string, res schema.ResourceSchema) error {
	rows, err := conn.Query(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = $2`,
		pgSchema, resName,
	)
	if err != nil {
		return fmt.Errorf("query columns: %w", err)
	}
	existing := map[string]bool{}
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			rows.Close()
			return err
		}
		existing[col] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	keys := make([]string, 0, len(res.Fields))
	for name, f := range res.Fields {
		if name != "id" && !f.Auto {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)

	for _, name := range keys {
		if existing[name] {
			continue
		}
		alter := fmt.Sprintf(
			`ALTER TABLE "%s"."%s" ADD COLUMN IF NOT EXISTS "%s" %s`,
			pgSchema, resName, name, fieldTypeToPG(res.Fields[name].Type),
		)
		if _, err := conn.Exec(ctx, alter); err != nil {
			return fmt.Errorf("add column %q: %w", name, err)
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

	// Quote both identifiers: schema or table names may contain hyphens.
	return fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS "%s"."%s" (`+"\n  %s\n)",
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
