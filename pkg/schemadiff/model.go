package schemadiff

import "fmt"

// Schema is the canonical, comparable model of one Postgres schema (namespace).
// Tables and Enums are indexed by name for O(1) lookup — the diff that compares
// two Schemas walks these maps, never a linear list. Two Schemas built by
// Introspect are reflect.DeepEqual iff they describe the same database state.
type Schema struct {
	Name   string
	Tables map[string]*Table    // by table name
	Enums  map[string]*EnumType // by enum type name
}

// NewSchema returns an empty Schema with initialized maps.
func NewSchema(name string) *Schema {
	return &Schema{
		Name:   name,
		Tables: make(map[string]*Table),
		Enums:  make(map[string]*EnumType),
	}
}

// Table is one relation with its columns and constraints. Columns are indexed by
// name (O(1)); ColumnOrder preserves the on-disk column order so DDL can be
// emitted stably. Constraints are split by kind — PK, FKs, Uniques and Checks are
// declarative integrity (each altered with distinct DDL), while Indexes holds the
// STANDALONE indexes only (those NOT backing a PK or unique constraint).
type Table struct {
	Name string

	// RenamedFrom is the EXPLICIT previous name of this table when the desired
	// schema declares a rename. It is never inferred by a heuristic — the diff
	// uses it to emit ALTER TABLE … RENAME instead of drop+create. Empty for an
	// introspected (real) table.
	RenamedFrom string

	Columns     map[string]*Column
	ColumnOrder []string

	PK      *PrimaryKey
	FKs     map[string]*ForeignKey       // by constraint symbol
	Uniques map[string]*UniqueConstraint // by constraint symbol
	Checks  map[string]*Check            // by constraint symbol
	Indexes map[string]*Index            // by index name (standalone indexes only)
}

// NewTable returns an empty Table with initialized maps.
func NewTable(name string) *Table {
	return &Table{
		Name:    name,
		Columns: make(map[string]*Column),
		FKs:     make(map[string]*ForeignKey),
		Uniques: make(map[string]*UniqueConstraint),
		Checks:  make(map[string]*Check),
		Indexes: make(map[string]*Index),
	}
}

// AddColumn registers c, appending to ColumnOrder the first time the name is seen
// so the declared/on-disk order is preserved for deterministic DDL emission.
func (t *Table) AddColumn(c *Column) {
	if t.Columns == nil {
		t.Columns = make(map[string]*Column)
	}
	if _, exists := t.Columns[c.Name]; !exists {
		t.ColumnOrder = append(t.ColumnOrder, c.Name)
	}
	t.Columns[c.Name] = c
}

// Column is one column. Type is the canonical struct (never a string), so type
// comparison is exact and spelling-independent.
type Column struct {
	Name string

	// RenamedFrom is the EXPLICIT previous column name when the desired schema
	// declares a rename (never a heuristic). Empty for an introspected column.
	RenamedFrom string

	Type    Type
	NotNull bool

	// Default is the normalized DEFAULT expression, or nil when the column has
	// none. A serial/identity column carries its generation in Identity, not here
	// (its nextval default is implied), so Default is nil for those.
	Default *Expr

	// Identity is set for an auto-generated value: legacy serial, or an explicit
	// GENERATED { ALWAYS | BY DEFAULT } AS IDENTITY column. nil for a plain column.
	Identity *Identity
}

// Type is the canonical, comparable representation of a column type. It is a
// struct of comparable fields, so `==` and reflect.DeepEqual both work and two
// textual spellings of the same type normalize to the SAME Type — the whole point
// of not modeling types as strings (e.g. "varchar(255)" and
// "character varying(255)" both → {Base: BaseVarchar, Size: 255}).
type Type struct {
	Base  BaseType
	Size  int // length for varchar(N) / char(N); 0 if unspecified
	Prec  int // precision for numeric(P,S); fractional-seconds precision for time/timestamp
	Scale int // scale for numeric(P,S)
	Array bool

	// UserName holds the raw type name when Base == BaseUserDefined (a Postgres
	// enum, domain, or any type outside the alias map). Empty otherwise.
	UserName string
}

// String renders a compact canonical spelling of the type. The spelling round-
// trips: ParseType(t.String()) == t for every Type this package produces.
func (t Type) String() string {
	var s string
	switch t.Base {
	case BaseUserDefined:
		s = t.UserName
	case BaseVarchar, BaseChar:
		if t.Size > 0 {
			s = fmt.Sprintf("%s(%d)", t.Base, t.Size)
		} else {
			s = t.Base.String()
		}
	case BaseNumeric:
		switch {
		case t.Prec > 0 && t.Scale > 0:
			s = fmt.Sprintf("%s(%d,%d)", t.Base, t.Prec, t.Scale)
		case t.Prec > 0:
			s = fmt.Sprintf("%s(%d)", t.Base, t.Prec)
		default:
			s = t.Base.String()
		}
	case BaseTimestamp, BaseTimestamptz, BaseTime, BaseTimetz:
		if t.Prec > 0 {
			s = fmt.Sprintf("%s(%d)", t.Base, t.Prec)
		} else {
			s = t.Base.String()
		}
	default:
		s = t.Base.String()
	}
	if t.Array {
		s += "[]"
	}
	return s
}

