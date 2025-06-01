package util

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/exp/maps"
	"golang.org/x/sync/singleflight"
)

type FileCache[T any] struct {
	Path, Ext string
	Timeout   time.Duration
}

type RingCache[K comparable, V any] struct {
	fn func(context.Context, K) (V, error)
	vs map[K]V
	ks []*K
	i  int
	singleflight.Group
	sync.Mutex
}

func NewFileCache[T any](dir, key, ext string, timeout time.Duration) (*FileCache[T], error) {
	if dir == "-" {
		return &FileCache[T]{"-", ext, timeout}, nil
	}
	path := filepath.Join(dir, key)
	return &FileCache[T]{path, ext, timeout}, os.MkdirAll(path, os.ModePerm)
}

func (c *FileCache[T]) Get(k string, f func() (T, error)) (T, error) {
	if c.Path == "-" {
		return f()
	}
	h := sha256.New()
	h.Write([]byte(k))
	p := filepath.Join(c.Path, fmt.Sprintf("%x%s", h.Sum(nil), c.Ext))
	fi, err := os.Stat(p)
	if ok := err == nil && (c.Timeout < 0 || time.Now().Sub(fi.ModTime()) < c.Timeout); ok {
		if f, err := os.Open(p); err == nil {
			defer f.Close()
			d, v := gob.NewDecoder(f), *new(T)
			return v, d.Decode(&v)
		}
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

func NewRingCache[K comparable, V any](n int, fn func(context.Context, K) (V, error)) *RingCache[K, V] {
	return &RingCache[K, V]{
		fn: fn,
		vs: make(map[K]V, n),
		ks: make([]*K, n),
	}
}

func (c *RingCache[K, V]) Map() map[K]V {
	return maps.Clone(c.vs)
}

func (c *RingCache[K, V]) Get(ctx context.Context, k K) (V, error) {
	if ctx.Err() != nil {
		return *new(V), ctx.Err()
	}
	c.Lock()
	v, ok := c.vs[k]
	c.Unlock()
	if ok {
		return v, nil
	}
	r, err, _ := c.Do(fmt.Sprintf("%v", k), func() (any, error) { return c.fn(ctx, k) })
	if err != nil {
		return *new(V), err
	}
	c.Lock()
	defer c.Unlock()
	if k2 := c.ks[c.i]; k2 != nil {
		delete(c.vs, *k2)
	}
	c.ks[c.i], c.vs[k] = &k, r.(V)
	c.i = (c.i + 1) % len(c.ks)
	return r.(V), err
}
