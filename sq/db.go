// TODO: remove tag once stable; for sane empty map/slice marshalling
//go:build goexperiment.jsonv2

package sq

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"reflect"
	"strings"

	sqlite3 "github.com/mattn/go-sqlite3"
)

type Connection interface {
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
}

type DB struct {
	funcs map[string]any
	*sql.DB
}

type Table[T any] struct {
	name, idCol string
	*DB
}

func New(uri string, migrations []string, fs map[string]any, ffw bool) (*DB, error) {
	d := &DB{funcs: map[string]any{}}
	maps.Copy(d.funcs, defaultFuncs)
	maps.Copy(d.funcs, fs)
	driver := fmt.Sprintf("sqlite3-%d", driverIndex)
	driverIndex++
	sql.Register(driver, &sqlite3.SQLiteDriver{ConnectHook: d.connectHook})
	db, err := sql.Open(driver, uri)
	if err != nil {
		return nil, fmt.Errorf("failed to open: %w", err)
	}
	d.DB = db
	if err := d.migrate(migrations); !ffw || err != MigrateErr {
		return d, err
	}
	log.Println("FFW migrating to new schema...")
	parts := strings.Split(uri, "?")
	oldName, newName := parts[0], parts[0]+".tmp"
	newURI := newName
	if len(parts) > 1 {
		newURI = newName + "?" + parts[1]
	}
	newDB, err := New(newURI, migrations, fs, false)
	if err != nil {
		return nil, err
	}
	defer os.Remove(newName)
	defer newDB.Close()
	if err := Copy(oldName, d, newDB); err != nil {
		return nil, err
	} else if err := errors.Join(db.Close(), newDB.Close(), os.Rename(newName, oldName)); err != nil {
		return nil, err
	}
	return New(uri, migrations, fs, false)
}

func NewTable[T any](db *DB, name, idCol string) (*Table[T], error) {
	return &Table[T]{name, normalizedCol(idCol), db}, nil
}

func (t *Table[T]) Count(conds string, args ...any) (int, error) {
	n, err := QueryOne[int](t.DB, "SELECT count(1) FROM `"+t.name+"` WHERE "+conds, args...)
	return n, err
}

func (t *Table[T]) Insert(or string, v T) (int64, error) {
	idK, idV, kvs := RowMap(v)
	if rv := reflect.ValueOf(idV); !rv.IsZero() {
		kvs[idK] = idV
	}
	return Insert(t.DB, or, t.name, kvs)
}

func (t *Table[T]) Modify(id any, f func(*T) error, ks ...string) error {
	v, err := QueryOne[T](t.DB, `SELECT {cols "cols"} FROM {'table} WHERE {'k} = {$v}`, Args{
		"k": t.idCol, "v": id, "table": t.name, "cols": append(ks, t.idCol),
	})
	if err != nil {
		return err
	} else if err := f(&v); err != nil {
		return err
	}
	return t.Update(v, ks...)
}

func (t *Table[T]) Update(v T, ks ...string) error {
	if len(ks) == 0 {
		return fmt.Errorf("update w/o keys")
	}
	idK, idV, kvs := RowMap(v)
	if rv := reflect.ValueOf(idV); rv.IsZero() {
		return fmt.Errorf("update requires non-empty %q: %#v", idK, v)
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
	return Update(t.DB, t.name, idK, idV, kvs)
}

func (db *DB) connectHook(c *sqlite3.SQLiteConn) error {
	for name, f := range db.funcs {
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

func (db *DB) migrate(migrations []string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS _migrations (sql TEXT)`)
	if err != nil {
		return fmt.Errorf("failed to create _migrations table: %w", err)
	}
	appliedMigrations, err := QueryMap[string](tx, "SELECT sql FROM _migrations")
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
			if _, err := tx.Exec(stmt); err != nil {
				return fmt.Errorf("failed to apply migration %q: %w", stmt, err)
			}
			if _, err := tx.Exec("INSERT INTO _migrations (sql) VALUES (?)", stmt); err != nil {
				return fmt.Errorf("failed to record migration %q: %w", stmt, err)
			}
		}
		return tx.Commit()
	}
	return MigrateErr
}
