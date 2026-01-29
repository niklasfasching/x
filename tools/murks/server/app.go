package server

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	textTemplate "text/template"
	"time"

	_ "embed"

	"github.com/mattn/go-sqlite3"
	"github.com/niklasfasching/x/ops"
	"github.com/niklasfasching/x/sq"
	"github.com/niklasfasching/x/util"
	"github.com/niklasfasching/x/web/push"
	"golang.org/x/sync/errgroup"
)

type App struct {
	ID                      string `sq:"TEXT PRIMARY KEY"`
	Owner                   string `sq:"TEXT NOT NULL"`
	Slug                    string `sq:"TEXT UNIQUE NOT NULL"`
	ChatID, Name, ShortName string
	Logo, HTML, JS          string
	Schema                  []string
	Query, Exec             map[string]string
	Shortcuts               []map[string]any
	Status                  Status
	IsPublic                bool
	CreatedAt, UpdatedAt    time.Time `sq:"AUTO"`
	Users                   []string
	VAPIDKey                string
	Subscriptions           []push.Sub
	AssetDefs               map[string]AssetDef
}

type AssetDef struct {
	Prompt, Type string
}

type GeneratedAsset struct {
	ID   string `sq:"TEXT PRIMARY KEY"`
	Mime string
	Data []byte
	AssetDef
}

type User struct {
	ID    string `sq:"ID PRIMARY KEY"`
	AppID string
}

type Req struct {
	*http.Request
	API   *API
	User  User
	Level Level
	AppID string
}

type AppState struct {
	DBs map[string]AppDB
	*util.Broker[[]byte]
	*push.Server
	sync.RWMutex
}

type AppDB struct {
	Schema []string
	*sq.DB
}

type Level int
type Status int

const (
	LvlNone Level = iota
	LvlPublic
	LvlUser
	LvlOwner
)

const (
	StatusDraft Status = iota
	StatusDev
	StatusLive
)

//go:embed assets/prompt.md
var prompt string
var promptTemplate = textTemplate.Must(textTemplate.New("prompt").Parse(prompt))

func (a *API) NewPrompt(ctx context.Context, owner, id, name string) (string, string, error) {
	if id == "" {
		s := slug(name)
		id = fmt.Sprintf("%x", rand.Uint64())
		wp, err := push.New(a.Email, push.GeneratePrivateKey())
		if err != nil {
			return "", "", err
		}
		_, err = a.apps.Insert("", App{
			ID:       id,
			Slug:     s,
			Name:     name,
			Owner:    owner,
			VAPIDKey: wp.ExportKey(),
			Status:   StatusDraft,
		})
		if err != nil {
			return "", "", fmt.Errorf("app %q/%q not available: %w", id, s, err)
		}
	}
	x, err := sq.QueryOne[App](a.DB, "SELECT VAPIDKey FROM apps WHERE ID = ? AND Owner = ?",
		id, owner)
	if err != nil {
		return "", "", fmt.Errorf("app %q not found: %w", id, err)
	}
	w := &strings.Builder{}
	err = promptTemplate.Lookup("prompt").Execute(w, map[string]any{
		"Name":     "prompt",
		"ID":       id,
		"URL":      a.URL(""),
		"Token":    a.Sign(User{ID: owner, AppID: id}, 100*365*24*time.Hour),
		"VAPIDKey": push.PublicKey(x.VAPIDKey),
	})
	return id, w.String(), err
}

func (a *API) UpdateApp(x App) error {
	if strings.Contains(x.HTML, "PAT_") {
		return fmt.Errorf("app HTML must not contain PAT token")
	}
	y, err := sq.QueryOne[App](a.DB, "SELECT ID, IsPublic, Users FROM apps WHERE ID = ?", x.ID)
	if err != nil {
		return err
	}
	x.Users = slices.Compact(slices.Sorted(slices.Values(append(x.Users, y.Users...))))
	x.Status, x.IsPublic = cmp.Or(x.Status, StatusDev), y.IsPublic || x.IsPublic
	return a.apps.Update(x,
		"Status", "IsPublic", "Logo", "HTML", "JS", "ChatID", "Name",
		"ShortName", "Schema", "Query", "Exec", "Shortcuts", "AssetDefs")
}

