package server

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/niklasfasching/x/git"
	"github.com/niklasfasching/x/ops"
	"github.com/niklasfasching/x/sq"
	"github.com/niklasfasching/x/util"
	"github.com/niklasfasching/x/web/htmpl"
)

type Cron struct {
	ID                    int
	AppID, UserID         string
	Schedule, Title, Body string
	Count                 int
	NextRun               int64
	CreatedAt, UpdatedAt  time.Time `sq:"AUTO"`
}

var slugRe = regexp.MustCompile(`[^-a-z0-9]+`)

func (a *API) RunCron(ctx context.Context) error {
	ctx, span := ops.Traces.Start(ctx, "RunCron")
	defer span.Close()
	now := time.Now().Unix()
	jobs, err := sq.Query[Cron](a.DB, "SELECT * FROM crons WHERE NextRun < ?", now)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		start := time.Now()
		msg, _ := json.Marshal(map[string]string{"title": j.Title, "body": j.Body})
		if err := a.PushEmit(ctx, j.UserID, j.AppID, []string{j.UserID}, msg); err != nil {
			slog.ErrorContext(ctx, "cron push", "err", err.Error())
			ops.Metrics.Counter("cron_errors,app="+j.AppID, 1)
		}
		if j.Count > 0 {
			j.Count--
		}
		if j.Count == 0 {
			sq.Exec(a.DB, "DELETE FROM crons WHERE ID = ?", j.ID)
		} else if j.NextRun = nextRun(j.Schedule, time.Unix(j.NextRun, 0)).Unix(); j.NextRun == 0 {
			slog.ErrorContext(ctx, "cron: invalid schedule, deleting", "id", j.ID, "schedule", j.Schedule)
			sq.Exec(a.DB, "DELETE FROM crons WHERE ID = ?", j.ID)
		} else {
			a.crons.Update(j, "NextRun", "Count")
		}
		ops.Metrics.Hist("cron_duration_ms", time.Since(start).Milliseconds(), "app=%s", j.AppID)
		ops.Metrics.Counter("cron_success,app="+j.AppID, 1)
	}
	return nil
}

func (a *API) ErrHandler(f func(http.ResponseWriter, *http.Request) (int, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code, err := f(w, r); err != nil {
			slog.ErrorContext(r.Context(), "err", "url", r.URL.String(), "code", code, "err", err.Error())
			if code == 401 {
				http.Redirect(w, r, a.URL(""), http.StatusTemporaryRedirect)
			} else {
				http.Error(w, err.Error(), code)
			}
		}
	})
}

