package sq

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
)

type KV[K comparable, V any] struct {
	query, insert *sql.Stmt
	isStruct      bool
}

func NewKV[K comparable, V any](db *DB, table string) (*KV[K, V], error) {
	if _, err := db.ExecContext(context.Background(), KVSchema(table)); err != nil {
		return nil, fmt.Errorf("failed to create kv table: %w", err)
	}
	sQ, err := db.Prepare(fmt.Sprintf("SELECT v FROM `%s` WHERE _k_ = ? LIMIT 1", table))
	if err != nil {
		return nil, fmt.Errorf("failed to prepare kv query: %w", err)
	}
	sI, err := db.Prepare(fmt.Sprintf("INSERT OR REPLACE INTO `%s` VALUES (?, ?)", table))
	if err != nil {
		return nil, fmt.Errorf("failed to prepare insert: %w", err)
	}
	return &KV[K, V]{sQ, sI, reflect.TypeOf(*new(V)).Kind() == reflect.Struct}, nil
}

func KVSchema(table string) string {
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s` (_k_ TEXT PRIMARY KEY UNIQUE, v TEXT);", table)
}

func (kv *KV[K, V]) Get(k K) (v V, err error) {
	rows, err := kv.query.QueryContext(context.Background(), k)
	if err != nil {
		return v, fmt.Errorf("failed to execute query: %w", err)
	}
	noResultsErr := ErrNoResults
	if kv.isStruct {
		err = Scan(rows, func(_v V) error {
			v, noResultsErr = _v, nil
			return ErrAbortScan
		})
	} else {
		err = ScanVal(rows, func(_v V) error {
			v, noResultsErr = _v, nil
			return ErrAbortScan
		})
	}
	if err != nil {
		return v, err
	}
	return v, noResultsErr
}

func (kv *KV[K, V]) Set(k K, v V) error {
	if kv.isStruct {
		_, err := kv.insert.ExecContext(context.Background(), k, &JSON{v})
		return err
	} else {
		_, err := kv.insert.ExecContext(context.Background(), k, v)
		return err
	}
}

func (kv *KV[K, V]) Close() error {
	return errors.Join(kv.query.Close(), kv.insert.Close())
}
