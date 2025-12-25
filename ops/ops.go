package ops

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/sync/errgroup"
)

type O []Flusher
type Flusher interface{ Flush(*http.Client) error }

func New(fs ...Flusher) O { return fs }

func (o O) Start(d time.Duration, fs ...interface{ Flush(*http.Client) error }) {
	t, c := time.NewTicker(d), &http.Client{Timeout: d}
	for range t.C {
		if err := o.Flush(c); err != nil {
			fmt.Fprintf(os.Stderr, "ops flush: %v", err)
		}
	}
}

func (o O) Shutdown(timeout time.Duration) {
	if err := o.Flush(&http.Client{Timeout: timeout}); err != nil {
		fmt.Fprintf(os.Stderr, "ops shutdown: %v", err)
	}
}

func (o O) Flush(c *http.Client) error {
	g := &errgroup.Group{}
	for _, f := range o {
		g.Go(func() error { return f.Flush(c) })
	}
	return g.Wait()
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
