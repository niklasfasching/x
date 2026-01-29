package ops

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"net"
	"net/http"
	"runtime/metrics"
	"strings"
	"sync"
)

type M struct {
	Host, User, Pass string
	counts           map[string]int64
	gauges           map[string]float64
	active           int64
	sync.Mutex
}

type writer struct {
	http.ResponseWriter
	code int
}

var buckets = []int64{10, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 60000}

func (m *M) Gauge(k string, v float64) {
	if m == nil {
		return
	}
	m.Lock()
	if m.gauges == nil {
		m.gauges = map[string]float64{}
	}
	m.gauges[k] = v
	m.Unlock()
}

func (m *M) AddActive(n int64) {
	if m == nil {
		return
	}
	m.Lock()
	m.active += n
	m.Unlock()
}
func (m *M) Counter(k string, v int64) {
	if m == nil {
		return
	}
	m.Lock()
	if m.counts == nil {
		m.counts = map[string]int64{}
	}
	m.counts[k] += v
	m.Unlock()
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
	kvs := m.Collect()
	if len(kvs) == 0 {
		return nil
	}
	for k, v := range kvs {
		switch v := v.(type) {
		case int64:
			fmt.Fprintf(&b, "%s value=%di\n", k, v)
		case float64:
			fmt.Fprintf(&b, "%s value=%f\n", k, v)
		}
	}
	return post(cl, m.Host+"/api/v1/push/influx/write", m.User, m.Pass, "text/plain", &b)
}

func (m *M) Hist(name string, v int64, tmpl string, args ...any) {
	if m == nil {
		return
	}
	tags := fmt.Sprintf(tmpl, args...)
	m.Lock()
	defer m.Unlock()
	for _, b := range buckets {
		if v <= b {
			m.counts[fmt.Sprintf("%s_bucket,%s,le=%d", name, tags, b)]++
		}
	}
	m.counts[fmt.Sprintf("%s_bucket,%s,le=+Inf", name, tags)]++
	m.counts[fmt.Sprintf("%s_sum,%s", name, tags)] += v
	m.counts[fmt.Sprintf("%s_count,%s", name, tags)]++
}

func (m *M) Collect() map[string]any {
	if m == nil {
		return nil
	}
	m.Lock()
	kvs, a := map[string]any{}, m.active
	for k, v := range m.counts {
		kvs[k] = v
	}
	for k, v := range m.gauges {
		kvs[k] = v
	}
	m.counts = map[string]int64{}
	m.gauges = map[string]float64{}
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
	kvs["go_active_reqs"] = a
	kvs["go_goroutines"] = int64(ms[0].Value.Uint64())
	kvs["go_cpu_user_ms"] = int64(ms[1].Value.Float64() * 1000)
	kvs["go_cpu_gc_ms"] = int64(ms[2].Value.Float64() * 1000)
	kvs["go_mem_heap_bytes"] = int64(ms[3].Value.Uint64())
	kvs["go_mem_stack_bytes"] = int64(ms[4].Value.Uint64())
	kvs["go_mem_meta_bytes"] = int64(ms[5].Value.Uint64())
	kvs["go_mutex_wait_ms"] = int64(ms[6].Value.Float64() * 1000)
	return kvs
}

func (w *writer) WriteHeader(c int) {
	w.code = c
	w.ResponseWriter.WriteHeader(c)
}

func (w *writer) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *writer) Hijack() (c net.Conn, b *bufio.ReadWriter, err error) {
	return r.ResponseWriter.(http.Hijacker).Hijack()
}
