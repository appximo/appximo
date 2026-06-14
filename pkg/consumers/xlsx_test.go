package consumers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// TestProcessXLSXReader_Valid exercises the streaming reader entry point (the form
// the VFS uses: VFS.Get returns a reader). Same aggregate, fed from an io.Reader.
func TestProcessXLSXReader_Valid(t *testing.T) {
	path := writeXLSX(t, [][]any{
		{"tipo", "monto"},
		{"ingreso", 100},
		{"egreso", 25},
	})
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	res, err := ProcessXLSXReader(f)
	if err != nil {
		t.Fatalf("ProcessXLSXReader: %v", err)
	}
	if res.Rows != 2 || res.Total != 125 || res.ByType["ingreso"] != 100 || res.ByType["egreso"] != 25 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// TestProcessXLSXReader_Corrupt confirms a bad stream errors (worker → failed) and
// never panics.
func TestProcessXLSXReader_Corrupt(t *testing.T) {
	if _, err := ProcessXLSXReader(strings.NewReader("not an xlsx")); err == nil {
		t.Fatal("expected error from corrupt reader")
	}
}

// writeXLSX writes rows (row 0 = header) to a temp .xlsx and returns its path.
func writeXLSX(t *testing.T, rows [][]any) string {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close() //nolint:errcheck
	sheet := f.GetSheetName(0)
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			t.Fatalf("set row %d: %v", i, err)
		}
	}
	path := filepath.Join(t.TempDir(), "test.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save xlsx: %v", err)
	}
	return path
}

func TestProcessXLSX_Valid(t *testing.T) {
	path := writeXLSX(t, [][]any{
		{"tipo", "monto"},
		{"ingreso", 100.5},
		{"egreso", 50},
		{"ingreso", 200},
	})
	res, err := ProcessXLSX(path)
	if err != nil {
		t.Fatalf("ProcessXLSX: %v", err)
	}
	if res.Rows != 3 {
		t.Fatalf("rows = %d, want 3", res.Rows)
	}
	if res.Total != 350.5 {
		t.Fatalf("total = %v, want 350.5", res.Total)
	}
	if res.ByType["ingreso"] != 300.5 || res.ByType["egreso"] != 50 {
		t.Fatalf("by_type = %v, want ingreso=300.5 egreso=50", res.ByType)
	}
}

func TestProcessXLSX_ColumnOrderIndependent(t *testing.T) {
	// monto before tipo — located by header name, not position.
	path := writeXLSX(t, [][]any{
		{"monto", "tipo"},
		{10, "a"},
		{20, "b"},
	})
	res, err := ProcessXLSX(path)
	if err != nil {
		t.Fatalf("ProcessXLSX: %v", err)
	}
	if res.Total != 30 || res.ByType["a"] != 10 || res.ByType["b"] != 20 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestProcessXLSX_MissingColumn(t *testing.T) {
	path := writeXLSX(t, [][]any{
		{"tipo"}, // no 'monto'
		{"ingreso"},
	})
	if _, err := ProcessXLSX(path); err == nil {
		t.Fatal("expected error for missing 'monto' column")
	}
}

func TestProcessXLSX_NonNumeric(t *testing.T) {
	path := writeXLSX(t, [][]any{
		{"tipo", "monto"},
		{"ingreso", "not-a-number"},
	})
	if _, err := ProcessXLSX(path); err == nil {
		t.Fatal("expected error for non-numeric 'monto'")
	}
}

func TestProcessXLSX_HeaderOnly(t *testing.T) {
	path := writeXLSX(t, [][]any{{"tipo", "monto"}})
	if _, err := ProcessXLSX(path); err == nil {
		t.Fatal("expected error for header with no data rows")
	}
}

func TestProcessXLSX_Corrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.xlsx")
	if err := os.WriteFile(path, []byte("this is not a zip/xlsx file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ProcessXLSX(path); err == nil {
		t.Fatal("expected error opening a corrupt xlsx (worker must not crash)")
	}
}

func TestProcessXLSX_Missing(t *testing.T) {
	if _, err := ProcessXLSX(filepath.Join(t.TempDir(), "does-not-exist.xlsx")); err == nil {
		t.Fatal("expected error for a missing file")
	}
}

// TestProcessXLSX_Large10k streams 10k data rows through the iterator and checks
// the aggregate — exercising the large-file path without GetRows.
func TestProcessXLSX_Large10k(t *testing.T) {
	rows := make([][]any, 0, 10001)
	rows = append(rows, []any{"tipo", "monto"})
	var want float64
	for i := 0; i < 10000; i++ {
		tipo := "even"
		if i%2 == 1 {
			tipo = "odd"
		}
		rows = append(rows, []any{tipo, 1})
		want += 1
	}
	path := writeXLSX(t, rows)

	res, err := ProcessXLSX(path)
	if err != nil {
		t.Fatalf("ProcessXLSX(10k): %v", err)
	}
	if res.Rows != 10000 || res.Total != want {
		t.Fatalf("rows=%d total=%v, want 10000/%v", res.Rows, res.Total, want)
	}
	if res.ByType["even"] != 5000 || res.ByType["odd"] != 5000 {
		t.Fatalf("by_type = %v, want even=5000 odd=5000", res.ByType)
	}
	_ = fmt.Sprint(res)
}
