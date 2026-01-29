package util

import (
	"cmp"
	"context"
	"iter"
	"sync"
)

type Broker[T any] struct {
	BufferSize int
	subs       map[string]map[chan T]struct{}
	mu         sync.RWMutex
}

func NewBroker[T any](bufferSize int) *Broker[T] {
	return &Broker[T]{
		BufferSize: cmp.Or(bufferSize, 2),
		subs:       make(map[string]map[chan T]struct{}),
	}
}

func (b *Broker[T]) Sub(ctx context.Context, key string) iter.Seq[T] {
	ch := make(chan T, b.BufferSize)
	b.mu.Lock()
	if b.subs[key] == nil {
		b.subs[key] = make(map[chan T]struct{})
	}
	b.subs[key][ch] = struct{}{}
	b.mu.Unlock()
	return func(yield func(T) bool) {
		defer func() {
			b.mu.Lock()
			delete(b.subs[key], ch)
			if len(b.subs[key]) == 0 {
				delete(b.subs, key)
			}
			b.mu.Unlock()
			close(ch)
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-ch:
				if !yield(msg) {
					return
				}
			}
		}
	}
}

func (b *Broker[T]) Pub(key string, msg T) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[key] {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (b *Broker[T]) Subs() map[string]int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	counts := make(map[string]int, len(b.subs))
	for key, chs := range b.subs {
		counts[key] = len(chs)
	}
	return counts
}
