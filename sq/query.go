package sq

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
)

var ErrNoResults = fmt.Errorf("empty results")
var ErrAbortScan = fmt.Errorf("early exit scan")

func Insert(c Connection, or, table string, kvs map[string]any) (int64, error) {
	return InsertContext(context.Background(), c, or, table, kvs)
}

func InsertContext(ctx context.Context, c Connection, or, table string, kvs map[string]any) (int64, error) {
	id, _, err := ExecContext(ctx, c, "INSERT "+or+` INTO {'table} {values "kvs"}`, Args{
		"table": table,
		"kvs":   kvs,
	})
	return id, err
}

func Exec(c Connection, q string, args ...any) (id, count int64, err error) {
	return ExecContext(context.Background(), c, q, args...)
}

func ExecContext(ctx context.Context, c Connection, q string, args ...any) (id, count int64, err error) {
	q, args, err = maybeRenderQuery(q, args)
	if err != nil {
		return 0, 0, err
	}
	result, err := c.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, 0, err
	}
	id, idErr := result.LastInsertId()
	count, countErr := result.RowsAffected()
	return id, count, errors.Join(idErr, countErr)
}

func Update(c Connection, table, k string, v any, kvs map[string]any) error {
	return UpdateContext(context.Background(), c, table, k, v, kvs)
}

func UpdateContext(ctx context.Context, c Connection, table, k string, v any, kvs map[string]any) error {
	_, n, err := ExecContext(ctx, c, `UPDATE {'table} SET {set "kvs"} WHERE {'k} = {$v}`, Args{
		"table": table, "kvs": kvs, "k": k, "v": v,
	})
	if n != 1 {
		return fmt.Errorf("unexpectedly changed %d rows: %w", n, err)
	}
	return err
}

func Query[T any](c Connection, q string, args ...any) (vs []T, err error) {
	return QueryContext[T](context.Background(), c, q, args...)
}

func QueryContext[T any](ctx context.Context, c Connection, q string, args ...any) (vs []T, err error) {
	if reflect.TypeOf(*new(T)).Kind() == reflect.Struct {
		err = QueryRowsContext(ctx, c, q, func(v T) error {
			vs = append(vs, v)
			return nil
		}, args...)
	} else {
		err = QueryValRowsContext(ctx, c, q, func(v T) error {
			vs = append(vs, v)
			return nil
		}, args...)
	}
	return vs, err
}

func QueryMap[T any](c Connection, q string, args ...any) (vs []map[string]T, err error) {
	return QueryMapContext[T](context.Background(), c, q, args...)
}

func QueryMapContext[T any](ctx context.Context, c Connection, q string, args ...any) (vs []map[string]T, err error) {
	err = QueryMapRowsContext(ctx, c, q, func(v map[string]T) error {
		vs = append(vs, v)
		return nil
	}, args...)
	return vs, err
}

func QueryOne[T any](c Connection, q string, args ...any) (v T, err error) {
	return QueryOneContext[T](context.Background(), c, q, args...)
}

func QueryOneContext[T any](ctx context.Context, c Connection, q string, args ...any) (v T, err error) {
	noResultsErr := ErrNoResults
	if reflect.TypeOf(*new(T)).Kind() == reflect.Struct {
		err = QueryRowsContext(ctx, c, q, func(_v T) error {
			v, noResultsErr = _v, nil
			return ErrAbortScan
		}, args...)
	} else {
		err = QueryValRowsContext(ctx, c, q, func(_v T) error {
			v, noResultsErr = _v, nil
			return ErrAbortScan
		}, args...)
	}
	if err != nil {
		return v, err
	}
	return v, noResultsErr
}

func QueryOneMap[T any](c Connection, q string, args ...any) (v map[string]T, err error) {
	return QueryOneMapContext[T](context.Background(), c, q, args...)
}

func QueryOneMapContext[T any](ctx context.Context, c Connection, q string, args ...any) (v map[string]T, err error) {
	noResultsErr := ErrNoResults
	err = QueryMapRowsContext(ctx, c, q, func(_v map[string]T) error {
		v, noResultsErr = _v, nil
		return ErrAbortScan
	}, args...)
	if err != nil {
		return v, err
	}
	return v, noResultsErr
}

func Each[T any](c Connection, q string, f func(T) error, args ...any) error {
	return EachContext(context.Background(), c, q, f, args...)
}

func EachContext[T any](ctx context.Context, c Connection, q string, f func(T) error, args ...any) error {
	if reflect.TypeOf(*new(T)).Kind() == reflect.Struct {
		return QueryRowsContext(ctx, c, q, f, args...)
	} else {
		return QueryValRowsContext(ctx, c, q, f, args...)
	}
}

func EachMap[T any](c Connection, q string, f func(map[string]T) error, args ...any) error {
	return EachMapContext(context.Background(), c, q, f, args...)
}

func EachMapContext[T any](ctx context.Context, c Connection, q string, f func(map[string]T) error, args ...any) error {
	return QueryMapRowsContext(ctx, c, q, f, args...)
}

