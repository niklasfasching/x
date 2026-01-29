package server

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/niklasfasching/x/ops"
	"github.com/niklasfasching/x/soup"
	"github.com/niklasfasching/x/sq"
	"github.com/niklasfasching/x/telegram"
	"github.com/niklasfasching/x/web/push"
	"golang.org/x/net/websocket"
)

func (a *API) HandleMsg(ctx context.Context, msg telegram.Message) (string, error) {
	ctx, span := ops.Traces.Start(ctx, "HandleMsg")
	defer span.Close()
	parts := strings.Fields(msg.Text)
	if len(parts) == 0 {
		return "", nil
	} else if !slices.Contains(a.Users(), msg.Chat.Name) {
		return fmt.Sprintf("not allowed: %q", msg.Chat.Name), nil
	}
	user, txt := msg.Chat.Name, msg.Text
	isAdmin := a.IsAdmin(user)
	slog.InfoContext(ctx, "telegram", "user", user, "admin", isAdmin, "txt", txt)
	command := strings.SplitN(txt, " ", 2)[0]
	ops.Metrics.Counter(fmt.Sprintf("telegram_command,command=%s,user=%s", command, user), 1)
	switch {
	case txt == "/start" || strings.HasPrefix(txt, "/start "):
		if u, ok := strings.CutPrefix(txt, "/start "); ok && isAdmin && u != "login" {
			user = strings.TrimPrefix(u, "@")
		}
		v := fmt.Sprintf("Click [here](%s) to login as `@%s`", a.LoginURL(user), user)
		return "", a.T.SendMessage(msg.Chat.ID, "text", v, "parse_mode", "Markdown")
	case txt == "/list" && isAdmin:
		return strings.Join(a.Users(), "\n"), nil
	case strings.HasPrefix(txt, "/add ") && isAdmin:
		id := strings.TrimPrefix(strings.SplitN(txt, " ", 2)[1], "@")
		if _, err := a.users.Insert("OR REPLACE", User{ID: id}); err != nil {
			return err.Error(), nil
		}
		return fmt.Sprintf("Added %q", id), nil
	case strings.HasPrefix(txt, "/remove ") && isAdmin:
		id := strings.TrimPrefix(strings.SplitN(txt, " ", 2)[1], "@")
		if _, _, err := sq.Exec(a.DB, "DELETE FROM users WHERE id = ?", id); err != nil {
			return err.Error(), nil
		}
		return fmt.Sprintf("Removed %q", id), nil
	}
	help := fmt.Sprintf("Unhandled %q\n*Usage* [%s](%s)\n", txt, a.Domain, a.URL("")) +
		fmt.Sprintf("`/start` - Get %s link\n", a.Domain)
	return "", a.T.SendMessage(msg.Chat.ID, "text", help, "parse_mode", "Markdown")
}

func (a *API) HandleAPI(w http.ResponseWriter, r *http.Request) (int, any) {
	req := a.Req(r)
	slog.DebugContext(r.Context(), "HandleAPI", "user", req.User, "cmd", r.PathValue("cmd"))
	switch cmd := r.PathValue("cmd"); cmd {
	case "prompt":
		id, name := r.FormValue("id"), r.FormValue("name")
		if req.User.ID == "" {
			return 401, fmt.Errorf("unauthorized: login required")
		}
		if id != "" {
			if lvl := a.getLevel(req.User, id); lvl < LvlOwner {
				return 401, fmt.Errorf("unauthorized for app %v %q: %v %v", id, name, req.User, lvl)
			}
		}
		_, p, err := a.NewPrompt(r.Context(), req.User.ID, id, name)
		if err != nil {
			return 500, err
		}
		return 200, p
	case "remix":
		v := struct{ Id, SrcSlug string }{}
		if err := req.Decode(&v); err != nil {
			return 400, err
		}
		if dstLvl := a.getLevel(req.User, v.Id); dstLvl < LvlOwner {
			return 401, fmt.Errorf("unauthorized dst app %v: %v %v", v.Id, req.User, dstLvl)
		}
		dstX, err := sq.QueryOne[App](a.DB, "SELECT * FROM apps WHERE ID = ?", v.Id)
		if err != nil {
			return 404, fmt.Errorf("dst app not found: %q", v.Id)
		}
		srcX, err := sq.QueryOne[App](a.DB, "SELECT * FROM apps WHERE Slug = ?", v.SrcSlug)
		if err != nil {
			return 404, fmt.Errorf("src app not found: %q", v.SrcSlug)
		}
		if srcLvl := a.getLevel(req.User, srcX.ID); srcLvl == LvlNone {
			return 401, fmt.Errorf("unauthorized src app %v: %v %v", v.Id, req.User, srcLvl)
		}
		dstX.Name, dstX.ShortName = srcX.Name+" ("+srcX.Slug+")", srcX.ShortName
		dstX.Logo, dstX.HTML, dstX.JS = srcX.Logo, srcX.HTML, srcX.JS
		dstX.Schema, dstX.Query, dstX.Exec = srcX.Schema, srcX.Query, srcX.Exec
		dstX.Status, dstX.VAPIDKey = StatusDev, push.GeneratePrivateKey()
		err = a.apps.Update(dstX, "Status", "Name", "ShortName",
			"Logo", "HTML", "JS", "Schema", "Query", "Exec", "VAPIDKey")
		if err != nil {
			return 500, err
		}
		return 200, "ok"
	default:
		return 500, fmt.Errorf("unknown cmd: %q", cmd)
	}
}

