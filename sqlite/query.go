package sqlite

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Type interface {
	ScanRows(rows *Rows) (any, error)
	PrimaryKey() (string, any)
	Row() map[string]any
	Cols() []string
}

type Stmt struct {
	Name, SQL     string
	Many, IsQuery bool
	Args          []string
	*sql.Stmt
}

type Rows struct {
	*sql.Rows
	QueryErr   error
	Many, Done bool
}

type Map[T any] map[string]T
type JSON struct{ V any }
type Null[T any] struct{ V *T }

var NoResultsErr = fmt.Errorf("empty results")

func Exec(c Connection, q string, args ...any) (sql.Result, error) {
	if stmt := c.Stmt(q); stmt != nil {
		return stmt.Exec(args...)
	}
	return c.Exec(q, args...)
}

func CheckExec(c Connection, q string, args ...any) (id, count int64, err error) {
	result, err := Exec(c, q, args...)
	if err != nil {
		return 0, 0, err
	}
	id, idErr := result.LastInsertId()
	count, countErr := result.RowsAffected()
	return id, count, errors.Join(idErr, countErr)
}

func Query[T Type](c Connection, q string, args ...any) ([]T, error) {
	if stmt := c.Stmt(q); stmt != nil {
		rows, err := stmt.Query(args...)
		return scan[T](&Rows{rows, err, true, false})
	}
	return scan[T](queryRows(c, true, q, args...))
}

func QueryOne[T Type](c Connection, q string, args ...any) (T, error) {
	return scanOne[T](queryRows(c, false, q, args...))
}

func NewQuery(name, sql string, many bool, args ...string) *Stmt {
	return &Stmt{name, sql, many, true, args, nil}
}

func NewExec(name, sql string, args ...string) *Stmt {
	return &Stmt{name, sql, false, false, args, nil}
}

// TODO: not safe
func Insert(c Connection, table string, kvs map[string]any) (int64, error) {
	cols, args := []string{}, []any{}
	for k, v := range kvs {
		cols, args = append(cols, k), append(args, v)
	}
	q := fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s)", table,
		strings.Join(cols, ","), strings.Repeat(",?", len(cols))[1:],
	)
	id, _, err := CheckExec(c, q, args...)
	// Upsert(v, x)
	return id, err
}

func (s *Stmt) Do(args map[string]any) (any, error) {
	vs := []any{}
	for k, v := range args {
		vs = append(vs, sql.Named(k, v))
	}
	if s.IsQuery {
		rows, err := s.Query(vs...)
		if err != nil {
			return nil, err
		}
		if s.Many {
			return scan[Map[any]](&Rows{rows, err, true, false})
		}
		return scanOne[Map[any]](&Rows{rows, err, false, false})
	}
	result, err := s.Exec(vs...)
	if err != nil {
		return nil, err
	}
	id, _ := result.LastInsertId()
	changed, _ := result.RowsAffected()
	return map[string]any{"id": id, "changed": changed}, nil
}

func queryRows(c Connection, many bool, q string, args ...any) *Rows {
	rows, err := c.Query(q, args...)
	return &Rows{rows, err, many, false}
}

func scanOne[T Type](rows *Rows) (T, error) {
	vs, err := scan[T](rows)
	if len(vs) == 1 && err == nil {
		return vs[0], nil
	}
	return *new(T), errors.Join(err, NoResultsErr)
}

func scan[T Type](rows *Rows) ([]T, error) {
	if rows.QueryErr != nil {
		return nil, fmt.Errorf("failed to query: %w", rows.QueryErr)
	}
	defer rows.Close()
	t := *new(T)
	vs, err := t.ScanRows(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to scan rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate rows: %w", err)
	}
	return vs.([]T), nil
}

func scanJSON(src, dst any) error {
	switch src := src.(type) {
	case []byte:
		return json.Unmarshal(src, dst)
	case string:
		return json.Unmarshal([]byte(src), dst)
	case nil:
		return nil
	default:
		return fmt.Errorf("unsupported JSON scan %T => %T", src, dst)
	}
}

func NewNull[T any](v *T) *Null[T] { return &Null[T]{v} }
func NewJSON(v any) *JSON          { return &JSON{v} }

func (r *Rows) Next() bool {
	if r.Done {
		return false
	} else if !r.Many {
		r.Done = true
	}
	return r.Rows.Next()
}

func (v *Null[T]) Scan(src any) error {
	n := &sql.Null[T]{}
	err := n.Scan(src)
	*v.V = n.V
	return err
}

func (v *JSON) Scan(src any) error { return scanJSON(src, &v.V) }
func (v *JSON) Value() (driver.Value, error) {
	bs, err := json.Marshal(v.V)
	return string(bs), err
}

func (m Map[T]) Cols() []string { return []string{"id", "map"} }
func (m Map[T]) Row() map[string]any {
	row := map[string]any{}
	for k, v := range m {
		row[k] = v
	}
	return row
}
func (m Map[T]) PrimaryKey() (string, any) {
	if id, ok := m["id"]; ok {
		return "id", id
	}
	return "", nil
}

func (m Map[T]) ScanRows(rows *Rows) (any, error) {
	cts, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}
	vs, cols := []Map[T]{}, []string{}
	for _, ct := range cts {
		c := ct.Name()
		if ct.DatabaseTypeName() == "JSON_TEXT" {
			c = "@" + c
		}
		cols = append(cols, c)
	}
	for rows.Next() {
		m, row := Map[T]{}, make([]any, len(cols))
		for i := range cols {
			row[i] = new(T)
		}
		if err := rows.Scan(row...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		if len(cols) == 1 && cols[0] == "map" {
			if err := scanJSON(*(row[0]).(*any), m); err != nil {
				return nil, fmt.Errorf("failed to scan row: %w", err)
			}
		} else {
			for i, c := range cols {
				if c[0] == '@' {
					v := new(T)
					if err := scanJSON(*(row[i]).(*T), v); err != nil {
						return nil, fmt.Errorf("failed to scan row: %w", err)
					}
					m[c[1:]] = *v
				} else {
					m[c] = *(row[i]).(*T)
				}
			}
		}
		vs = append(vs, m)
	}
	return vs, nil
}
