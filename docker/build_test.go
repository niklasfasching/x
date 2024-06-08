package docker

import (
	"path/filepath"
	"testing"
)

func TestDockerBuild(t *testing.T) {
	dir := filepath.Join("testdata", "ubuntu:22.04")
	b := Builder{
		Registry: &DefaultRegistry{},
	}
	err := b.Build(`
      FROM ubuntu:22.04
      RUN touch /foo && ls -lisah /foo /bar
    `, dir)
	if err != nil {
		t.Fatal(err)
	}
}
