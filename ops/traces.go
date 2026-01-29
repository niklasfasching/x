package ops

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type T struct {
	Host, User, Pass string
	Service          string
	spans            []*Span
	sync.Mutex
}

type Span struct {
	TraceID, ID, ParentID string
	Name                  string
	Start, End            time.Time
	Tags                  map[string]string
	t                     *T
}

func (t *T) Start(ctx context.Context, name string) (context.Context, *Span) {
	if t == nil {
		return ctx, nil
	}
	tID, pID, sID := "", "", hexID(8)
	if p, ok := ctx.Value(ctxKey{}).(traceMeta); ok {
		tID, pID = p.TraceID, p.ParentID
	} else {
		tID = hexID(16)
	}
	return context.WithValue(ctx, ctxKey{}, traceMeta{tID, sID}), &Span{
		TraceID: tID, ID: sID, ParentID: pID, Name: name,
		Tags: map[string]string{}, t: t, Start: time.Now(),
	}
}

func GetTraceID(ctx context.Context) string {
	if p, ok := ctx.Value(ctxKey{}).(traceMeta); ok {
		return p.TraceID
	}
	return ""
}

func (s *Span) Set(k, v string) {
	if s != nil {
		s.Tags[k] = v
	}
}

func (s *Span) Close() {
	if s == nil || s.t == nil {
		return
	}
	s.End = time.Now()
	s.t.Lock()
	s.t.spans = append(s.t.spans, s)
	s.t.Unlock()
}

func (t *T) Flush(cl *http.Client) error {
	if t.Host == "" {
		return nil
	}
	t.Lock()
	spans := t.spans
	t.spans = nil
	t.Unlock()
	if len(spans) == 0 {
		return nil
	}
	ospans := make([]map[string]any, 0, len(spans))
	for _, s := range spans {
		attrs := make([]map[string]any, 0, len(s.Tags))
		for k, v := range s.Tags {
			attrs = append(attrs, map[string]any{
				"key": k, "value": map[string]any{"stringValue": v},
			})
		}
		osp := map[string]any{
			"traceId":           s.TraceID,
			"spanId":            s.ID,
			"name":              s.Name,
			"kind":              2,
			"startTimeUnixNano": fmt.Sprint(s.Start.UnixNano()),
			"endTimeUnixNano":   fmt.Sprint(s.End.UnixNano()),
			"attributes":        attrs,
		}
		if s.ParentID != "" {
			osp["parentSpanId"] = s.ParentID
		}
		ospans = append(ospans, osp)
	}
	bs, err := json.Marshal(map[string]any{
		"resourceSpans": []map[string]any{{
			"resource": map[string]any{
				"attributes": []map[string]any{{
					"key":   "service.name",
					"value": map[string]any{"stringValue": t.Service},
				}},
			},
			"scopeSpans": []map[string]any{{
				"scope": map[string]any{"name": "ops"},
				"spans": ospans,
			}},
		}},
	})
	if err != nil {
		return fmt.Errorf("trace marshal: %w", err)
	}
	return post(cl, t.Host+"/otlp/v1/traces", t.User, t.Pass,
		"application/json", bytes.NewReader(bs))
}

func hexID(n int) string {
	bs := make([]byte, n)
	rand.Read(bs)
	return hex.EncodeToString(bs)
}