func Scan[T any](rows *sql.Rows, f func(T) error) error {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("column types: %w", err)
	}
	v := *new(T)
	t := reflect.TypeOf(v)
	fields := make(map[string]int, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		k := normalizedCol(t.Field(i).Name)
		fields[k] = i
	}
	type colMapper struct {
		i      int
		v      any
		isJSON bool
	}
	mappers := make([]colMapper, len(cols))
	ptrs, row := make([]any, len(cols)), make([]any, len(cols))
	for i, c := range cols {
		k := normalizedCol(c)
		if fi, ok := fields[k]; ok {
			if ft := t.Field(fi).Type; jsonDefault(ft) != "" {
				mappers[i] = colMapper{i: fi, isJSON: true}
			} else {
				ptr := reflect.New(reflect.PointerTo(ft))
				mappers[i] = colMapper{i: fi, v: ptr.Interface()}
				ptrs[i] = mappers[i].v
			}
		} else {
			mappers[i], ptrs[i] = colMapper{i: -1}, new(any)
		}
	}
	for rows.Next() {
		v := *new(T)
		rv := reflect.ValueOf(&v).Elem()
		for i, m := range mappers {
			if m.isJSON {
				row[i] = &JSON{V: rv.Field(m.i).Addr().Interface()}
			} else {
				row[i] = ptrs[i]
			}
		}
		if err := rows.Scan(row...); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}
		for i, m := range mappers {
			if m.i != -1 && !m.isJSON {
				if ptr := reflect.ValueOf(row[i]); !ptr.Elem().IsNil() {
					rv.Field(m.i).Set(ptr.Elem().Elem())
				}
			}
		}
		if err := f(v); errors.Is(err, ErrAbortScan) {
			break
		} else if err != nil {
			return err
		}
	}
	return rows.Err()
}

func ScanMap[T any](rows *sql.Rows, f func(map[string]T) error) error {
	defer rows.Close()
	cts, err := rows.ColumnTypes()
	if err != nil {
		return fmt.Errorf("failed to get columns: %w", err)
	}
	cols, jsonCols := make([]string, len(cts)), make(map[string]bool, len(cts))
	for i, ct := range cts {
		c, t := ct.Name(), ct.DatabaseTypeName()
		if c[0] == '$' {
			c, t = c[1:], "JSON_TEXT"
		}
		cols[i], jsonCols[c] = c, t == "JSON_TEXT"
	}
	for rows.Next() {
		row := make([]any, len(cols))
		for i, c := range cols {
			if jsonCols[c] {
				row[i] = &JSON{new(T)}
			} else {
				row[i] = new(T)
			}
		}
		if err := rows.Scan(row...); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}
		m := make(map[string]T, len(cols))
		for i, c := range cols {
			if jsonCols[c] {
				m[c] = *(row[i].(*JSON).V.(*T))
			} else {
				m[c] = *row[i].(*T)
			}
		}
		if err := f(m); errors.Is(err, ErrAbortScan) {
			return nil
		} else if err != nil {
			return err
		}
	}
	return rows.Err()
}

func ScanVal[T any](rows *sql.Rows, f func(T) error) error {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("failed to get columns: %w", err)
	} else if len(cols) != 1 {
		return fmt.Errorf("must select a single col (%v)", cols)
	}
	for rows.Next() {
		nv := &sql.Null[T]{}
		if err := rows.Scan(nv); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}
		if err := f(nv.V); errors.Is(err, ErrAbortScan) {
			return nil
		} else if err != nil {
			return err
		}
	}
	return rows.Err()
}

func QueryRows[T any](c Connection, q string, f func(T) error, args ...any) error {
	return QueryRowsContext(context.Background(), c, q, f, args...)
}

func QueryRowsContext[T any](ctx context.Context, c Connection, q string, f func(T) error, args ...any) error {
	q, args, err := maybeRenderQuery(q, args)
	if err != nil {
		return fmt.Errorf("failed to render query: %w", err)
	}
	rows, err := c.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}
	return Scan(rows, f)
}

func QueryMapRows[T any](c Connection, q string, f func(map[string]T) error, args ...any) error {
	return QueryMapRowsContext(context.Background(), c, q, f, args...)
}

func QueryMapRowsContext[T any](ctx context.Context, c Connection, q string, f func(map[string]T) error, args ...any) error {
	q, args, err := maybeRenderQuery(q, args)
	if err != nil {
		return fmt.Errorf("failed to render query: %w", err)
	}
	rows, err := c.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}
	return ScanMap(rows, f)
}

func QueryValRows[T any](c Connection, q string, f func(T) error, args ...any) error {
	return QueryValRowsContext(context.Background(), c, q, f, args...)
}

func QueryValRowsContext[T any](ctx context.Context, c Connection, q string, f func(T) error, args ...any) error {
	q, args, err := maybeRenderQuery(q, args)
	if err != nil {
		return fmt.Errorf("failed to render query: %w", err)
	}
	rows, err := c.QueryContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("failed to execute query: %w", err)
	}
	return ScanVal(rows, f)
}

func maybeRenderQuery(q string, args []any) (string, []any, error) {
	if len(args) >= 1 {
		a, ok := args[0].(Args)
		if ok {
			if len(args) > 1 {
				return "", nil, fmt.Errorf("Args must be the only arg")
			}
			return a.Render(q)
		}
	}
	return q, args, nil
}
