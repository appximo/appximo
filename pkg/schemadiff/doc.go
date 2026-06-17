// Package schemadiff is the foundation of a real schema-migration engine for
// Appitools — the eventual replacement for the idempotent table "converger" in
// pkg/migration (which only ever runs CREATE TABLE / ADD COLUMN IF NOT EXISTS and
// therefore loses data on rename, ignores NOT NULL, no-ops a type change, and
// emits no foreign keys — see docs/MIGRATION_DIAG.md).
//
// # Isolation (important)
//
// This package is built ALONGSIDE the engine and is deliberately NOT imported by
// it. The running engine and its converger are untouched; integration (swapping
// the converger for a diff-driven planner) is a much later session, once the diff
// engine is complete and proven. The only dependency this package takes is pgx
// (a database library, not engine code), so it stays a self-contained library
// the engine can adopt later without a circular dependency.
//
// # What lives here (this session)
//
//   - A canonical, COMPARABLE model of a Postgres schema (model.go): typed structs
//     indexed by name (map[string]) for O(1) lookup — never slices with linear
//     scans. Column types are a canonical struct (Type), never a string, so two
//     textual spellings of the same type ("varchar(255)" and
//     "character varying(255)") compare equal. Foreign keys carry their ON
//     DELETE / ON UPDATE actions (the converger has none today). Rename intent is
//     EXPLICIT (RenamedFrom), never a heuristic.
//   - The type alias map + ParseType (parsetype.go): normalizes any Postgres type
//     spelling — and the Appitools schema-JSON type vocabulary — into the canonical
//     Type.
//   - The pg_catalog introspector (introspect.go): reads the REAL state of a
//     Postgres schema into the canonical model in a fixed, small number of queries
//     (NOT information_schema, NOT one-query-per-table).
//
// # What comes next (later sessions)
//
// The diff itself — comparing a desired canonical schema against the introspected
// real one to produce a typed plan of operations — then topological ordering of
// those operations (FK cycle breaking), safe DDL rendering, and finally the
// integration that replaces the converger. The foundational property the diff
// will rely on, prepared here, is determinism: Introspect is repeatable, so
// diff(Introspect(x), Introspect(x)) is empty.
package schemadiff