func (a *API) handleUpdate(req *Req) (int, any) {
	if req.Level < LvlOwner {
		return 401, req.AuthError(LvlOwner, "update app")
	}
	app, err := req.App("ID, IsPublic, Status")
	if err != nil {
		return 500, err
	}
	cols := []string{}
	if name := req.FormValue("name"); name != "" {
		app.Name, cols = name, append(cols, "Name")
	}
	if slugVal := req.FormValue("slug"); slugVal != "" {
		app.Slug, cols = slug(slugVal), append(cols, "Slug")
	}
	if req.FormValue("toggle-public") == "true" {
		app.IsPublic, cols = !app.IsPublic, append(cols, "IsPublic")
	}
	if req.FormValue("toggle-status") == "true" {
		if app.Status == StatusLive {
			app.Status = StatusDev
		} else {
			app.Status = StatusLive
		}
		cols = append(cols, "Status")
	}
	if len(cols) == 0 {
		return 400, fmt.Errorf("no fields to update")
	} else if err := a.apps.Update(app, cols...); err != nil {
		return 500, err
	}
	return 200, "ok"
}

func (a *API) handleInvite(req *Req) (int, any) {
	if req.Level < LvlOwner {
		return 401, req.AuthError(LvlOwner, "create invite")
	}
	code := a.SignClaim(map[string]any{"op": "invite", "app": req.AppID}, a.TokenTTL)
	return 200, fmt.Sprintf("%s/invite?code=%s", a.URL(""), code)
}

func (a *API) handleSQL(req *Req, cmd, action string) (int, any) {
	if req.Level < LvlPublic {
		return 401, req.AuthError(LvlPublic, "SQL access")
	}
	args := []any{}
	if err := req.Decode(&args); err != nil {
		return 500, err
	}
	ctx, cancel := context.WithTimeout(req.Context(), 60*time.Second)
	defer cancel()
	v, err := a.RunAppSQL(ctx, req.User, req.AppID, cmd, action, args, req.Level)
	if err != nil {
		return 500, err
	}
	req.Track(cmd)
	return 200, v
}

func (a *API) handleDeploy(req *Req) (int, any) {
	if !req.User.IsDeployToken(req.AppID) {
		return 401, fmt.Errorf("unauthorized: need owner + deploy token")
	}
	req.Track("deploy")
	x := App{}
	if err := req.Decode(&x); err != nil {
		return 400, err
	}
	tokenRe := regexp.MustCompile(`PAT_[a-zA-Z0-9_\-\.]+`)
	x.HTML = tokenRe.ReplaceAllString(x.HTML, "")
	x.JS = tokenRe.ReplaceAllString(x.JS, "")
	x.ID = req.AppID
	if _, migrateErr := a.GetAppDB(req.Context(), x.ID, x.Schema, false); migrateErr != nil {
		mErr := &sq.MigrateError{}
		if errors.As(migrateErr, &mErr) && mErr.Reason == "forward_only" {
			return 400, map[string]any{
				"error":      "Schema Change Blocked",
				"details":    mErr.Details,
				"old_tables": mErr.OldTables,
			}
		}
		return 400, migrateErr
	}
	if err := a.UpdateApp(x); err != nil {
		return 500, err
	}
	return 200, "ok"
}