func (a *API) GetAppDB(ctx context.Context, id string, migrations []string, isTmp bool) (*sq.DB, error) {
	ctx, span := ops.Traces.Start(ctx, "GetAppDB")
	defer span.Close()
	migrations = append([]string{sq.Schema(GeneratedAsset{})}, migrations...)
	name, key := filepath.Join(a.DataDir, id+".sqlite"), id
	if isTmp {
		name, key = ":memory:", id+":memory:"
	}
	st := a.GetAppState(id)
	st.RLock()
	if x, ok := st.DBs[key]; ok {
		if slices.Equal(x.Schema, migrations) {
			st.RUnlock()
			ops.Metrics.Counter("app_db_cache_hit,app="+id, 1)
			return x.DB, nil
		}
		delete(st.DBs, key)
		x.DB.Close()
	}
	st.RUnlock()
	ops.Metrics.Counter("app_db_cache_miss,app="+id, 1)
	ffw, uri := 1, fmt.Sprintf(
		"%s?_pragma=page_size=%d&_pragma=max_page_count=%d&_timeout=10000",
		name, 4096, a.MaxBytesDB/4096)
	app, err := sq.QueryOne[App](a.DB, "SELECT Status FROM apps WHERE ID = ?", id)
	if err != nil {
		return nil, err
	} else if app.Status < StatusLive {
		ffw = 2
	}
	db, err := sq.New(uri, migrations, func(c *sqlite3.SQLiteConn) error {
		c.SetLimit(sqlite3.SQLITE_LIMIT_LENGTH, a.MaxBytesDB/10)
		c.SetLimit(sqlite3.SQLITE_LIMIT_SQL_LENGTH, a.MaxBytesDB/10)
		c.SetLimit(sqlite3.SQLITE_LIMIT_COLUMN, 100)
		c.RegisterAuthorizer(func(op int, a1, a2, a3 string) int {
			switch op {
			case sqlite3.SQLITE_ATTACH:
				if isSelf := a1 == name; isSelf { // sq ffw auto migration
					return sqlite3.SQLITE_OK
				}
				return sqlite3.SQLITE_DENY
			case sqlite3.SQLITE_PRAGMA:
				switch a1 {
				case "journal_mode", "synchronous", "foreign_keys",
					"busy_timeout", "table_list", "table_info":
					return sqlite3.SQLITE_OK
				}
				return sqlite3.SQLITE_DENY
			case sqlite3.SQLITE_FUNCTION:
				switch strings.ToLower(a2) {
				case "load_extension", "edit", "readfile", "writefile":
					return sqlite3.SQLITE_DENY
				}
				return sqlite3.SQLITE_OK
			case
				sqlite3.SQLITE_CREATE_VTABLE,
				sqlite3.SQLITE_DROP_VTABLE,
				sqlite3.SQLITE_DETACH:
				return sqlite3.SQLITE_DENY
			default:
				return sqlite3.SQLITE_OK
			}
		})
		return nil
	}, ffw)
	if err != nil {
		return nil, err
	}
	st.Lock()
	defer st.Unlock()
	st.DBs[key] = AppDB{Schema: migrations, DB: db}
	return db, nil
}

func (a *API) GetAppState(id string) *AppState {
	a.Lock()
	defer a.Unlock()
	if a.states[id] == nil {
		a.states[id] = &AppState{
			DBs:    map[string]AppDB{},
			Broker: util.NewBroker[[]byte](16),
		}
	}
	return a.states[id]
}

func (a *API) RunAppSQL(ctx context.Context, u User, id, cmd, action string,
	args []any, lvl Level) (any, error) {
	ctx, span := ops.Traces.Start(ctx, "RunAppSQL "+cmd+" "+action)
	defer span.Close()
	start := time.Now()
	parts, requiredLvl := strings.SplitN(action, ":", 2), LvlOwner
	if len(parts) == 2 {
		v, _ := strconv.Atoi(parts[1])
		requiredLvl = cmp.Or(Level(v), requiredLvl)
	}
	if lvl < requiredLvl {
		return nil, fmt.Errorf("unauthorized %v %d < %d", u, lvl, requiredLvl)
	}
	x, err := sq.QueryOne[App](a.DB,
		"SELECT Owner, Schema, Exec, Query FROM apps WHERE ID = ?", id)
	if err != nil {
		return nil, err
	}
	db, err := a.GetAppDB(ctx, id, x.Schema, u.IsDeployToken(id))
	if err != nil {
		return nil, err
	}
	defer func() {
		ops.Metrics.Hist("app_sql_duration_ms", time.Since(start).Milliseconds(),
			"app=%s,cmd=%s,action=%s", id, cmd, action)
	}()
	switch cmd {
	case "query":
		sql, ok := x.Query[action]
		if !ok {
			return nil, fmt.Errorf("unknown query %q", action)
		}
		rows, err := sq.QueryMapContext[any](ctx, db, sql, args...)
		if err != nil {
			ops.Metrics.Counter("app_sql_errors,app="+id, 1)
			return nil, err
		} else if rows == nil {
			rows = []map[string]any{}
		}
		return rows, nil
	case "exec":
		sql, ok := x.Exec[action]
		if !ok {
			return nil, fmt.Errorf("unknown exec %q", action)
		}
		lastID, count, err := sq.ExecContext(ctx, db, sql, args...)
		if err != nil {
			ops.Metrics.Counter("app_sql_errors,app="+id, 1)
			return nil, err
		}
		return map[string]any{"id": lastID, "count": count}, nil
	}
	return nil, fmt.Errorf("unknown cmd: %q", cmd)
}

func (a App) ChatURL() string {
	if strings.HasPrefix(a.ChatID, "share/") {
		return "https://gemini.google.com/" + a.ChatID
	}
	parts := strings.Split(a.ChatID, "_")
	if len(parts) < 2 {
		return ""
	}
	return "https://gemini.google.com/app/" + parts[1]
}

