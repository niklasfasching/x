package util

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
)

type Cache[T any] string

func NewCache[T any](base, key string) (Cache[T], error) {
	if base == "-" {
		return Cache[T]("-"), nil
	}
	p := filepath.Join(base, key)
	return Cache[T](p), os.MkdirAll(p, os.ModePerm)
}

func (c Cache[T]) Get(k string, f func() (T, error)) (T, error) {
	if string(c) == "-" {
		return f()
	}
	h := sha256.New()
	h.Write([]byte(k))
	p := filepath.Join(string(c), fmt.Sprintf("%x", h.Sum(nil)))
	if f, err := os.Open(p); err == nil {
		defer f.Close()
		d, v := gob.NewDecoder(f), *new(T)
		return v, d.Decode(&v)
	}
	b := &bytes.Buffer{}
	if v, err := f(); err != nil {
		return v, err
	} else if err := gob.NewEncoder(b).Encode(v); err != nil {
		return v, nil
	} else if err := os.WriteFile(p, b.Bytes(), 0644); err != nil {
		return v, nil
	} else {
		return v, nil
	}
}