func (a *API) handleAssets(req *Req, action string) (int, any) {
	if !req.User.IsDeployToken(req.AppID) {
		return 401, req.AuthError(LvlOwner, "asset management")
	}
	x, err := req.App("Schema")
	if err != nil {
		return 500, err
	}
	db, err := a.GetAppDB(req.Context(), req.AppID, x.Schema, false)
	if err != nil {
		return 500, err
	}
	if action == "" {
		m := map[string]AssetDef{}
		if err := req.Decode(&m); err != nil {
			return 400, err
		}
		missing := []string{}
		for name, def := range m {
			v, err := sq.QueryOne[GeneratedAsset](db,
				"SELECT AssetDef FROM generatedassets WHERE id = ?", name)
			if err != nil || v.Prompt != def.Prompt {
				missing = append(missing, name)
			}
		}
		return 200, missing
	}
	v := GeneratedAsset{}
	if err := req.Decode(&v); err != nil {
		return 400, err
	}
	_, _, kvs := sq.RowMap(v)
	kvs["ID"] = action
	if _, err := sq.Insert(db, "OR REPLACE", "generatedassets", kvs); err != nil {
		return 500, err
	}
	return 200, "ok"
}

func (a *API) handlePush(req *Req, action string) (int, any) {
	if req.Level < LvlPublic {
		return 401, req.AuthError(LvlPublic, "push subscription")
	}
	switch action {
	case "sub":
		s := push.Sub{}
		if err := req.Decode(&s); err != nil {
			return 400, err
		}
		return 200, a.PushSub(req.User, req.AppID, s)
	case "emit":
		v := struct {
			User, Title, Body string
			Users             []string
		}{}
		if err := req.Decode(&v); err != nil {
			return 400, err
		}
		msg, err := json.Marshal(map[string]string{"title": v.Title, "body": v.Body})
		if err != nil {
			return 500, err
		}
		return 200, a.PushEmit(req.Context(), cmp.Or(req.User.ID, "anon#"+v.User),
			req.AppID, v.Users, msg)
	}
	return 500, fmt.Errorf("unknown action")
}

func (a *API) handleCron(req *Req, action string) (int, any) {
	if req.Level < LvlUser {
		return 401, req.AuthError(LvlUser, "cron management")
	}
	switch action {
	case "list":
		crons, err := sq.QueryMap[any](a.DB,
			`SELECT id, schedule, title, body, count FROM crons WHERE AppID = ? AND UserID = ?`,
			req.AppID, req.User.ID)
		if err != nil {
			return 500, err
		} else if crons == nil {
			crons = []map[string]any{}
		}
		return 200, crons
	case "save":
		r := Cron{}
		if err := req.Decode(&r); err != nil {
			return 400, err
		}
		next := nextRun(r.Schedule, time.Now())
		if next.IsZero() {
			return 400, fmt.Errorf("invalid schedule")
		}
		job := Cron{
			AppID:    req.AppID,
			UserID:   req.User.ID,
			Title:    r.Title,
			Body:     r.Body,
			Schedule: r.Schedule,
			Count:    cmp.Or(r.Count, -1),
			NextRun:  next.Unix(),
		}
		if _, err := a.crons.Insert("OR REPLACE", job); err != nil {
			return 500, err
		}
		return 200, "ok"
	case "delete":
		v := struct{ ID int }{}
		if err := req.Decode(&v); err != nil {
			return 400, err
		}
		if _, _, err := sq.Exec(a.DB, "DELETE FROM Crons WHERE ID = ? AND AppID = ? AND UserID = ?",
			v.ID, req.AppID, req.User.ID); err != nil {
			return 500, err
		}
		return 200, "ok"
	}
	return 404, fmt.Errorf("unknown action")
}