// BaseType is the canonical family of a column type (the alias map collapses every
// Postgres spelling — int4/int/integer, varchar/character varying, etc. — onto one
// of these).
type BaseType int

const (
	BaseUnknown BaseType = iota
	BaseSmallint
	BaseInteger
	BaseBigint
	BaseNumeric
	BaseReal   // float4
	BaseDouble // float8
	BaseBool
	BaseText
	BaseVarchar
	BaseChar
	BaseUUID
	BaseTimestamptz
	BaseTimestamp
	BaseDate
	BaseTime
	BaseTimetz
	BaseJSON
	BaseJSONB
	BaseBytea
	BaseInterval
	BaseInet
	BaseUserDefined // enum / domain / anything not in the alias map (see Type.UserName)
)

// baseName maps a BaseType to a compact canonical spelling that ParseType accepts,
// so Type.String() round-trips through ParseType.
var baseName = map[BaseType]string{
	BaseUnknown:     "unknown",
	BaseSmallint:    "smallint",
	BaseInteger:     "integer",
	BaseBigint:      "bigint",
	BaseNumeric:     "numeric",
	BaseReal:        "real",
	BaseDouble:      "double precision",
	BaseBool:        "boolean",
	BaseText:        "text",
	BaseVarchar:     "varchar",
	BaseChar:        "char",
	BaseUUID:        "uuid",
	BaseTimestamptz: "timestamptz",
	BaseTimestamp:   "timestamp",
	BaseDate:        "date",
	BaseTime:        "time",
	BaseTimetz:      "timetz",
	BaseJSON:        "json",
	BaseJSONB:       "jsonb",
	BaseBytea:       "bytea",
	BaseInterval:    "interval",
	BaseInet:        "inet",
	BaseUserDefined: "",
}

func (b BaseType) String() string {
	if s, ok := baseName[b]; ok {
		return s
	}
	return "unknown"
}

// Identity describes how a column's value is auto-generated.
type Identity struct {
	// Generated is one of:
	//   "serial"     — legacy serial / bigserial (integer + an owned sequence
	//                  default). The implied nextval default is NOT stored on the
	//                  column (it is fully captured by this field).
	//   "always"     — GENERATED ALWAYS AS IDENTITY
	//   "by_default" — GENERATED BY DEFAULT AS IDENTITY
	Generated string
}

// Expr is a (default / check) expression captured verbatim from pg_get_expr. The
// text Postgres returns is already canonical and deterministic; deeper semantic
// normalization (e.g. stripping redundant `::type` casts so a desired default
// matches a real one) is a diff-session concern, intentionally not done here.
type Expr struct {
	Raw string
}

// PrimaryKey is the table's primary key (always exactly one, or nil).
type PrimaryKey struct {
	Name    string
	Columns []string // in key order
}

// ForeignKey is one foreign-key constraint, including its referential actions —
// which the current converger never emits (docs/MIGRATION_DIAG.md §2). Columns and
// RefColumns are ordered and may be composite.
type ForeignKey struct {
	Symbol     string
	Columns    []string
	RefTable   string
	RefColumns []string
	OnDelete   RefAction
	OnUpdate   RefAction
}

// UniqueConstraint is a UNIQUE constraint (distinct from a standalone UNIQUE
// INDEX: a constraint is altered via ALTER TABLE ADD/DROP CONSTRAINT and can be
// the target of a foreign key, so the diff must treat the two differently).
type UniqueConstraint struct {
	Symbol  string
	Columns []string
}

// Check is a CHECK constraint. Expression is the predicate text from
// pg_get_constraintdef (minus the leading "CHECK ").
type Check struct {
	Symbol     string
	Expression string
}

// Index is a STANDALONE index — one NOT backing a primary-key or unique
// constraint (those live in PK / Uniques). Columns are in index order; an
// expression key is captured as "(expression)". Predicate is the partial-index
// WHERE text, empty for a total index.
type Index struct {
	Name      string
	Columns   []string
	Unique    bool
	Method    string // access method: btree, gin, …
	Predicate string
}

// EnumType is a Postgres enum type with its labels in sort order. Appitools' own
// converger does not create enums (enum fields are TEXT + app-layer validation),
// but the model captures them so an introspected schema that has them is faithful.
type EnumType struct {
	Name   string
	Values []string
}

// RefAction is a foreign-key referential action (ON DELETE / ON UPDATE).
type RefAction int

const (
	NoAction   RefAction = iota // 'a' — Postgres default
	Restrict                    // 'r'
	Cascade                     // 'c'
	SetNull                     // 'n'
	SetDefault                  // 'd'
)

func (a RefAction) String() string {
	switch a {
	case Restrict:
		return "RESTRICT"
	case Cascade:
		return "CASCADE"
	case SetNull:
		return "SET NULL"
	case SetDefault:
		return "SET DEFAULT"
	default:
		return "NO ACTION"
	}
}
