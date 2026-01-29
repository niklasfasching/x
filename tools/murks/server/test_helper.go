package server

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"log/slog"

	"github.com/niklasfasching/x/headless"
	"github.com/niklasfasching/x/snap"
	"github.com/niklasfasching/x/sq"
)

type H struct {
	*API
	*testing.T
	*headless.H
	*headless.Session
	Timeout time.Duration
}

func NewHarness(t *testing.T, isIntegrationTest bool) H {
	logLvl := slog.LevelInfo
	logLvl.UnmarshalText([]byte(os.Getenv("LogLevel")))
	slog.SetLogLoggerLevel(logLvl)

	c := DefaultConfig
	c.AppSecret = "test"
	c.DataDir = t.TempDir()
	timeout := 5 * time.Second
	a, err := New(c)
	if err != nil {
		t.Fatalf("new %v", err)
	} else if isIntegrationTest {
		if os.Getenv("HEADLESS") == "" {
			t.Skip("set HEADLESS=1 to run integration tests")
		}
		isHeadlessDebug := os.Getenv("HEADLESS") == "debug"
		if isHeadlessDebug {
			timeout = 5 * time.Minute
		}
		srv := httptest.NewServer(a.Handler())
		t.Cleanup(func() { srv.Close() })
		_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
		a.Address = ":" + port
		h, err := headless.Start(map[string]any{
			"--headless": !isHeadlessDebug,
		})
		if err != nil {
			t.Fatalf("headless: %v", err)
		}
		t.Cleanup(func() { h.Stop() })
		return H{a, t, h, nil, timeout}
	}
	return H{a, t, nil, nil, timeout}
}

func (h H) CreateApp(userID string, x App) App {
	appID, _, err := h.NewPrompt(h.Context(), userID, "", cmp.Or(x.Name, "app"))
	if err != nil {
		h.Fatalf("failed to create prompt (and init app): %v", err)
	}
	if x.ID != "" {
		if _, _, err := sq.Exec(h.DB, "UPDATE apps SET ID = ? WHERE ID = ?", x.ID, appID); err != nil {
			h.Fatalf("failed to update id: %v", err)
		}
		appID = x.ID
	}
	x.ID, x.Owner = appID, userID
	if err := h.apps.Update(x, "Owner", "Users"); err != nil {
		h.Fatalf("failed to update app: %v", err)
	} else if err := h.UpdateApp(x); err != nil {
		h.Fatalf("failed to update app: %v", err)
	}
	x, err = sq.QueryOne[App](h.DB, "SELECT * FROM apps WHERE ID = ?", x.ID)
	if err != nil {
		h.Fatalf("failed to query app: %v", err)
	}
	return x
}

func (h H) DeployApp(userID string, x App) App {
	appID, _, err := h.NewPrompt(h.Context(), userID, x.ID, cmp.Or(x.Name, "app"))
	if err != nil {
		h.Fatalf("failed to create prompt (and init app): %v", err)
	}
	deployToken := h.Auth.Sign(User{ID: userID, AppID: appID}, time.Hour)
	bs, err := json.MarshalIndent(x, "", "  ")
	if err != nil {
		h.Fatalf("failed to marshal API config: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `
          <!DOCTYPE html>
          <html>
            <head><meta charset="utf-8"></head>
            <body>
              <main>
                <script type="module">
                import { API } from "%s/assets/api.mjs";
                window.api = new API(%q, %s)
                </script>
              </main>
            </body>
            </html>`, h.URL(""), deployToken, string(bs))
	}))
	h.IsDeployOrigin = func(origin string) bool { return origin == srv.URL }
	h.Cleanup(func() { srv.Close() })

	h.Open(h.T, srv.URL)
	h.Wait(`await window.api?.ready`, 5)
	x, err = sq.QueryOne[App](h.DB, "SELECT * FROM apps WHERE ID = ?", appID)
	if err != nil {
		h.Fatalf("failed to query app: %v", err)
	}
	return x
}

func (h *H) Open(t *testing.T, url string) {
	s, err := h.H.Open(url, func(s *headless.Session) error {
		s.Handle("Runtime.consoleAPICalled", func(p struct{ Args []struct{ Value any } }) {
			h.Log("console:", p.Args)
		})
		return nil
	})
	if err != nil {
		h.Fatal(err)
	}
	h.Session = s
	h.Cleanup(func() { s.Close() })
	snap := snap.New(h.T)
	// NOTE: binding with return value because only bindings
	// with a return value return promises and thus can be awaited
	h.Bind("snap", func(k string, v any) string {
		snap.KeyedSnap(t, k, v)
		return k
	})
	h.Bind("assert", func(ok bool, msg ...any) {
		if !ok {
			t.Error(msg...)
		}
	})
	js := `window.$ = (s) => document.querySelector(s);
		   window.$$ = (s) => [...document.querySelectorAll(s)];`
	h.Eval(js)
	h.Exec("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": js}, nil)
}

func (h *H) Login(t *testing.T, user string) {
	h.Open(t, h.LoginURL(user))
	h.Wait(`!!document.querySelector("button[title='Enter']")`, 5)
	h.Eval(`document.querySelector("button[title='Enter']").click()`)
	h.Wait(`location.pathname === "/"`, 5)
}

func (h *H) OpenApp(t *testing.T, app App, user string) {
	if user != "" {
		h.Login(t, user)
	}
	h.Open(t, h.URL(app.Slug))
	h.Wait(`await window.api?.ready`, 5)
}

func (h *H) Eval(js string) any {
	h.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), h.Timeout)
	defer cancel()
	done, v := make(chan error, 1), new(any)
	go func() { done <- h.Session.Eval(js, v) }()
	select {
	case err := <-done:
		if err != nil {
			h.Fatalf("eval err: %v", err)
		}
	case <-ctx.Done():
		h.Fatalf("eval timeout: %s", js)
	}
	return *v
}

func (h *H) Exec(method string, params, v any) {
	if err := h.Session.Exec(method, params, v); err != nil {
		h.Errorf("exec %q %v: %v", method, params, err)
	}
}

func (h *H) Wait(js string, seconds int) any {
	slog.Debug("Waiting for", "js", js)
	h.Helper()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for time.Now().Before(deadline) {
		v := *new(any)
		err := h.Session.Eval(js, &v)
		if err == nil && v != nil && !reflect.ValueOf(v).IsZero() {
			return v
		}
		time.Sleep(100 * time.Millisecond)
	}
	body := h.Eval("document.body.innerHTML").(string)
	h.Fatalf("timeout waiting for %q: %s", js, body)
	return nil
}

func (h *H) MockPrompt(value string) {
	js := fmt.Sprintf(`
      window.ogPrompt = window.prompt;
      window.prompt = () => {
        setTimeout(() => window.prompt = window.ogPrompt);
        return "%s";
      }`, value)
	h.Exec("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": js}, nil)
	h.Eval(js)
}

func (h *H) MockConfirm(value bool) {
	js := fmt.Sprintf(`
      window.ogConfirm = window.confirm;
      window.confirm = () => {
        setTimeout(() => window.confirm = window.ogConfirm);
        return %v;
      }`, value)
	h.Exec("Page.addScriptToEvaluateOnNewDocument",
		map[string]any{"source": js}, nil)
	h.Eval(js)
}
