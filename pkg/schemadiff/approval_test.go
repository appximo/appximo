package schemadiff

import "testing"

func TestDestructiveKey(t *testing.T) {
	tests := []struct {
		name        string
		op          Operation
		wantKey     string
		wantDestroy bool
	}{
		{
			name:        "drop table → table name",
			op:          DropTable{Table: &Table{Name: "proyectos"}},
			wantKey:     "proyectos",
			wantDestroy: true,
		},
		{
			name:        "drop column → table.column",
			op:          DropColumn{Table: "empleados", Column: &Column{Name: "telefono"}},
			wantKey:     "empleados.telefono",
			wantDestroy: true,
		},
		// Safe drops lose no data → never approvable, no key.
		{name: "drop index is not destructive", op: DropIndex{Table: "t", Index: &Index{Name: "i"}}},
		{name: "drop unique is not destructive", op: DropUnique{Table: "t", Unique: &UniqueConstraint{Symbol: "u"}}},
		{name: "drop fk is not destructive", op: DropForeignKey{Table: "t", FK: &ForeignKey{Symbol: "f"}}},
		{name: "drop check is not destructive", op: DropCheck{Table: "t", Check: &Check{Symbol: "c"}}},
		{name: "drop pk is not destructive", op: DropPrimaryKey{Table: "t", PK: &PrimaryKey{Name: "p"}}},
		// Non-drops are never destructive.
		{name: "create table is not destructive", op: CreateTable{Table: &Table{Name: "t"}}},
		{name: "add column is not destructive", op: AddColumn{Table: "t", Column: &Column{Name: "c"}}},
		{name: "rename table is not destructive", op: RenameTable{From: "a", To: "b"}},
		{name: "rename column is not destructive", op: RenameColumn{Table: "t", From: "a", To: "b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, destroy := DestructiveKey(tc.op)
			if destroy != tc.wantDestroy {
				t.Errorf("destructive = %v, want %v", destroy, tc.wantDestroy)
			}
			if key != tc.wantKey {
				t.Errorf("key = %q, want %q", key, tc.wantKey)
			}
			if got := IsDestructive(tc.op); got != tc.wantDestroy {
				t.Errorf("IsDestructive = %v, want %v", got, tc.wantDestroy)
			}
		})
	}
}

// TestDestructiveKey_NoCollision asserts a table key never collides with a column
// key (the property that makes enumeration unambiguous): a table named "empleados"
// keys to "empleados", while a column keys to "empleados.<col>".
func TestDestructiveKey_NoCollision(t *testing.T) {
	tableKey, _ := DestructiveKey(DropTable{Table: &Table{Name: "empleados"}})
	colKey, _ := DestructiveKey(DropColumn{Table: "empleados", Column: &Column{Name: "telefono"}})
	if tableKey == colKey {
		t.Fatalf("table key %q collides with column key %q", tableKey, colKey)
	}
	if tableKey != "empleados" || colKey != "empleados.telefono" {
		t.Fatalf("unexpected keys: table=%q column=%q", tableKey, colKey)
	}
}
