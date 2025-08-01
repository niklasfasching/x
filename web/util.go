package web

import (
	"crypto/subtle"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"time"
)

type ErrHandler func(http.ResponseWriter, *http.Request) (int, error)

type FilterFS struct {
	http.FileSystem
	Filter func(name string) bool
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
	return http.StripPrefix(prefix, http.FileServer(&FilterFS{http.FS(Assets(dynamic)), nil}))
}

func (fs *FilterFS) Open(name string) (http.File, error) {
	if fs.Filter != nil && fs.Filter(name) {
		return nil, os.ErrNotExist
	} else if f, err := fs.FileSystem.Open(name); err != nil {
		return nil, err
	} else if s, err := f.Stat(); err != nil {
		return nil, err
	} else if !s.IsDir() {
		return f, nil
	} else if index := path.Join(name, "index.html"); fs.Filter != nil && fs.Filter(index) {
		return nil, os.ErrNotExist
	} else if f2, err := fs.FileSystem.Open(index); err != nil {
		return nil, err
	} else if err := f2.Close(); err != nil {
		return nil, err
	} else {
		return f, nil
	}
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

type File interface {
	Readdir(count int) ([]fs.FileInfo, error)
	Stat() (fs.FileInfo, error)
}
