package sqlite

import (
	"context"
	"testing"
)

func TestScanStringsCollectsRowsInQueryOrder(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	rows, err := database.QueryContext(context.Background(),
		"SELECT value FROM (SELECT 'b' AS value UNION SELECT 'a' UNION SELECT 'c') ORDER BY value")
	if err != nil {
		t.Fatal(err)
	}
	values, err := ScanStrings(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 || values[0] != "a" || values[1] != "b" || values[2] != "c" {
		t.Fatalf("values = %v", values)
	}
}

func TestScanStringsReturnsNilForEmptyResult(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	rows, err := database.QueryContext(context.Background(), "SELECT 'unreachable' WHERE 1 = 0")
	if err != nil {
		t.Fatal(err)
	}
	values, err := ScanStrings(rows)
	if err != nil {
		t.Fatal(err)
	}
	if values != nil {
		t.Fatalf("values = %v", values)
	}
}

// A scan error must surface with its identity intact and must still close rows,
// because callers rely on ScanStrings owning the row lifetime on every path.
func TestScanStringsClosesRowsAndKeepsScanErrorIdentity(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	rows, err := database.QueryContext(context.Background(), "SELECT 'a' AS one, 'b' AS two")
	if err != nil {
		t.Fatal(err)
	}
	values, err := ScanStrings(rows)
	if err == nil {
		t.Fatal("column count mismatch accepted")
	}
	if values != nil {
		t.Fatalf("values = %v", values)
	}
	if _, err := rows.Columns(); err == nil {
		t.Fatal("rows were left open")
	}
}
