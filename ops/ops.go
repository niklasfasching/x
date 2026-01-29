package ops

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"log/slog"

	"golang.org/x/sync/errgroup"
)

type O []Flusher
type Flusher interface{ Flush(*http.Client) error }

type ctxKey struct{}
type traceMeta struct{ TraceID, ParentID string }

var Metrics *M
var Traces *T

func New(fs ...Flusher) O { return fs }

func (o O) Start(d time.Duration) {
	t, c := time.NewTicker(d), &http.Client{Timeout: d}
	for range t.C {
		if err := o.Flush(c); err != nil {
			fmt.Fprintf(os.Stderr, "ops flush: %v\n", err)
		}
	}
}

func (o O) Shutdown(timeout time.Duration) {
	if err := o.Flush(&http.Client{Timeout: timeout}); err != nil {
		fmt.Fprintf(os.Stderr, "ops shutdown: %v\n", err)
	}
}

func (o O) Flush(c *http.Client) error {
	g := &errgroup.Group{}
	for _, f := range o {
		g.Go(func() error { return f.Flush(c) })
	}
	return g.Wait()
}

func Auto(name string, d time.Duration) O {
	o, lvl := New(), slog.LevelDebug
	lvl.UnmarshalText([]byte(os.Getenv("LogLevel")))
	slog.SetLogLoggerLevel(lvl)
	influxURI, lokiURI := os.Getenv("InfluxURI"), os.Getenv("LokiURI")
	tempoURI := os.Getenv("TempoURI")
	if u, err := url.Parse(influxURI); err == nil && influxURI != "" {
		user, token := u.Query().Get("user"), u.Query().Get("token")
		slog.Debug("ops: Setup metrics", "host", u.Host, "user", user)
		Metrics = &M{Host: "https://" + u.Host, User: user, Pass: token}
		o = append(o, Metrics)
	}
	if u, err := url.Parse(lokiURI); err == nil && lokiURI != "" {
		user, token := u.Query().Get("user"), u.Query().Get("token")
		slog.Debug("ops: Setup logs", "host", u.Host, "user", user)
		l := &L{Host: "https://" + u.Host, User: user, Pass: token,
			Lvl:          lvl,
			IndexedAttrs: []string{"service"},
			Attrs:        []slog.Attr{slog.String("service", name)},
			local:        slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}),
			sink:         &sink{},
		}
		o = append(o, l)
		slog.SetDefault(slog.New(l))
	}
	if u, err := url.Parse(tempoURI); err == nil && tempoURI != "" {
		user, token := u.Query().Get("user"), u.Query().Get("token")
		slog.Debug("ops: Setup traces", "host", u.Host, "user", user)
		Traces = &T{Host: "https://" + u.Host, User: user, Pass: token, Service: name}
		o = append(o, Traces)
	}
	go o.Start(d)
	return o
}

func WithOps(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := Traces.Start(r.Context(), r.Method+" "+r.URL.Path)
		span.Set("http.method", r.Method)
		span.Set("http.path", r.URL.Path)
		span.Set("http.host", r.Host)
		r = r.WithContext(ctx)
		Metrics.AddActive(1)
		t, ww := time.Now(), &writer{ResponseWriter: w, code: 200}
		defer func() {
			d := time.Since(t).Milliseconds()
			Metrics.AddActive(-1)
			_, p := mux.Handler(r)
			tags := fmt.Sprintf("path=%s,method=%s,code=%d",
				Metrics.Esc(p, "404"), r.Method, ww.code)
			Metrics.Counter("http_req_total,"+tags, 1)
			Metrics.Hist("http_dur", d, "%s", tags)
			span.Set("http.status_code", fmt.Sprint(ww.code))
			if ww.code >= 500 {
				span.Set("error", "true")
			}
			span.Close()
		}()
		mux.ServeHTTP(ww, r)
	})
}

func post(cl *http.Client, url, user, pass, contentType string, body io.Reader) error {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", contentType)
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("flush: %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		bs, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("flush: %s %d %q", url, resp.StatusCode, string(bs))
	}
	return nil
}
