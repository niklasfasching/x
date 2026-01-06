package container

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/niklasfasching/x/snap"
	"github.com/niklasfasching/x/soup"
)

func TestBuild(t *testing.T) {
	if done, err := ReExecTestNamespaced(t.Name()); err != nil {
		t.Fatal(err)
	} else if done {
		return
	}
	dir, err := os.MkdirTemp("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	layersDir, ctxDir := filepath.Join(dir, "layers"), filepath.Join(dir, "ctx")
	if err := os.MkdirAll(ctxDir, 0755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	c := soup.Transport{Cache: &soup.FileCache{Root: filepath.Join(wd, "testdata/http")}}.Client()
	b := Builder{Registry: NewDockerRegistry(c), LayersDir: layersDir}
	f, err := Parse(`
	  FROM alpine:latest
	  COPY . .
	  RUN ls
    `, ".")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Build(f, ctxDir); err != nil {
		t.Fatal(err)
	}
}

func TestParse(t *testing.T) {
	f, err := Parse(`
      FROM ubuntu:24.04
      RUN apt-get install -y cowsay
      COPY . .
      CMD cowsay
    `, ".")
	if err != nil {
		t.Fatal(err)
	}
	snap.Snap(t, f)
}