func (a *API) Collect(ctx context.Context) error {
	_, span := ops.Traces.Start(ctx, "Collect")
	defer span.Close()
	if ops.Metrics == nil {
		return nil
	}
	a.RLock()
	totalSubs, totalDBs := 0, 0
	for id, st := range a.states {
		st.RLock()
		totalDBs += len(st.DBs)
		appSubs := 0
		for _, count := range st.Broker.Subs() {
			appSubs += count
		}
		st.RUnlock()
		ops.Metrics.Gauge("sse_active,app="+id, float64(appSubs))
		totalSubs += appSubs
	}
	a.RUnlock()
	ops.Metrics.Gauge("sse_clients", float64(totalSubs))
	ops.Metrics.Gauge("dbs_cached", float64(totalDBs))
	s := a.DB.Stats()
	ops.Metrics.Gauge("db_open", float64(s.OpenConnections))
	ops.Metrics.Gauge("db_use", float64(s.InUse))
	size := int64(0)
	if fs, err := os.ReadDir(a.DataDir); err == nil {
		for _, f := range fs {
			if i, _ := f.Info(); i != nil {
				s, name := i.Size(), f.Name()
				size += s
				if strings.HasSuffix(name, ".sqlite") && !strings.HasSuffix(name, ".dev.sqlite") {
					app := strings.TrimSuffix(name, ".sqlite")
					ops.Metrics.Gauge("app_bytes,app="+app, float64(s))
				}
			}
		}
	}
	ops.Metrics.Gauge("data_bytes", float64(size))
	users, apps := 0, 0
	if err := a.DB.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM users),
			(SELECT COUNT(*) FROM apps),
	`).Scan(&users, &apps); err == nil {
		ops.Metrics.Gauge("users", float64(users))
		ops.Metrics.Gauge("apps", float64(apps))
	}
	return nil
}

func (a *API) Backup(ctx context.Context) error {
	ctx, span := ops.Traces.Start(ctx, "Backup")
	defer span.Close()
	start := time.Now()
	defer func() { slog.InfoContext(ctx, "backup: finished", "duration", time.Since(start)) }()
	return git.PushGitHub(a.BackupRepo, "main", []byte(a.BackupKey), func(c *git.Commit) error {
		fs, _ := filepath.Glob(filepath.Join(a.DataDir, "*.sqlite"))
		for _, f := range fs {
			if strings.HasSuffix(f, ".dev.sqlite") {
				continue
			}
			app := strings.TrimSuffix(filepath.Base(f), ".sqlite")
			db, err := sql.Open("sqlite3", f)
			if err != nil {
				return err
			}
			defer db.Close()
			tables, err := sq.Tables(db, false)
			if err != nil {
				return err
			}
			for table := range tables {
				rows, err := sq.QueryMap[any](db, fmt.Sprintf("SELECT * FROM `%s`", table))
				if err != nil {
					return err
				}
				bs, err := json.MarshalIndent(rows, "", "  ")
				if err != nil {
					return err
				}
				c.Add(filepath.Join(app, table+".json"), bs)
			}
		}
		xs, err := sq.Query[App](a.DB, "SELECT * FROM apps")
		if err != nil {
			return err
		}
		for _, x := range xs {
			bs, err := json.MarshalIndent(x, "", "  ")
			if err != nil {
				return err
			}
			c.Add(filepath.Join(x.ID, "_.json"), bs)
		}
		return nil
	})
}

func (a *API) IsDev() bool                { return a.Domain == DevDomain }
func (a *API) IsAdmin(userID string) bool { return slices.Contains(a.TelegramAdminUsers, userID) }

func (a *API) LoginURL(userID string) string {
	code := a.SignClaim(map[string]any{"op": "login", "user": userID}, a.TokenTTL)
	return fmt.Sprintf("%s/login?code=%s", a.URL(""), code)
}

func (a *API) URL(appSlug string) string {
	host, proto := a.Domain, "https"
	if a.IsDev() {
		host, proto = fmt.Sprintf("%s%s", a.Domain, a.Address), "http"
	}
	if appSlug != "" {
		return fmt.Sprintf("%s://%s.%s", proto, appSlug, host)
	}
	return fmt.Sprintf("%s://%s", proto, host)
}

func (a *API) DevLoginCode() string {
	if a.IsDev() {
		return a.SignClaim(map[string]any{"op": "login", "user": "dev"}, time.Hour)
	}
	panic("must not be called outside dev")
}

func (a *API) Users() []string {
	users, err := sq.Query[string](a.DB, "SELECT ID FROM users")
	if err != nil {
		panic(err)
	}
	return append(a.TelegramAdminUsers, users...)
}

func (a *API) Lookup(name string) *htmpl.Template {
	a.RLock()
	defer a.RUnlock()
	return a.Template.Lookup(name)
}

func (a *API) Render(ctx context.Context, w io.Writer, name string, data any) error {
	ctx, span := ops.Traces.Start(ctx, "render "+name)
	defer span.Close()
	return a.Lookup(name).Execute(w, data)
}

func Fetch(ctx context.Context, method, url, body string,
	headers map[string]string) (int, map[string]string, string, error) {
	for attempt := 1; attempt <= 3; attempt++ {
		status, resHeaders, resBody, err := fetchOnce(ctx, method, url, body, headers)
		if err == nil {
			return status, resHeaders, resBody, nil
		}
		if attempt == 3 {
			return status, resHeaders, resBody, err
		}
		time.Sleep(time.Duration(attempt) * time.Second)
	}
	panic("unreachable")
}

func fetchOnce(ctx context.Context, method, url, body string,
	headers map[string]string) (int, map[string]string, string, error) {
	publicClient := &http.Client{
		Transport: &http.Transport{DialContext: util.NewPublicDialer(&net.Dialer{})},
	}
	req, err := http.NewRequestWithContext(ctx, cmp.Or(method, "GET"), url,
		strings.NewReader(body))
	if err != nil {
		return 0, nil, "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := publicClient.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()
	bs, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, "", err
	}
	resHeaders := make(map[string]string)
	for k, v := range resp.Header {
		resHeaders[k] = strings.Join(v, ", ")
	}
	return resp.StatusCode, resHeaders, string(bs), nil
}

func nextRun(schedule string, from time.Time) time.Time {
	if t, err := time.Parse("15:04", schedule); err == nil {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.Local)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		return next.UTC()
	}
	if d, err := time.ParseDuration(schedule); err == nil {
		return from.Add(d).UTC()
	}
	return time.Time{}
}

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}