func (a *API) handleDelete(req *Req) (int, any) {
	if req.Level == LvlUser {
		x, err := req.App("ID, Users")
		if err != nil {
			return 500, err
		}
		x.Users = slices.DeleteFunc(x.Users, func(id string) bool { return id == req.User.ID })
		return 200, a.apps.Update(x, "Users")
	} else if req.Level == LvlOwner {
		ctx := req.Context()
		if _, _, err := sq.ExecContext(ctx, a.DB, "DELETE FROM apps WHERE ID = ?", req.AppID); err != nil {
			return 500, err
		}
		st := req.State()
		st.Lock()
		if db, ok := st.DBs[req.AppID]; ok {
			delete(st.DBs, req.AppID)
			db.DB.Close()
		}
		st.Unlock()
		if err := errors.Join(
			os.Remove(filepath.Join(a.DataDir, req.AppID+".sqlite")),
			os.Remove(filepath.Join(a.DataDir, req.AppID+".dev.sqlite")),
		); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 500, err
		}
		return 200, "ok"
	} else {
		return 401, req.AuthError(LvlUser, "delete app")
	}
}

func (a *API) handleFetch(req *Req) (int, any) {
	if req.Level < LvlUser {
		return 401, req.AuthError(LvlUser, "fetch proxy")
	}
	r := struct {
		Url, Method, Body string
		Headers           map[string]string
	}{}
	if err := req.Decode(&r); err != nil {
		return 400, err
	}
	if r.Headers == nil {
		r.Headers = map[string]string{}
	}
	if r.Headers["User-Agent"] == "" {
		r.Headers["User-Agent"] = req.UserAgent()
	}
	status, headers, body, err := Fetch(req.Context(), r.Method, r.Url, r.Body, r.Headers)
	if err != nil {
		return 500, err
	}
	return 200, map[string]any{"status": status, "headers": headers, "body": body}
}

func (a *API) HandleAppAPI(w http.ResponseWriter, r *http.Request) (int, any) {
	req, cmd, action := a.Req(r), r.PathValue("cmd"), r.PathValue("action")
	slog.DebugContext(r.Context(), "HandleAPI", "user", req.User, "cmd", cmd,
		"action", action, "id", req.AppID, "lvl", req.Level)
	switch cmd {
	case "query", "exec":
		return a.handleSQL(req, cmd, action)
	case "assets":
		return a.handleAssets(req, action)
	case "deploy":
		return a.handleDeploy(req)
	case "invite":
		return a.handleInvite(req)
	case "push":
		return a.handlePush(req, action)
	case "cron":
		return a.handleCron(req, action)
	case "delete":
		return a.handleDelete(req)
	case "update", "publish", "rename":
		return a.handleUpdate(req)
	case "fetch":
		return a.handleFetch(req)
	case "share":
		if err := r.ParseForm(); err != nil {
			return 400, err
		}
		http.Redirect(w, r, "/share?"+r.Form.Encode(), http.StatusSeeOther)
		return 0, nil
	default:
		return 500, fmt.Errorf("unknown cmd/action: %q/%q", cmd, action)
	}
}

func (a *API) HandleIndex(w http.ResponseWriter, r *http.Request) (int, error) {
	u, ok := a.Subject(r)
	xs, err := sq.Query[App](a, `
		SELECT ID, Slug, ChatID, Name, Owner, Users, IsPublic, Status
		FROM apps
		WHERE ? AND
              (Owner = ? OR EXISTS (SELECT 1 FROM json_each(Users) WHERE value = ?))
		ORDER BY CreatedAt DESC`, ok, u.ID, u.ID)
	if err != nil {
		return 500, err
	}
	private, public, community := []App{}, []App{}, []App{}
	for _, x := range xs {
		if x.Owner == u.ID {
			if x.IsPublic {
				public = append(public, x)
			} else {
				private = append(private, x)
			}
		} else {
			community = append(community, x)
		}
	}
	ops.Metrics.Gauge("apps_displayed,type=private", float64(len(private)))
	ops.Metrics.Gauge("apps_displayed,type=public", float64(len(public)))
	ops.Metrics.Gauge("apps_displayed,type=community", float64(len(community)))
	w.Header().Set("Content-Type", "text/html")
	return 0, a.Render(r.Context(), w, "index", map[string]any{
		"Name":      "index",
		"User":      u.ID,
		"isadmin":   a.IsAdmin(u.ID),
		"Private":   private,
		"Public":    public,
		"Community": community,
	})
}

