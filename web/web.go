package web

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
)

type HTTPFileSystem struct{ http.FileSystem }

//go:embed src
var src embed.FS

func Assets(dynamic bool) (fs.FS, error) {
	if !dynamic {
		return fs.Sub(src, "src")
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil, fmt.Errorf("could not read buildInfo")
	}
	for _, d := range info.Deps {
		if d.Path == "github.com/niklasfasching/x" && d.Replace != nil {
			return os.DirFS(filepath.Join(d.Replace.Path, "web", "src")), nil
		}
	}
	return nil, fmt.Errorf("could not find (replace) path for module")
}

func FSHandler(prefix string, f fs.FS) http.Handler {
	return http.StripPrefix(prefix, http.FileServer(&HTTPFileSystem{http.FS(f)}))
}

func (h *HTTPFileSystem) Open(name string) (http.File, error) {
	f, err := h.FileSystem.Open(name)
	if err != nil {
		return nil, err
	} else if s, err := f.Stat(); err != nil {
		return nil, err
	} else if !s.IsDir() {
		return f, nil
	} else if f2, err := h.Open(filepath.Join(name, "index.html")); err != nil {
		return nil, err
	} else if err := f2.Close(); err != nil {
		return nil, err
	}
	return f, nil
}
