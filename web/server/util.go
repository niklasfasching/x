package server

import (
	"net/http"
	"os"
	"path"
)

type FilterFS struct {
	http.FileSystem
	Filter func(name string) bool
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