func (a *API) HandleRootAssets(w http.ResponseWriter, r *http.Request) (int, error) {
	switch name := r.PathValue("name"); name {
	case "api.mjs":
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/javascript")
		if err := a.Render(r.Context(), w, "api.mjs", map[string]any{}); err != nil {
			return 500, err
		}
		return 0, nil
	default:
		return 404, fmt.Errorf("asset %q not found", name)
	}
}

func (a *API) HandleAssets(w http.ResponseWriter, r *http.Request) (int, error) {
	req := a.Req(r)
	switch name := r.PathValue("name"); name {
	case "icon.svg":
		if req.Level == LvlNone {
			return 401, req.AuthError(LvlPublic, "icon.svg")
		}
		x, err := req.App("Logo")
		if err != nil {
			return 500, err
		}
		logo := strings.TrimSpace(x.Logo)
		if !strings.Contains(logo, "xmlns=") {
			logo = strings.Replace(logo, "<svg", `<svg xmlns="http://www.w3.org/2000/svg"`, 1)
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(strings.ReplaceAll(logo, "currentColor", "#000000")))
		return 0, nil
	case "manifest.json":
		// TODO: hotfix. require lvl != LvlNone after wildcard DNS fix in k
		x, err := req.App("Name, ShortName, Shortcuts")
		if err != nil {
			return 500, err
		}
		manifest := map[string]any{
			"name":       x.Name,
			"short_name": x.ShortName,
			"start_url":  "/",
			"display":    "standalone",
			"icons": []map[string]any{{
				"src":   "/assets/icon.svg",
				"sizes": "any",
				"type":  "image/svg+xml",
			}},
			"share_target": map[string]any{
				"action": "/api/share",
				"method": "POST",
				"params": map[string]string{
					"title": "title",
					"text":  "text",
					"url":   "url",
				},
			},
		}
		if len(x.Shortcuts) > 0 {
			manifest["shortcuts"] = x.Shortcuts
		}
		w.Header().Set("Content-Type", "application/manifest+json")
		return 0, json.NewEncoder(w).Encode(manifest)
	case "sw.mjs":
		if req.Level == LvlNone {
			return 401, req.AuthError(LvlPublic, "service worker")
		}
		w.Header().Set("Service-Worker-Allowed", "/")
		w.Header().Set("Content-Type", "application/javascript")
		x, err := req.App("JS")
		if err != nil {
			return 500, err
		} else if err := a.Render(r.Context(), w, "sw.mjs", x); err != nil {
			return 500, err
		}
		return 0, nil
	default:
		if req.Level == LvlNone {
			return 401, req.AuthError(LvlPublic, "asset")
		}
		x, err := req.App("Schema")
		if err != nil {
			return 500, err
		}
		db, err := a.GetAppDB(r.Context(), req.AppID, x.Schema, false)
		if err != nil {
			return 500, err
		}
		asset, err := sq.QueryOne[GeneratedAsset](db,
			"SELECT mime, data FROM generatedassets WHERE id = ?", name)
		if err != nil {
			return 404, fmt.Errorf("asset %q not found: %w", name, err)
		}
		w.Header().Set("Content-Type", asset.Mime)
		w.Write(asset.Data)
		return 0, nil
	}
}

func (a *API) HandleAppHTML(w http.ResponseWriter, r *http.Request) (int, error) {
	req := a.Req(r)
	if req.Level == LvlNone {
		return 401, req.AuthError(LvlPublic, "app HTML")
	}
	x, err := req.App("HTML, VAPIDKey, Owner, Users")
	if err != nil {
		return 500, err
	}
	if r.URL.Query().Get("raw") == "true" {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(x.HTML))
		return 0, nil
	}
	d, err := soup.Parse(strings.NewReader(x.HTML))
	if err != nil {
		return 500, err
	}
	w.Header().Set("Content-Type", "text/html")
	if err := a.Render(r.Context(), w, "app-html", map[string]any{
		"body":     template.HTML(d.First("body").OuterHTML()),
		"debug":    r.URL.Query().Get("debug"),
		"VAPIDKey": push.PublicKey(x.VAPIDKey),
		"user":     req.User,
		"lvl":      req.Level,
		"owner":    x.Owner,
		"users":    x.Users,
	}); err != nil {
		return 500, err
	}
	return 0, nil
}

