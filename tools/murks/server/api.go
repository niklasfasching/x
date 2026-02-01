package server

import (
	"cmp"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/niklasfasching/x/ops"
	"github.com/niklasfasching/x/sq"
	"github.com/niklasfasching/x/telegram"
	"github.com/niklasfasching/x/util"
	"github.com/niklasfasching/x/web"
	"github.com/niklasfasching/x/web/htmpl"
	"golang.org/x/net/websocket"
	"golang.org/x/sync/errgroup"
)

type API struct {
	Config
	*sq.DB
	*telegram.T
	*htmpl.Template
	*web.Auth[User]
	apps   *sq.Table[App]
	users  *sq.Table[User]
	crons  *sq.Table[Cron]
	states map[string]*AppState
	sync.RWMutex
}

type Config struct {
	Address, Domain, Email      string
	AppSecret, TelegramBotToken string
	TelegramBotName             string
	BackupRepo, BackupKey       string
	DeployRepo, DeployKey       string
	BackupFreq, TokenTTL        time.Duration
	DataDir, DBName             string
	TelegramAdminUsers          []string
	MaxBytesDB                  int
	IsDeployOrigin              func(origin string) bool
}

//go:embed assets/*
var assets embed.FS

// NOTE: The Domain field of cookies must contain a dot, so we can't use
// bare localhost (necessary for sharing credentials with subdomains)
var DevDomain = "murks.localhost"
var DefaultConfig = Config{
	Address:            ":9002",
	Domain:             DevDomain,
	Email:              "admin@" + DevDomain,
	BackupFreq:         time.Hour,
	TokenTTL:           24 * time.Hour,
	DataDir:            "data",
	DBName:             "db.sqlite",
	TelegramAdminUsers: []string{},
	MaxBytesDB:         1024 * 1024 * 100,
	IsDeployOrigin: func(origin string) bool {
		return strings.HasSuffix(origin, ".goog")
	},
}

func New(c Config) (*API, error) {
	if err := util.LoadConfig(&c, true); err != nil {
		return nil, err
	} else if err := os.MkdirAll(c.DataDir, 0755); err != nil {
		return nil, err
	}
	uri := filepath.Join(c.DataDir, c.DBName) + "?_journal=WAL&_timeout=10000"
	db, err := sq.New(uri, []string{
		sq.Schema(App{}),
		sq.Schema(User{}),
		sq.Schema(Cron{}),
	}, nil, 1)
	if err != nil {
		return nil, err
	}
	a := &API{
		Config: c,
		DB:     db,
		Auth:   &web.Auth[User]{Secret: c.AppSecret},
		apps:   sq.NewTable[App](db, "apps", "ID"),
		users:  sq.NewTable[User](db, "users", "ID"),
		crons:  sq.NewTable[Cron](db, "crons", "ID"),
		T:      &telegram.T{Token: c.TelegramBotToken},
		states: map[string]*AppState{},
	}
	sub, _ := fs.Sub(assets, "assets")
	t := template.New("").Option("missingkey=error").Funcs(htmpl.DefaultFuncs).Funcs(web.Funcs)
	t.Funcs(template.FuncMap{"api": func() any { return a }})
	if _, err := t.ParseFS(sub, "*"); err != nil {
		return nil, err
	}
	ht, err := htmpl.NewCompiler(htmpl.ProcessDirectives).Compile(t)
	if err != nil {
		return nil, err
	}
	a.Template = ht
	return a, nil
}

