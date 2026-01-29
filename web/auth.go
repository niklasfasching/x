package web

import (
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Auth[T any] struct {
	Secret string
}

type token[T any] struct {
	V   T
	Exp int64
}

type authCtxKey struct{}

func (a *Auth[T]) WithAuth(next http.Handler, k string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := cmp.Or(r.Header.Get("x-"+k), r.URL.Query().Get(k))
		if c, err := r.Cookie(k); err == nil {
			v = c.Value
		}
		if t, ok := a.Verify(v); ok {
			r = r.WithContext(context.WithValue(r.Context(), authCtxKey{}, t))
		} else if v != "" {
			w.Header().Set("Clear-Site-Data", `"cookies"`)
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Auth[T]) Subject(r *http.Request) (T, bool) {
	return AuthSubject[T](r.Context())
}

func (a *Auth[T]) Sign(v T, ttl time.Duration) string {
	return a.sign(token[T]{V: v, Exp: time.Now().Add(ttl).Unix()})
}

func (a *Auth[T]) Verify(v string) (T, bool) {
	t := new(T)
	if !a.verify(v, t) {
		return *t, false
	}
	return *t, true
}

func (a *Auth[T]) SignClaim(v map[string]any, ttl time.Duration) string {
	return a.sign(token[map[string]any]{V: v, Exp: time.Now().Add(ttl).Unix()})
}

func (a *Auth[T]) VerifyClaim(v string) (map[string]any, bool) {
	m := map[string]any{}
	if !a.verify(v, &m) {
		return nil, false
	}
	return m, true
}

func (a *Auth[T]) Inspect(v string) (any, bool) {
	x := any(nil)
	return x, a.verify(v, &x)
}

func (a *Auth[T]) sign(v any) string {
	bs, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Errorf("auth: sign:%w", err))
	}
	h := hmac.New(sha256.New, []byte(a.Secret))
	h.Write(bs)
	return fmt.Sprintf("PAT_%s.%x", base64.RawURLEncoding.EncodeToString(bs), h.Sum(nil))
}

func (a *Auth[T]) verify(v string, dst any) bool {
	v, isToken := strings.CutPrefix(v, "PAT_")
	msg, sig, hasSig := strings.Cut(v, ".")
	if !isToken || !hasSig {
		return false
	}
	h := hmac.New(sha256.New, []byte(a.Secret))
	bs, err := base64.RawURLEncoding.DecodeString(msg)
	if err != nil {
		return false
	}
	h.Write(bs)
	if actual, err := hex.DecodeString(sig); err != nil {
		return false
	} else if !hmac.Equal(h.Sum(nil), actual) {
		return false
	}
	t := token[json.RawMessage]{}
	if json.Unmarshal(bs, &t) != nil || time.Now().Unix() > t.Exp {
		return false
	}
	return json.Unmarshal(t.V, dst) == nil
}

func AuthSubject[T any](ctx context.Context) (T, bool) {
	v, ok := ctx.Value(authCtxKey{}).(T)
	return v, ok
}