func (a *API) HandleSSE(w http.ResponseWriter, r *http.Request) (int, any) {
	_, span := ops.Traces.Start(r.Context(), "HandleSSE")
	defer span.Close()
	start := time.Now()
	req := a.Req(r)
	if req.Level == LvlNone {
		return 401, req.AuthError(LvlPublic, "SSE")
	}
	defer func() {
		ops.Metrics.Hist("sse_connection_duration_ms", time.Since(start).Milliseconds(), "app=%s", req.AppID)
	}()
	if r.Method == "POST" {
		req.Track("sse_emit")
		bs, err := io.ReadAll(r.Body)
		if err != nil {
			return 500, err
		}
		a.GetAppState(req.AppID).Broker.Pub("sse", bs)
		return 200, "ok"
	}
	req.Track("sse_sub")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	for msg := range a.GetAppState(req.AppID).Broker.Sub(r.Context(), "sse") {
		req.Track("sse_messages_sent")
		fmt.Fprintf(w, "data: %s\n\n", msg)
		w.(http.Flusher).Flush()
	}
	return 0, nil
}

func (a *API) HandleWS(ws *websocket.Conn) {
	_, span := ops.Traces.Start(ws.Request().Context(), "HandleWS")
	defer span.Close()
	start := time.Now()
	req := a.Req(ws.Request())
	defer ws.Close()
	if req.Level == LvlNone {
		return
	}
	defer func() {
		ops.Metrics.Hist("ws_connection_duration_ms", time.Since(start).Milliseconds(), "app=%s", req.AppID)
	}()
	b := a.GetAppState(req.AppID).Broker
	go func() {
		for msg := range b.Sub(ws.Request().Context(), "ws") {
			if err := websocket.Message.Send(ws, string(msg)); err != nil {
				req.Track("ws_messages_sent_errors")
				return
			}
			req.Track("ws_messages_sent")
		}
	}()
	for {
		msg := ""
		if err := websocket.Message.Receive(ws, &msg); err != nil {
			req.Track("ws_messages_received_errors")
			break
		}
		req.Track("ws_messages_received")
		b.Pub("ws", []byte(msg))
	}
}

func (a *API) ClaimHandler(w http.ResponseWriter, r *http.Request) (int, error) {
	m, ok := a.VerifyClaim(r.FormValue("code"))
	switch r.Method {
	case "POST":
		switch r.URL.Path {
		case "/login":
			c := http.Cookie{
				Name: "token", HttpOnly: true, Secure: !a.IsDev(),
				SameSite: http.SameSiteLaxMode, Domain: a.Domain,
				MaxAge: int((365 * 24 * time.Hour).Seconds()),
			}
			if !ok || m["op"] != "login" {
				slog.InfoContext(r.Context(), "login op", "m", m, "ok", ok)
				c.MaxAge = -1
			} else {
				u := User{ID: m["user"].(string)}
				c.Value = a.Sign(u, time.Duration(c.MaxAge)*time.Second)
			}
			http.SetCookie(w, &c)
			http.Redirect(w, r, "/", http.StatusSeeOther)
		case "/invite":
			if !ok || m["op"] != "invite" {
				return 401, fmt.Errorf("invalid claim")
			}
			u, ok := a.Subject(r)
			if !ok {
				return 401, fmt.Errorf("unauthorized %v", u)
			}
			x, err := sq.QueryOne[App](a.DB, "SELECT ID, Owner, Users FROM apps WHERE ID = ?",
				m["app"])
			if err != nil {
				return 404, err
			} else if x.Owner != u.ID {
				x.Users = slices.Compact(slices.Sorted(slices.Values(append(x.Users, u.ID))))
				if err := a.apps.Update(x, "Users"); err != nil {
					return 500, err
				}
			}
			http.Redirect(w, r, "/", http.StatusSeeOther)
		}
	case "GET":
		w.Header().Set("Content-Type", "text/html")
		return 0, a.Render(r.Context(), w, "claim_form", map[string]any{
			"Name":    "claim_form",
			"Path":    r.URL.Path,
			"Code":    r.FormValue("code"),
			"Claim":   m,
			"IsAdmin": false,
		})
	}
	return 0, nil
}
