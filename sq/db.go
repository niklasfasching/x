// TODO: remove tag once stable; for sane empty map/slice marshalling
//go:build goexperiment.jsonv2

package sq

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"

	sqlite3 "github.com/mattn/go-sqlite3"
)

type Connection interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type DB struct {
	*sql.DB
	ConnectHook func(c *sqlite3.SQLiteConn) error
}

type Table[T any] struct {
	name, idCol string
	*DB
}

func New(uri string, migrations []string, f func(c *sqlite3.SQLiteConn) error, ffw int) (*DB, error) {
	d, driver := &DB{}, "sqlite3"
	if f != nil {
		driver = fmt.Sprintf("sqlite3-%d", driverIndex)
		driverIndex++
		sql.Register(driver, &sqlite3.SQLiteDriver{ConnectHook: f})
	}
	db, err := sql.Open(driver, uri)
	if err != nil {
		return nil, fmt.Errorf("failed to open: %w", err)
	}
	d.DB = db
	ctx := context.Background()
	err = d.MigrateContext(ctx, migrations)
	if ffw == 0 {
		return d, err
	}
	mErr := &MigrateError{}
	if !errors.As(err, &mErr) || mErr.Reason != "rebuild" {
		return d, err
	}
	log.Println("FFW migrating to new schema...")
	parts := strings.Split(uri, "?")
	oldName, newName := parts[0], parts[0]+".tmp"
	newURI := newName
	if len(parts) > 1 {
		newURI = newName + "?" + parts[1]
	}
	newDB, err := New(newURI, migrations, f, 0)
	if err != nil {
		return nil, err
	}
	defer os.Remove(newName)
	defer newDB.Close()
	if err := CopyContext(ctx, oldName, d, newDB, ffw == 1); err != nil {
		return nil, err
	} else if err := errors.Join(db.Close(), newDB.Close(), os.Rename(newName, oldName)); err != nil {
		return nil, err
	}
	return New(uri, migrations, f, 0)
}

func FuncHook(fs map[string]any) func(c *sqlite3.SQLiteConn) error {
	all := map[string]any{}
	maps.Copy(all, defaultFuncs)
	maps.Copy(all, fs)
	return func(c *sqlite3.SQLiteConn) error {
		for name, f := range all {
			v, isPure := f.(PureFunc)
			if isPure {
				f = v.F
			}
			if err := c.RegisterFunc(name, f, isPure); err != nil {
				return err
			}
		}
		return nil
	}
}

func NewTable[T any](db *DB, name, idCol string) *Table[T] {
	return &Table[T]{name, normalizedCol(idCol), db}
}

func (t *Table[T]) Count(conds string, args ...any) (int, error) {
	return t.CountContext(context.Background(), conds, args...)
}

func (t *Table[T]) CountContext(ctx context.Context, conds string, args ...any) (int, error) {
	n, err := QueryOneContext[int](ctx, t.DB, "SELECT count(1) FROM `"+t.name+"` WHERE "+conds, args...)
	return n, err
}

func (t *Table[T]) Insert(or string, v T) (int64, error) {
	return t.InsertContext(context.Background(), or, v)
}

func (t *Table[T]) InsertContext(ctx context.Context, or string, v T) (int64, error) {
	idK, idV, kvs := RowMap(v)
	if rv := reflect.ValueOf(idV); !rv.IsZero() {
		kvs[idK] = idV
	}
	return InsertContext(ctx, t.DB, or, t.name, kvs)
}

func (t *Table[T]) Modify(id any, f func(*T) error, ks ...string) error {
	return t.ModifyContext(context.Background(), id, f, ks...)
}

func (t *Table[T]) ModifyContext(ctx context.Context, id any, f func(*T) error, ks ...string) error {
	v, err := QueryOneContext[T](ctx, t.DB, `SELECT {cols "cols"} FROM {'table} WHERE {'k} = {$v}`, Args{
		"k": t.idCol, "v": id, "table": t.name, "cols": append(ks, t.idCol),
	})
	if err != nil {
		return err
	} else if err := f(&v); err != nil {
		return err
	}
	return t.UpdateContext(ctx, v, ks...)
}

func (t *Table[T]) Update(v T, ks ...string) error {
	return t.UpdateContext(context.Background(), v, ks...)
}

func (t *Table[T]) UpdateContext(ctx context.Context, v T, ks ...string) error {
	idK, idV, kvs := RowMap(v)
	if rv := reflect.ValueOf(idV); rv.IsZero() {
		return fmt.Errorf("update requires non-empty %q: %#v", idK, v)
	}
	if len(ks) == 0 {
		ks = slices.Collect(maps.Keys(kvs))
	}
	nkvs := map[string]any{}
	for _, k := range ks {
		if v, ok := kvs[k]; ok {
			nkvs[k] = v
		} else {
			return fmt.Errorf("k %q not in %v", k, kvs)
		}
	}
	kvs = nkvs
	return UpdateContext(ctx, t.DB, t.name, idK, idV, kvs)
}

func (db *DB) Migrate(migrations []string) error {
	return db.MigrateContext(context.Background(), migrations)
}

func (db *DB) MigrateContext(ctx context.Context, migrations []string) error {
	if migrations == nil {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, _, err = ExecContext(ctx, tx, `CREATE TABLE IF NOT EXISTS _migrations (sql TEXT)`)
	if err != nil {
		return fmt.Errorf("failed to create _migrations table: %w", err)
	}
	appliedMigrations, err := QueryMapContext[string](ctx, tx, "SELECT sql FROM _migrations")
	if err != nil {
		return fmt.Errorf("failed to query _migrations: %w", err)
	}
	rebuild := len(migrations) != len(appliedMigrations)
	if !rebuild {
		for i := range appliedMigrations {
			rebuild = rebuild || migrations[i] != appliedMigrations[i]["sql"]
		}
	}
	if !rebuild || len(appliedMigrations) == 0 {
		for _, stmt := range migrations[len(appliedMigrations):] {
			if _, _, err := ExecContext(ctx, tx, stmt); err != nil {
				return fmt.Errorf("failed to apply migration %q: %w", stmt, err)
			}
			if _, _, err := ExecContext(ctx, tx, "INSERT INTO _migrations (sql) VALUES (?)", stmt); err != nil {
				return fmt.Errorf("failed to record migration %q: %w", stmt, err)
			}
		}
		return tx.Commit()
	}
	return &MigrateError{Reason: "rebuild"}
}
