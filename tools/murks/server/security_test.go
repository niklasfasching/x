package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/niklasfasching/x/snap"
	"github.com/niklasfasching/x/web/push"
)

func TestSecurity(t *testing.T) {
	h := NewHarness(t, false)
	owner, other := User{ID: "owner"}, User{ID: "other"}
	priv := h.CreateApp(owner.ID, App{ID: "private", Slug: "app", Name: "app"})
	pub := h.CreateApp(owner.ID, App{ID: "public", Slug: "pub", Name: "pub", IsPublic: true})
	for _, tc := range []struct{ name, user, method, path, origin string }{
		{"owner can access private app", owner.ID,
			"GET", h.URL(priv.Slug) + "/", ""},
		{"other cannot access private app", other.ID,
			"GET", h.URL(priv.Slug) + "/", ""},
		{"other can access public app", other.ID,
			"GET", h.URL(pub.Slug) + "/", ""},
		{"owner can access manifest", owner.ID,
			"GET", h.URL(priv.Slug) + "/assets/manifest.json", ""},
		{"other cannot access icon of private app", other.ID, "GET",
			h.URL(priv.Slug) + "/assets/icon.svg", ""},
		{"other cannot delete owner app", other.ID,
			"POST", h.URL(priv.Slug) + "/api/delete", h.URL("")},
		{"cross-origin delete is blocked (cookie stripping)", owner.ID,
			"POST", h.URL(priv.Slug) + "/api/delete", "http://evil.com"},
		{"sibling subdomain delete is blocked (cookie stripping)", owner.ID,
			"POST", h.URL(priv.Slug) + "/api/delete", "http://other." + DevDomain},
		{"other cannot request prompt for owner app", other.ID,
			"POST", h.URL("") + "/api/prompt?id=" + priv.ID, ""},
		{"owner can delete app from root", owner.ID,
			"POST", h.URL(priv.Slug) + "/api/delete", h.URL("")},
	} {
		s := snap.New(t)
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.user != "" {
				r.AddCookie(&http.Cookie{Name: "token", Value: h.Sign(User{ID: tc.user}, time.Hour)})
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			} else {
				r.Header.Set("Sec-Fetch-Site", "same-origin")
			}
			w := httptest.NewRecorder()
			h.Handler().ServeHTTP(w, r)
			clean := strings.NewReplacer(
				push.PublicKey(priv.VAPIDKey), "",
				push.PublicKey(pub.VAPIDKey), "",
				"<script>", "<x-script>",
				"</script>", "</x-script>",
			).Replace
			s.KeyedSnap(t, tc.name, map[string]any{
				"code":   w.Code,
				"header": w.Header(),
				"body":   clean(w.Body.String()),
			})
		})
	}

	// Attack vectors: all should fail
	alice := h.CreateApp("alice", App{Slug: "alice", Name: "Alice"})
	eve := h.CreateApp("eve", App{Slug: "eve", Name: "Eve"})
	for _, tc := range []struct{ name, user, path, origin string }{
		{"cross-user delete", "eve", h.URL(alice.Slug) + "/api/delete", h.URL("")},
		{"cross-user prompt", "eve", "/api/prompt?id=" + alice.ID, ""},
		{"cross-origin delete", "alice", h.URL(alice.Slug) + "/api/delete", "http://evil.com"},
		{"cross-subdomain delete", "alice", h.URL(alice.Slug) + "/api/delete", h.URL(eve.Slug)},
		{"token replay", "alice", h.URL(eve.Slug) + "/api/deploy", h.URL("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tc.path, nil)
			r.AddCookie(&http.Cookie{Name: "token", Value: h.Sign(User{ID: tc.user}, time.Hour)})
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
				r.Header.Set("Sec-Fetch-Site", "cross-site")
			} else {
				r.Header.Set("Sec-Fetch-Site", "same-origin")
			}
			w := httptest.NewRecorder()
			h.Handler().ServeHTTP(w, r)
			if w.Code == 200 {
				t.Errorf("attack succeeded")
			}
		})
	}
}

func TestIntegrationSecurity(t *testing.T) {
	h, owner, other := NewHarness(t, true), "owner", "other"
	priv := h.DeployApp(owner, App{Name: "private", IsPublic: false})
	pub := h.DeployApp(owner, App{
		Name: "public", IsPublic: true,
		Query: map[string]string{"ping:3": "SELECT 1"},
	})
	t.Run("private app access", func(t *testing.T) {
		h.OpenApp(t, priv, owner)
		h.Eval(`await snap("owner", location.hostname)`)
		h.Open(t, h.LoginURL(other))
		h.Wait(`!!document.querySelector("button[title='Enter']")`, 5)
		h.Eval(`document.querySelector("button[title='Enter']").click()`)
		h.Wait(`location.pathname === "/"`, 5)
		h.Open(t, h.URL(priv.Slug))
		h.Wait(`location.hostname === "`+DevDomain+`"`, 5)
		h.Eval(`await snap("other", location.hostname)`)
	})

	t.Run("public app permissions", func(t *testing.T) {
		h.OpenApp(t, pub, other)
		h.Eval(`
			await snap("other", {
				identity: await api.identity(),
				location: location.hostname,
			});
			try {
				await api.query("ping:3");
				await snap("other ping", "unexpected success");
			} catch (e) {
				await snap("other ping", e.message);
			}
		`)
	})
}
