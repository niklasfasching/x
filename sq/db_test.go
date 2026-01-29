package sq

import (
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestSchemaJSON(t *testing.T) {
	type V struct {
		ID                   int
		Name                 string
		CreatedAt, UpdatedAt time.Time `sq:"AUTO"`
	}
	t.Run("Query and Insert JSON values (map/struct)", func(t *testing.T) {
		t.Skipf("TODO")
	})
}

func TestSchemaAutoTimestamp(t *testing.T) {
	type V struct {
		ID                   int
		Name                 string
		CreatedAt, UpdatedAt time.Time `sq:"AUTO"`
	}
	db := newDB(t, V{})
	t.Run("CreatedAt/UpdatedAt not part of SET", func(t *testing.T) {
		_, _, kvs := RowMap(V{Name: "Bob"})
		v1 := insert[V](t, db, "vs", "id", kvs)
		time.Sleep(time.Second)
		v2 := update[V](t, db, "vs", "id", v1.ID, map[string]any{"Name": "Alice"})
		if d := v2.UpdatedAt.Sub(v1.UpdatedAt); d < 500*time.Millisecond {
			t.Fatalf("expected UpdateAt to default to now: %v", d)
		}
		if d1, d2 := v2.CreatedAt.Sub(v1.CreatedAt), time.Since(v1.CreatedAt); d1 != 0 ||
			d2 > 2*time.Second {
			t.Fatalf("expected CreatedAt to be unchanged now-1s: %v %v %v", v2.CreatedAt, d1, d2)
		}
	})
	t.Run("CreatedAt/UpdatedAt SET to zero value", func(t *testing.T) {
		_, _, kvs := RowMap(V{Name: "Bob"})
		v1 := insert[V](t, db, "vs", "id", kvs)
		time.Sleep(time.Second)
		_, _, kvs = RowMap(V{Name: "Alice"})
		v2 := update[V](t, db, "vs", "id", v1.ID, kvs)
		if d := v2.UpdatedAt.Sub(v1.UpdatedAt); d < 500*time.Millisecond {
			t.Fatalf("UpdatedAt not in Update: expected UpdateAt to default to now: %v", d)
		}
		if d1, d2 := v2.CreatedAt.Sub(v1.CreatedAt), time.Since(v1.CreatedAt); d1 != 0 ||
			d2 > 2*time.Second {
			t.Fatalf("expected CreatedAt to be unchanged now-1s: %v %v %v", v2.CreatedAt, d1, d2)
		}
	})
	t.Run("CreatedAt/UpdatedAt SET to custom value", func(t *testing.T) {
		_, _, kvs := RowMap(V{Name: "Bob"})
		v1 := insert[V](t, db, "vs", "id", kvs)
		time.Sleep(time.Second)
		ts := time.UnixMilli(0)
		_, _, kvs = RowMap(V{Name: "Alice", CreatedAt: ts, UpdatedAt: ts})
		v2 := update[V](t, db, "vs", "id", v1.ID, kvs)
		if v2.CreatedAt.Sub(ts) > 0 || v2.UpdatedAt.Sub(ts) > 0 {
			t.Fatalf("Expected CreatedAt/UpdatedAt to match %v: %v", ts, v2)
		}
	})
}

func insert[T any](t *testing.T, db *DB, table, idK string, kvs map[string]any) T {
	id, err := Insert(db, "", table, kvs)
	if err != nil {
		t.Fatal(err)
	}
	v, err := QueryOne[T](db, fmt.Sprintf("SELECT * FROM vs WHERE %s = ?", idK), id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func update[T any](t *testing.T, db *DB, table, idK string, idV any, kvs map[string]any) T {
	err := Update(db, table, idK, idV, kvs)
	if err != nil {
		t.Fatal(err)
	}
	v, err := QueryOne[T](db, fmt.Sprintf("SELECT * FROM vs WHERE %s = ?", idK), idV)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestFFW(t *testing.T) {
	tmp := t.TempDir()
	m1 := []string{"CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"}
	m2 := []string{"CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT, desc TEXT)"}
	m3 := []string{"CREATE TABLE items (id INTEGER PRIMARY KEY, desc TEXT)"}

	t.Run("ffw=0 blocks rebuild", func(t *testing.T) {
		db, _ := New(tmp+"/0.db", m1, nil, 0)
		if _, _, err := Exec(db, "INSERT INTO items (name) VALUES (?)", "test"); err != nil {
			t.Fatal(err)
		}
		db.Close()
		if _, err := New(tmp+"/0.db", m2, nil, 0); err != MigrateErr {
			t.Fatalf("expected MigrateErr, got %v", err)
		}
	})

	t.Run("ffw=1 allows additive", func(t *testing.T) {
		db, _ := New(tmp+"/1.db", m1, nil, 1)
		Exec(db, "INSERT INTO items (name) VALUES (?)", "test")
		db.Close()
		db, err := New(tmp+"/1.db", m2, nil, 1)
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
		rows, _ := QueryMap[any](db, "SELECT * FROM items")
		if len(rows) != 1 || rows[0]["name"] != "test" {
			t.Fatalf("expected data preserved: %v", rows)
		}
		db.Close()
	})

	t.Run("ffw=1 blocks drops", func(t *testing.T) {
		db, _ := New(tmp+"/1b.db", m2, nil, 1)
		db.Close()
		if _, err := New(tmp+"/1b.db", m3, nil, 1); err == nil || err == MigrateErr {
			t.Fatalf("expected forward-only error, got %v", err)
		}
	})

	t.Run("ffw=2 allows drops", func(t *testing.T) {
		db, _ := New(tmp+"/2.db", m2, nil, 2)
		Exec(db, "INSERT INTO items (name, desc) VALUES (?, ?)", "test", "foo")
		db.Close()
		db, err := New(tmp+"/2.db", m3, nil, 2)
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
		rows, _ := QueryMap[any](db, "SELECT * FROM items")
		if len(rows) != 1 || rows[0]["desc"] != "foo" {
			t.Fatalf("expected desc preserved, name dropped: %v", rows)
		}
		db.Close()
	})
}

func newDB[T any](t *testing.T, v T) *DB {
	t.Helper()
	f, err := os.CreateTemp("", "test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	name := reflect.TypeOf(v).Name()
	tableName := name + "s"
	db, err := New(f.Name(), []string{
		Schema(v),
		``,
		FTSIndex(fmt.Sprintf("%s_fts", name), tableName, "id", "porter", "name"),
	}, nil, 0)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
