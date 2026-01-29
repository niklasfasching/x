package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"log/slog"

	"maps"
	"slices"
)

type L struct {
	Host, User, Pass string
	Lvl              slog.Level
	Attrs            []slog.Attr
	IndexedAttrs     []string
	local            slog.Handler
	*sink
}

type sink struct {
	rs []R
	sync.Mutex
}

type R struct {
	TS, Line string
	Attrs    map[string]string
}

func (l *L) Handle(ctx context.Context, r slog.Record) error {
	l.local.Handle(ctx, r)
	if p, ok := ctx.Value(ctxKey{}).(traceMeta); ok {
		r.AddAttrs(slog.String("traceID", p.TraceID))
		if p.ParentID != "" {
			r.AddAttrs(slog.String("spanID", p.ParentID))
		}
	}
	attrs, iattrs := map[string]any{}, map[string]string{}
	iattrs["lvl"] = r.Level.String()
	fn := func(a slog.Attr) bool {
		if slices.Contains(l.IndexedAttrs, a.Key) {
			iattrs[a.Key] = a.Value.String()
		} else {
			attrs[a.Key] = a.Value.Any()
		}
		return true
	}
	for _, a := range l.Attrs {
		fn(a)
	}
	r.Attrs(fn)
	line, err := json.Marshal(map[string]any{"msg": r.Message, "attr": attrs})
	if err != nil {
		return err
	}
	l.Lock()
	l.rs = append(l.rs, R{fmt.Sprint(r.Time.UnixNano()), string(line), iattrs})
	l.Unlock()
	return nil
}

func (l *L) WithAttrs(as []slog.Attr) slog.Handler {
	nl := *l
	nl.Attrs, nl.local = slices.Concat(l.Attrs, as), l.local.WithAttrs(as)
	return &nl
}

func (l *L) Enabled(ctx context.Context, lvl slog.Level) bool { return lvl >= l.Lvl }

func (l *L) WithGroup(name string) slog.Handler {
	nl := *l
	nl.local = l.local.WithGroup(name)
	return &nl
}

func (l *L) Flush(cl *http.Client) error {
	if l.Host == "" {
		return nil
	}
	l.Lock()
	rs := l.rs
	l.rs = nil
	l.Unlock()
	if len(rs) == 0 {
		return nil
	}
	m := map[string]map[string]any{}
	for _, r := range rs {
		bs, err := json.Marshal(r.Attrs)
		if err != nil {
			panic(fmt.Errorf("ops log flush: %w", err))
		}
		k := string(bs)
		if m[k] == nil {
			m[k] = map[string]any{"stream": r.Attrs, "values": [][2]string{}}
		}
		m[k]["values"] = append(m[k]["values"].([][2]string), [2]string{r.TS, r.Line})
	}
	bs, err := json.Marshal(map[string]any{"streams": slices.Collect(maps.Values(m))})
	if err != nil {
		panic(fmt.Errorf("ops log flush: %w", err))
	}
	return post(cl, l.Host+"/loki/api/v1/push",
		l.User, l.Pass, "application/json", bytes.NewReader(bs))
}
