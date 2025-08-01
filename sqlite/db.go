package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"maps"
	"os"
	"strings"

	sqlite3 "github.com/mattn/go-sqlite3"
	"golang.org/x/exp/slices"
)

type Connection interface {
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
	Stmt(string) *sql.Stmt
}

type DB struct {
	funcs map[string]any
	stmts map[string]*sql.Stmt
	*sql.DB
}

type Tx struct {
	*sql.Tx
	*DB
}

type PureFunc any

var driverIndex = 0
var defaultFuncs = map[string]any{
	"re_extract": PureFunc(regexpExtract),
	// "fts_index":  ftsIndex,
}

func New(name string, migrations []string, stmts map[string]string, fs map[string]any, ffw bool) (*DB, error) {
	d := &DB{
		funcs: map[string]any{},
		stmts: map[string]*sql.Stmt{},
	}
	maps.Copy(d.funcs, defaultFuncs)
	maps.Copy(d.funcs, fs)
	driver := fmt.Sprintf("sqlite3-%d", driverIndex)
	driverIndex++
	sql.Register(driver, &sqlite3.SQLiteDriver{ConnectHook: d.connectHook})
	db, err := sql.Open(driver, name)
	if err != nil {
		return nil, fmt.Errorf("failed to open: %w", err)
	}
	d.DB = db
	for k, sql := range stmts {
		stmt, err := db.Prepare(sql)
		if err != nil {
			return nil, err
		}
		d.stmts[k] = stmt
	}
	return d.migrate(name, migrations, stmts, fs, ffw)
}

func (db *DB) Begin() (*Tx, error) {
	return db.BeginTx(context.Background(), nil)
}

func (db *DB) Stmt(k string) *sql.Stmt {
	return db.stmts[k]
}

func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	tx, err := db.DB.BeginTx(ctx, opts)
	return &Tx{tx, db}, err
}

func (tx *Tx) Stmt(k string) *sql.Stmt {
	if stmt := tx.DB.Stmt(k); stmt != nil {
		return tx.Tx.Stmt(stmt)
	}
	return nil
}

func (db *DB) connectHook(c *sqlite3.SQLiteConn) error {
	for name, f := range db.funcs {
		_, isPure := f.(PureFunc)
		if err := c.RegisterFunc(name, f, isPure); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) Tables() (map[string][]string, error) {
	sql := `SELECT name, (SELECT group_concat(name) FROM pragma_table_info(tl.name)) as columns
            FROM pragma_table_list tl
            WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != '_migrations'`
	ts, err := Query[Map[string]](db, sql)
	m := map[string][]string{}
	for _, t := range ts {
		m[t["name"]] = strings.Split(t["columns"], ",")
	}
	return m, err
}

func (db *DB) migrate(name string, migrations []string, stmts map[string]string, fs map[string]any, ffw bool) (*DB, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS _migrations (sql TEXT)`)
	if err != nil {
		return nil, fmt.Errorf("failed to create _migrations table: %w", err)
	}
	appliedMigrations, err := Query[Map[string]](tx, "SELECT sql FROM _migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to query _migrations: %w", err)
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
				return nil, fmt.Errorf("failed to apply migration %q: %w", stmt, err)
			}
			if _, err := tx.Exec("INSERT INTO _migrations (sql) VALUES (?)", stmt); err != nil {
				return nil, fmt.Errorf("failed to record migration %q: %w", stmt, err)
			}
		}
		return db, tx.Commit()
	} else if !ffw {
		return nil, fmt.Errorf("migrations do not match existing schema and ffw is disabled")
	}
	newName := name + ".tmp"
	os.Remove(newName)
	newDB, err := New(newName, migrations, stmts, fs, ffw)
	if err != nil {
		return nil, fmt.Errorf("failed to open rebuild db: %w", err)
	}
	defer os.Remove(newName)
	defer newDB.Close()
	newTX, err := newDB.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to open rebuild tx: %w", err)
	}
	defer newTX.Rollback()
	oldTables, err := db.Tables()
	if err != nil {
		return nil, fmt.Errorf("failed to list tables to rebuild: %w", err)
	}
	newTables, err := newDB.Tables()
	if err != nil {
		return nil, fmt.Errorf("failed to list tables to rebuild: %w", err)
	}
	if _, err := newTX.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS old", name)); err != nil {
		return nil, fmt.Errorf("failed to attach existing db: %w", err)
	}
	for name, oldCols := range oldTables {
		newCols := newTables[name]
		if len(newCols) == 0 {
			continue
		}
		cols := []string{}
		for _, c := range newCols {
			if slices.Contains(oldCols, c) {
				cols = append(cols, c)
			}
		}
		sql := fmt.Sprintf(`INSERT INTO %[1]s (%[2]s) SELECT %[2]s FROM old.%[1]s`,
			name, strings.Join(cols, ","))
		if _, err := newTX.Exec(sql); err != nil {
			return nil, fmt.Errorf("failed to copy data from table %q: %w", name, err)
		}
	}
	if err := newTX.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit rebuild tx: %w", err)
	}
	if err := cmp.Or(db.Close(), newDB.Close(), os.Rename(newName, name)); err != nil {
		return nil, fmt.Errorf("failed to rename rebuilt db: %w", err)
	}
	return New(name, migrations, stmts, fs, ffw)
}
