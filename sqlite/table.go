package sqlite

import (
	"fmt"
	"strings"
)

type Table[T Type] struct {
	name string
	cols []string
	*DB
}

func NewTable[T Type](db *DB, name string) (*Table[T], error) {
	return &Table[T]{name, (*new(T)).Cols(), db}, nil
}

func (t *Table[T]) Insert(v T) (int64, error) {
	cols, args := []string{}, []any{}
	for k, v := range v.Row() {
		cols, args = append(cols, k), append(args, v)
	}
	q := fmt.Sprintf("INSERT OR REPLACE INTO `%s` (%s) VALUES (%s)", t.name,
		strings.Join(cols, ","), strings.Repeat(",?", len(cols))[1:],
	)
	id, _, err := CheckExec(t.DB, q, args...)
	return id, err
}

func (t *Table[T]) Update(v T, ks ...string) (int64, error) {
	pkCol, pk := v.PrimaryKey()
	if pk == 0 {
		return 0, fmt.Errorf("Type without primary key")
	}
	row, cols, args := v.Row(), []string{}, []any{}
	for _, k := range ks {
		if v, ok := row[k]; ok {
			cols, args = append(cols, k), append(args, v)
		}
	}
	q := fmt.Sprintf("UPDATE `%s` SET (%s) = (%s) WHERE %s = ?",
		t.name, strings.Join(cols, ","), strings.Repeat(",?", len(cols))[1:], pkCol,
	)
	_, changed, err := CheckExec(t.DB, q, append(args, pk)...)
	return changed, err
}

func (t *Table[T]) Query(q string, args ...any) ([]T, error) {
	return scan[T](queryRows(t.DB, true, q, args...))
}

func (t *Table[T]) QueryOne(q string, args ...any) (T, error) {
	return scanOne[T](queryRows(t.DB, false, q, args...))
}

func (t *Table[T]) MapOne(q string, f func(*T) error, args ...any) (T, error) {
	vs, err := t.Map(q, f, args...)
	if err != nil {
		return *new(T), err
	} else if len(vs) != 1 {
		return *new(T), fmt.Errorf("did not update a single row but %d", len(vs))
	}
	return vs[0], nil
}

func (t *Table[T]) Map(q string, f func(*T) error, args ...any) ([]T, error) {
	rows := queryRows(t.DB, true, q, args...)
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	vs, err := scan[T](rows)
	if err != nil {
		return nil, err
	}
	for i, v := range vs {
		if err := f(&v); err != nil {
			return nil, err
		}
		vs[i] = v
		pkc, pkv := v.PrimaryKey()
		pkc = strings.ToLower(pkc)
		row, rcs, rvs := v.Row(), []string{}, []any{}
		for _, c := range cols {
			if c == pkc {
				continue
			}
			rcs, rvs = append(rcs, c), append(rvs, row[c])
		}
		rvs = append(rvs, pkv)
		q := fmt.Sprintf("UPDATE %s SET (%s) = (?%s) WHERE %s = ?", t.name,
			strings.Join(rcs, ","), strings.Repeat(",?", len(rcs)-1), pkc)
		if r, err := t.DB.Exec(q, rvs...); err != nil {
			return vs, err
		} else if c, err := r.RowsAffected(); err != nil {
			return vs, err
		} else if c != 1 {
			return vs, fmt.Errorf("expected 1 row to be modified, not %d", c)
		}
	}
	return vs, nil
}
