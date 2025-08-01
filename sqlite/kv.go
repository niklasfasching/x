package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type KV[K comparable, V Type] struct {
	query, insert *sql.Stmt
	*Table[V]
}

func NewKV[K comparable, V Type](db *DB, table string) (*KV[K, V], error) {
	t, err := NewTable[V](db, table)
	if err != nil {
		return nil, err
	}
	sql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s` (_k_ PRIMARY KEY UNIQUE, %s);",
		table, strings.Join(t.cols, ", "))
	if _, err := db.Exec(sql); err != nil {
		return nil, fmt.Errorf("failed to create table %q: %w", sql, err)
	}
	sql = fmt.Sprintf("SELECT %s FROM `%s` WHERE _k_ = ? LIMIT 1",
		strings.Join(t.cols, ", "), t.name)
	sQ, err := db.Prepare(sql)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare query %q: %w", sql, err)
	}
	sql = fmt.Sprintf("INSERT OR REPLACE INTO `%s` VALUES (? %s)",
		t.name, strings.Repeat(", ?", len(t.cols)))
	sI, err := db.Prepare(sql)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare insert %q: %w", sql, err)
	}
	return &KV[K, V]{sQ, sI, t}, nil
}

func (kv *KV[K, V]) Get(k K) (V, error) {
	rows, err := kv.query.Query(k)
	v, err := scanOne[V](&Rows{rows, err, false, false})
	return v, err
}

func (kv *KV[K, V]) Set(k K, v V) error {
	row, args := v.Row(), []any{k}
	for _, c := range kv.cols {
		args = append(args, row[c])
	}
	_, err := kv.insert.Exec(args...)
	return err
}

func (kv *KV[K, V]) Close() error {
	return errors.Join(kv.query.Close(), kv.insert.Close())
}
