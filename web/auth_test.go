package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/niklasfasching/x/snap"
)

func TestAuthSignVerify(t *testing.T) {
	a, u := &Auth[string]{Secret: "secret"}, "user-123"
	t.Run("accept valid", func(t *testing.T) {
		v, ok := a.Verify(a.Sign(u, time.Hour))
		if !ok || v != u {
			t.Fatalf("verify failed: got (%v, %v), want (%v, true)", v, ok, u)
		}
	})
	t.Run("reject expired", func(t *testing.T) {
		if _, ok := a.Verify(a.Sign(u, -time.Hour)); ok {
			t.Fatalf("accepted expired token")
		}
	})
	t.Run("reject tampered", func(t *testing.T) {
		tok := a.Sign(u, time.Hour)
		parts := strings.Split(strings.TrimPrefix(tok, "PAT_"), ".")
		uBase64, sig := parts[0], parts[1]
		bs, _ := base64.RawURLEncoding.DecodeString(uBase64)
		v := token[string]{}
		if err := json.Unmarshal(bs, &v); err != nil {
			t.Fatal(err)
		} else if v.V != u {
			t.Fatalf("unexpected token value: %q != %q", v.V, u)
		}
		v.V = "tampered-" + v.V
		bs, _ = json.Marshal(v)
		tamperedToken := "PAT_" + base64.RawURLEncoding.EncodeToString(bs) + "." + sig
		if u, ok := a.Verify(tamperedToken); ok {
			t.Fatalf("accepted tampered token: %q", u)
		}
	})
	t.Run("reject malformated", func(t *testing.T) {
		validToken := a.Sign("user", time.Hour)
		head, sig, _ := strings.Cut(validToken, ".")
		tcs := []struct{ name, tok string }{
			{"empty", ""},
			{"no prefix", strings.TrimPrefix(validToken, "PAT_")},
			{"no signature", strings.Split(validToken, ".")[0]},
			{"invalid hex", head + ".zz"},
			{"invalid b64", "PAT_!!." + sig},
			{"invalid json", "PAT_YQ." + sig},
			{"wrong secret", (&Auth[string]{Secret: "wrong"}).Sign("user", time.Hour)},
		}
		for _, tc := range tcs {
			if v, ok := a.Verify(tc.tok); ok {
				t.Fatalf("accepted invalid token %q: %v", tc.name, v)
			}
		}
	})
}

func TestAuthMiddleware(t *testing.T) {
	a, k := &Auth[string]{Secret: "secret"}, "token"
	s := snap.New(t)
	run := func(t *testing.T, r *http.Request) *httptest.ResponseRecorder {
		h := a.WithAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			v, ok := a.Subject(r)
			s.Snap(t, map[string]any{"v": v, "ok": ok})
		}), k)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	t.Run("cookie", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: k, Value: a.Sign("cookie-user", time.Hour)})
		run(t, r)
	})
	t.Run("query", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/?"+k+"="+a.Sign("query-user", time.Hour), nil)
		run(t, r)
	})
	t.Run("header", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("x-"+k, a.Sign("header-user", time.Hour))
		run(t, r)
	})
	t.Run("clear on invalid", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("x-"+k, a.Sign("header-user", time.Hour))
		run(t, r)
		r.Header.Set("x-"+k, "invalid")
		w := run(t, r)
		if h := w.Header().Get("Clear-Site-Data"); h != `"cookies"` {
			t.Fatalf("missing Clear-Site-Data: %q", h)
		}
	})
}
