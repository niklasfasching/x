package web

import (
	"crypto/subtle"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	"github.com/niklasfasching/x/web/server"
)

type ErrHandler func(http.ResponseWriter, *http.Request) (int, error)

type util struct{}

type Option struct {
	K, V              string
	Selected, Default bool
}

//go:embed src
var src embed.FS

func Assets(dynamic bool) fs.FS {
	if !dynamic {
		fs, err := fs.Sub(src, "src")
		if err != nil {
			panic(err)
		}
		return fs
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		panic(fmt.Errorf("could not read buildInfo"))
	}
	for _, d := range info.Deps {
		if d.Path == "github.com/niklasfasching/x" && d.Replace != nil {
			return os.DirFS(filepath.Join(d.Replace.Path, "web", "src"))
		}
	}
	panic(fmt.Errorf("could not find (replace) path for module"))
}

func (f ErrHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if code, err := f(w, r); err != nil {
		http.Error(w, err.Error(), code)
	}
}

func AssetHandler(prefix string, dynamic bool) http.Handler {
	return http.StripPrefix(prefix, http.FileServer(&server.FilterFS{FileSystem: http.FS(Assets(dynamic))}))
}

func WithBasicAuth(h http.Handler, user, pass string, crash bool) http.Handler {
	eq := func(s1, s2 string) bool { return subtle.ConstantTimeCompare([]byte(s1), []byte(s2)) == 1 }
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("token"); err == nil {
			r.Header.Set("Authorization", "Basic "+c.Value)
		} else if t := r.URL.Query().Get("token"); t != "" && t != "null" {
			r.Header.Set("Authorization", "Basic "+t)
		}
		if rUser, rPass, ok := r.BasicAuth(); !ok || !eq(user, rUser) || !eq(pass, rPass) {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm="%s"`, "flix"))
			w.WriteHeader(http.StatusUnauthorized)
			if (rUser != "" || rPass != "") && crash {
				log.Fatalf("crash on bad login attempt: %s %q:%q", r.URL.Path, rUser, rPass)
			}
			return
		}
		if _, err := r.Cookie("token"); err != nil {
			http.SetCookie(w, &http.Cookie{
				Name:    "token",
				Value:   r.Header.Get("Authorization")[6:],
				Expires: time.Now().Add(365 * 24 * time.Hour),
			})
		}
		h.ServeHTTP(w, r)
	})
}

func (util) Exit() (any, error) {
	return nil, TemplateExitErr
}
