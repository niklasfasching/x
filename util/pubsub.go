package util

import (
	"context"
	"slices"
	"sync"
)

type PubSub[K comparable, V any] struct {
	subs map[K][]chan V
	sync.Mutex
}

func (p *PubSub[K, V]) Publish(k K, v V) {
	p.Lock()
	if p.subs == nil {
		p.subs = map[K][]chan V{}
	}
	cs := p.subs[k]
	p.Unlock()
	for _, c := range cs {
		select {
		case c <- v:
		default:
		}
	}
}

func (p *PubSub[K, V]) Subscribe(ctx context.Context, k K, f func(V)) {
	ch := make(chan V)
	p.Lock()
	if p.subs == nil {
		p.subs = map[K][]chan V{}
	}
	p.subs[k] = append(p.subs[k], ch)
	p.Unlock()
	defer func() {
		p.Lock()
		p.subs[k] = slices.DeleteFunc(p.subs[k], func(ch2 chan V) bool { return ch == ch2 })
		p.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case v := <-ch:
			f(v)
		}
	}
}
