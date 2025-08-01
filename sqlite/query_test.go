package sqlite_test

import (
	"testing"

	"github.com/niklasfasching/x/snap"
	. "github.com/niklasfasching/x/sqlite"
)

type Doc struct {
	ID    int
	Title string
	Tags  []int
}

var docsSQL = `
  CREATE TABLE docs (id, title, tags JSON);
  INSERT INTO docs VALUES (1, 'doc one', '[1, 2]');
  INSERT INTO docs VALUES (2, 'doc two', '[2, 3]');
`

// func TestQuery(t *testing.T) {
// 	db := simpleDB(t, docsSQL)
// 	t.Run("query generic map", func(t *testing.T) {
// 		anyMaps, err := Query[Map[any]](db, "SELECT * FROM docs")
// 		if err != nil {
// 			t.Fatal(err)
// 		}
// 		strMaps, err := Query[Map[string]](db, "SELECT title FROM docs")
// 		if err != nil {
// 			t.Fatal(err)
// 		}
// 		snap.Snap(t, snap.JSON{}, map[string]any{
// 			"any": anyMaps,
// 			"str": strMaps,
// 		})
// 	})
// 	t.Run("query type", func(t *testing.T) {
// 		docs, err := Query[Doc](db, "SELECT * FROM docs")
// 		if err != nil {
// 			t.Fatal(err)
// 		}
// 		snap.Snap(t, snap.JSON{}, docs)
// 	})
// }

func TestExec(t *testing.T) {
	t.Run("query type", func(t *testing.T) {
	})
}

func TestTable(t *testing.T) {
	db := simpleDB(t, docsSQL)
	tb, err := NewTable[Map[any]](db, "docs")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := tb.Query("SELECT * FROM docs")
	if err != nil {
		t.Fatal(err)
	}
	snap.Snap(t, snap.JSON{}, docs)
}

func simpleDB(t *testing.T, schema ...string) *DB {
	db, err := New(":memory:", schema, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func exec(t *testing.T, db *DB, q string, args ...any) {
	_, err := db.Exec(q, args...)
	if err != nil {
		t.Fatalf("failed to execute query %q: %v", q, err)
	}
}

func query[T Type](t *testing.T, db *DB, q string, args ...any) []T {
	vs, err := Query[T](db, q, args...)
	if err != nil {
		t.Fatalf("failed to execute query %q: %v", q, err)
	}
	return vs
}
