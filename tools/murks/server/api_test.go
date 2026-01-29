package server

import (
	"testing"
	"time"

	"github.com/niklasfasching/x/headless"
)

func TestIntegrationAPI_DB(t *testing.T) {
	h, user := NewHarness(t, true), "test"
	a := h.DeployApp(user, App{
		Name:   "test",
		Schema: []string{"CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"},
		Exec: map[string]string{
			"add:1": "INSERT INTO items (name) VALUES (?)",
			"del:1": "DELETE FROM items WHERE id = ?",
		},
		Query: map[string]string{
			"list:1": "SELECT * FROM items ORDER BY id",
			"get:1":  "SELECT * FROM items WHERE id = ?",
		},
	})
	h.OpenApp(t, a, user)
	h.Eval(`
      await snap("identity", await api.identity());
      await snap("adds", await Promise.all([
        api.exec("add:1", "foo"),
        api.exec("add:1", "bar"),
      ]));
      await snap("list", await api.query("list:1"));
      await snap("get", await api.query("get:1", 1));
      await snap("del", await api.exec("del:1", 1));
      await snap("list", await api.query("list:1"));
      await snap("list", await api.query("list:1"));
    `)
}

func TestIntegrationAPI_Broker(t *testing.T) {
	h, user := NewHarness(t, true), "test"
	a := h.DeployApp(user, App{Name: "test"})
	h.OpenApp(t, a, user)
	h.Eval(`
      await snap("emit/sub", await new Promise(resolve => {
        api.subSSE((v) => resolve(v));
        setTimeout(() => api.emitSSE({value: 42}))
      }));
    `)
}

func TestIntegrationAPI_WS(t *testing.T) {
	h, user := NewHarness(t, true), "test"
	a := h.DeployApp(user, App{Name: "test"})
	h.OpenApp(t, a, user)
	h.Eval(`
      await snap("ws connect", await new Promise((resolve, reject) => {
        const ws = api.ws();
        ws.onmessage = (e) => {
          resolve(JSON.parse(e.data));
          ws.close();
        }
        ws.onopen = () => ws.send(JSON.stringify({type: "test", value: 123}));;
        ws.onerror = (e) => reject(e);
      }));
    `)
}

func TestIntegrationAPI_Cron(t *testing.T) {
	h, user := NewHarness(t, true), "test"
	a := h.DeployApp(user, App{Name: "test"})
	h.OpenApp(t, a, user)
	h.Eval(`
      // NOTE: skip push manager / service worker setup
      api.subPush = () => {};
      await api.cronSave("5m", "Test", "body");
      const crons = await api.cronList();
      await snap("cron list length", crons.length);
      await snap("cron delete", await api.cronDelete(crons[0].ID));
      await snap("cron list after delete", await api.cronList());
    `)
}

func TestIntegrationAPI_Push(t *testing.T) {
	t.Skip("Only works with headless=false. Paste the below into console")
	h, user := NewHarness(t, true), "test"
	a := h.DeployApp(user, App{Name: "test"})
	h.OpenApp(t, a, user)
	h.Exec("Browser.grantPermissions", headless.Params{
		"origin":      h.URL(a.Slug),
		"permissions": []string{"notifications"},
	}, nil)
	h.Eval(`
      await snap("sub", await api.subPush())
      await snap("emit", await api.emitPush("title", "body", ["test"]))
    `)
	time.Sleep(time.Minute)
}

func TestAPI_RunAppSQL(t *testing.T) {
	h := NewHarness(t, false)
	owner, member, guest := User{ID: "owner"}, User{ID: "member"}, User{ID: "guest"}
	a := h.CreateApp(owner.ID, App{
		Query: map[string]string{
			"public:1": "SELECT 'public' AS v",
			"member:2": "SELECT 'member' AS v",
			"owner:3":  "SELECT 'owner' AS v",
		},
		Exec: map[string]string{
			"public:1": "SELECT 'public' AS v",
			"member:2": "SELECT 'member' AS v",
			"owner:3":  "SELECT 'owner' AS v",
		},
		Users: []string{member.ID},
	})
	for _, tc := range []struct {
		user   User
		action string
		cmd    string
		ok     bool
	}{
		{owner, "owner:3", "query", true},
		{owner, "owner:3", "exec", true},
		{member, "member:2", "query", true},
		{member, "owner:3", "query", false},
		{guest, "public:1", "query", false},
		{guest, "member:2", "query", false},
	} {
		t.Run(tc.user.ID+"_"+tc.cmd+"_"+tc.action, func(t *testing.T) {
			lvl := h.getLevel(tc.user, a.ID)
			_, err := h.RunAppSQL(h.Context(), tc.user, a.ID, tc.cmd, tc.action, nil, lvl)
			if (err == nil) != tc.ok {
				t.Fatalf("user=%s cmd=%s action=%s: want ok=%v, got err=%v",
					tc.user.ID, tc.cmd, tc.action, tc.ok, err)
			}
		})
	}
}
