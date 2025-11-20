package container

import (
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/niklasfasching/x/snap"
)

func TestMain(t *testing.M) {
	if v := os.Getenv(EnvKey); v == "" {
		os.Exit(t.Run())
	} else {
		if err := AwaitIDMapping(); err != nil {
			log.Fatal(err)
		}
		funcs[v]()
	}
}

func MainRun(layersDir, dockerFile, contextDir string, force bool) error {
	v, err := ReExecNamespaced("true")
	if err != nil || v == "" {
		return err
	}
	b := Builder{Registry: DockerRegistry, LayersDir: layersDir, Force: force}
	d, err := Parse(dockerFile)
	if err != nil {
		return err
	}
	if err := b.Build(d, contextDir); err != nil {
		return err
	}
	return nil
}

var funcs = map[string]func(){
	"TestBuild": func() {
		dir, err := os.MkdirTemp("", "test")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(dir)
		layersDir, ctxDir := filepath.Join(dir, "layers"), filepath.Join(dir, "ctx")
		if err := os.MkdirAll(ctxDir, 0755); err != nil {
			log.Fatal(err)
		}
		b := Builder{Registry: DockerRegistry, LayersDir: layersDir}
		err = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(`
          FROM alpine:latest
          COPY . .
          RUN ls
        `), 0644)
		if err != nil {
			log.Fatal(err)
		}
		f, err := Parse(filepath.Join(dir, "Dockerfile"))
		if err != nil {
			log.Fatal(err)
		}
		err = b.Build(f, ctxDir)
		if err != nil {
			log.Fatal(err)
		}
	},
}

func TestBuild(t *testing.T) {
	if _, err := ReExecNamespaced("TestBuild"); err != nil {
		t.Fatal(err)
	}
}

func TestParse(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(`
      FROM ubuntu:24.04
      RUN apt-get install -y cowsay
      COPY . .
      CMD cowsay
    `), 0644)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	snap.Snap(t, f)
}