func (a *API) PushSub(u User, id string, sub push.Sub) error {
	st := a.GetAppState(id)
	st.Lock()
	defer st.Unlock()
	x, err := sq.QueryOne[App](a.DB, "SELECT ID, Subscriptions FROM apps WHERE ID = ?", id)
	if err != nil {
		return err
	}
	sub.UserID = u.ID
	isSubscribed := slices.ContainsFunc(x.Subscriptions, func(s push.Sub) bool {
		return s.UserID == sub.UserID && s.Endpoint == sub.Endpoint
	})
	if isSubscribed {
		return nil
	}
	x.Subscriptions = append(x.Subscriptions, sub)
	return a.apps.Update(x, "Subscriptions")
}

func (a *API) PushEmit(ctx context.Context, userID, appID string, users []string, msg []byte) error {
	ctx, span := ops.Traces.Start(ctx, "PushEmit")
	defer span.Close()
	start := time.Now()
	wp, err := a.GetAppPush(appID)
	if err != nil {
		return err
	}
	x, err := sq.QueryOne[App](a.DB, "SELECT Subscriptions FROM apps WHERE ID = ?", appID)
	if err != nil {
		return err
	}
	g := errgroup.Group{}
	slog.InfoContext(ctx, "PushEmit", "user", userID, "subs", len(x.Subscriptions))
	for _, sub := range x.Subscriptions {
		if len(users) != 0 && !slices.Contains(users, sub.UserID) {
			continue
		} else if len(users) == 0 && userID == sub.UserID && !a.IsDev() {
			continue
		}
		g.Go(func() error {
			err := wp.Send(sub, msg)
			if err != nil && (strings.Contains(err.Error(), "410") || strings.Contains(err.Error(), "401")) {
				ops.Metrics.Counter("push_notification_errors,app="+appID, 1)
				q := `UPDATE apps SET Subscriptions = (
					SELECT json_group_array(json(value)) FROM json_each(Subscriptions)
					WHERE json_extract(value, '$.endpoint') != ?
				) WHERE ID = ?`
				if _, _, err := sq.Exec(a.DB, q, sub.Endpoint, appID); err != nil {
					return err
				}
				return nil
			} else {
				slog.DebugContext(ctx, "PushEmit Message", "sender", userID, "app", appID, "receiver", sub.UserID,
					"sub", sub.Endpoint)
			}
			ops.Metrics.Counter("push_notification_sent,app="+appID, 1)
			return err
		})
	}
	defer func() {
		ops.Metrics.Hist("push_notification_duration_ms", time.Since(start).Milliseconds(), "app=%s", appID)
	}()
	return g.Wait()
}

func (a *API) GetAppPush(id string) (*push.Server, error) {
	st := a.GetAppState(id)
	st.RLock()
	if st.Server != nil {
		st.RUnlock()
		return st.Server, nil
	}
	st.RUnlock()
	x, err := sq.QueryOne[App](a.DB, "SELECT ID, VAPIDKey FROM apps WHERE ID = ?", id)
	if err != nil {
		return nil, err
	} else if x.VAPIDKey == "" {
		return nil, fmt.Errorf("exp")
	}
	wp, err := push.New(a.Email, x.VAPIDKey)
	if err != nil {
		return nil, err
	}
	st.Lock()
	defer st.Unlock()
	st.Server = wp
	return wp, nil
}

func (a *API) Req(r *http.Request) *Req {
	u, _ := a.Subject(r)
	appID := r.PathValue("id")
	return &Req{r, a, u, a.getLevel(u, appID), appID}
}

func (req *Req) AuthError(required Level, reason string) error {
	auth := ""
	if req.Header.Get("X-Token") != "" {
		auth += "header "
	}
	if req.URL.Query().Get("token") != "" {
		auth += "query "
	}
	if cookie, _ := req.Cookie("token"); cookie != nil {
		auth += "cookie"
	}
	return fmt.Errorf("unauthorized: %s %v => %s (need lvl %d, have %d, auth: %s)",
		reason, req.User, req.AppID, required, req.Level, auth)
}

func (a *API) getLevel(u User, appID string) Level {
	x, err := sq.QueryOne[App](a.DB, "SELECT Owner, IsPublic, Users FROM apps WHERE ID = ?", appID)
	if err != nil {
		return LvlNone
	}
	if u.ID == x.Owner {
		return LvlOwner
	}
	if slices.Contains(x.Users, u.ID) {
		return LvlUser
	}
	if x.IsPublic {
		return LvlPublic
	}
	return LvlNone
}

func (r *Req) App(fields string) (App, error) {
	return sq.QueryOne[App](r.API.DB, fmt.Sprintf("SELECT %s FROM apps WHERE ID = ?", fields), r.AppID)
}

func (r *Req) Decode(v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func (r *Req) State() *AppState {
	return r.API.GetAppState(r.AppID)
}

func (r *Req) Track(name string) {
	ops.Metrics.Counter(name+",app="+r.AppID, 1)
}

func (u User) IsDeployToken(appID string) bool {
	return u.AppID != "" && u.AppID == appID
}