func Start(c Config) error {
	a, err := New(c)
	if err != nil {
		return err
	}
	if a.IsDev() {
		x, qErr := sq.QueryOne[App](a.DB, "SELECT ID FROM apps WHERE Slug = 'debug'")
		id, _, _, pErr := a.NewPrompt(context.Background(), "dev", x.ID, "debug")
		if err := errors.Join(qErr, pErr); err != nil {
			return err
		}
		slog.Debug(fmt.Sprintf(`
          import { API } from "%s/assets/api.mjs";
          const api = new API(%q, {
        `, a.URL(""), a.Sign(User{"dev", id}, time.Hour)))
	}
	defer a.DB.Close()
	g, ctx := errgroup.WithContext(context.Background())
	g.Go(func() error {
		if a.TelegramBotToken == "" {
			return nil
		}
		slog.InfoContext(ctx, "bot: listening...")
		return a.T.Start(ctx, func(msg telegram.Message) {
			if txt, err := a.HandleMsg(ctx, msg); err != nil {
				slog.ErrorContext(ctx, "telegram", "err", err.Error())
			} else if txt != "" {
				if err := a.T.SendMessage(msg.Chat.ID, "text", txt); err != nil {
					slog.InfoContext(ctx, "telegram reply", "err", err.Error())
				}
			}
		})
	})
	g.Go(func() error {
		if a.BackupRepo == "" || a.BackupKey == "" {
			return nil
		}
		slog.InfoContext(ctx, "backup: waiting", "d", a.BackupFreq)
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-time.Tick(a.BackupFreq):
				if err := a.Backup(ctx); err != nil {
					return err
				}
			}
		}
	})
	g.Go(func() error {
		slog.InfoContext(ctx, "http: listening...", "url", a.URL(""))
		s := http.Server{Addr: a.Address, Handler: a.Handler()}
		g.Go(func() error {
			<-ctx.Done()
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return s.Shutdown(closeCtx)
		})
		return s.ListenAndServe()
	})
	g.Go(func() error {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				if err := a.RunCron(ctx); err != nil {
					slog.ErrorContext(ctx, "cron", "err", err.Error())
				}
			}
		}
	})
	g.Go(func() error {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
				if err := a.Collect(ctx); err != nil {
					slog.ErrorContext(ctx, "metrics", "err", err.Error())
				}
			}
		}
	})
	return g.Wait()
}

func (a *API) Handler() http.Handler {
	root, app := http.NewServeMux(), http.NewServeMux()
	root.Handle("GET /{$}", a.ErrHandler(a.HandleIndex))
	root.Handle("/login", a.ErrHandler(a.ClaimHandler))
	root.Handle("/invite", a.ErrHandler(a.ClaimHandler))
	root.Handle("GET /assets/{name}", a.ErrHandler(a.HandleRootAssets))
	root.Handle("POST /api/{cmd}", web.JSONHandler(a.HandleAPI))
	app.Handle("GET /assets/{name}", a.ErrHandler(a.HandleAssets))
	app.Handle("GET /api/events", web.JSONHandler(a.HandleSSE))
	app.Handle("GET /api/ws", websocket.Handler(a.HandleWS))
	app.Handle("GET /{path...}", a.ErrHandler(a.HandleAppHTML))
	app.Handle("POST /api/events", web.JSONHandler(a.HandleSSE))
	app.Handle("POST /api/{cmd}", web.JSONHandler(a.HandleAppAPI))
	app.Handle("POST /api/{cmd}/{action...}", web.JSONHandler(a.HandleAppAPI))
	rootHandler, appHandler := ops.WithOps(root), ops.WithOps(app)
	h := a.WithAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := ops.Traces.Start(r.Context(), "route")
		defer span.Close()
		r = r.WithContext(ctx)
		host, _, _ := net.SplitHostPort(r.Host)
		subDomainSlug, _, isSubDomain := strings.Cut(cmp.Or(host, r.Host), "."+a.Domain)
		u, _ := a.Subject(r)
		subjectAppID, isScopedToken := u.AppID, u.AppID != ""
		appID := subjectAppID
		if isSubDomain {
			x, err := sq.QueryOne[App](a.DB, "SELECT ID FROM apps WHERE Slug = ?",
				subDomainSlug)
			if err != nil {
				http.Error(w, "app not found", 404)
				return
			}
			appID = x.ID
		}
		if isSubDomain || isScopedToken {
			r.SetPathValue("id", appID)
			appHandler.ServeHTTP(w, r)
		} else {
			rootHandler.ServeHTTP(w, r)
		}
	}), "token")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// NOTE: We only allow requests cross-origin requests to subdomains / apps
		// if the request comes from the root domain. cross-sibling requests MUST be
		// unauthenticated to prevent evil.example.com from deleting alice.example.com
		origin, fetchSite := r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site")
		if origin == a.URL("") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		} else if o := r.Header.Get("Origin"); a.IsDeployOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "X-Token, Content-Type")
		} else if o != "" && fetchSite != "same-origin" {
			// NOTE: websocket does not send Sec-Fetch-Site
			o, err := url.Parse(origin)
			if isSameOrigin := err == nil && o.Host == r.Host; !isSameOrigin {
				r.Header.Del("Cookie")
			}
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(200)
			return
		}
		h.ServeHTTP(w, r)
	})
}
