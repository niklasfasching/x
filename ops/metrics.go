package ops

import (
	"bytes"
	"cmp"
	"fmt"
	"net/http"
	"runtime/metrics"
	"strings"
	"sync"
	"time"
)

type M struct {
	Host, User, Pass string
	counts           map[string]int64
	active           int64
	sync.Mutex
}

type writer struct {
	http.ResponseWriter
	code int
}

var buckets = []int64{10, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 30000, 60000}

func (m *M) WithMetrics(mux *http.ServeMux) http.Handler {
	m.counts = map[string]int64{}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		m.Lock()
		m.active++
		m.Unlock()
		defer func() {
			m.Lock()
			m.active--
			m.Unlock()
		}()
		t, ww := time.Now(), &writer{w, 200}
		mux.ServeHTTP(ww, req)
		d := time.Since(t).Milliseconds()
		_, p := mux.Handler(req)
		m.Track(d, "path=%s,method=%s,code=%d", m.Esc(p, "404"), req.Method, ww.code)
	})
}

func (m *M) Esc(s, f string) string {
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "=", "\\=")
	s = strings.ReplaceAll(s, " ", "\\ ")
	return cmp.Or(s, f)
}

func (m *M) Flush(cl *http.Client) error {
	if m.Host == "" {
		return nil
	}
	b := bytes.Buffer{}
	cs := m.Collect()
	if len(cs) == 0 {
		return nil
	}
	for k, v := range cs {
		fmt.Fprintf(&b, "%s value=%di\n", k, v)
	}
	return post(cl, m.Host+"/api/v1/push/influx/write", m.User, m.Pass, "text/plain", &b)
}

func (m *M) Track(ms int64, tmpl string, args ...any) {
	tags := fmt.Sprintf(tmpl, args...)
	m.Lock()
	defer m.Unlock()
	m.counts[fmt.Sprintf("http_req_total,%s", tags)]++
	for _, b := range buckets {
		if ms <= b {
			m.counts[fmt.Sprintf("http_dur_bucket,%s,le=%d", tags, b)]++
		}
	}
	m.counts[fmt.Sprintf("http_dur_bucket,%s,le=+Inf", tags)]++
	m.counts[fmt.Sprintf("http_dur_sum,%s", tags)] += ms
	m.counts[fmt.Sprintf("http_dur_count,%s", tags)]++
}

func (m *M) Collect() map[string]int64 {
	m.Lock()
	cs, a := m.counts, m.active
	if cs == nil {
		cs = map[string]int64{}
	}
	m.counts = map[string]int64{}
	m.Unlock()
	ms := []metrics.Sample{
		{Name: "/sched/goroutines:goroutines"},
		{Name: "/cpu/classes/user:cpu-seconds"},
		{Name: "/cpu/classes/gc/total:cpu-seconds"},
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/heap/stacks:bytes"},
		{Name: "/memory/classes/metadata/other:bytes"},
		{Name: "/sync/mutex/wait/total:seconds"},
	}
	metrics.Read(ms)
	cs["go_active_reqs"] = a
	cs["go_goroutines"] = int64(ms[0].Value.Uint64())
	cs["go_cpu_user_ms"] = int64(ms[1].Value.Float64() * 1000)
	cs["go_cpu_gc_ms"] = int64(ms[2].Value.Float64() * 1000)
	cs["go_mem_heap_bytes"] = int64(ms[3].Value.Uint64())
	cs["go_mem_stack_bytes"] = int64(ms[4].Value.Uint64())
	cs["go_mem_meta_bytes"] = int64(ms[5].Value.Uint64())
	cs["go_mutex_wait_ms"] = int64(ms[6].Value.Float64() * 1000)
	return cs
}

func (w *writer) WriteHeader(c int) {
	w.code = c
	w.ResponseWriter.WriteHeader(c)
}
