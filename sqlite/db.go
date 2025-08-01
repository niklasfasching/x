package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"maps"

	sqlite3 "github.com/mattn/go-sqlite3"
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

func New(name string, migrations []string, stmts map[string]string, fs map[string]any) (*DB, error) {
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
	return d, d.migrate(migrations)
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
	appliedMigrations, err := Query[Map[string]](tx, "SELECT sql FROM _migrations")
	if err != nil {
		return fmt.Errorf("failed to query _migrations: %w", err)
	}
	rebuild := len(migrations) < len(appliedMigrations)
	if !rebuild {
		for i := range appliedMigrations {
			rebuild = rebuild || migrations[i] != appliedMigrations[i]["sql"]
		}
	}
	if rebuild {
		panic("rebuild")
	}
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
