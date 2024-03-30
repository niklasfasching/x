package rar

import (
	"os"
	"testing"

	"github.com/niklasfasching/x/util"
)

func TestRARv4(t *testing.T) {
	testRAR(t, "testdata/v4.rar")
}

func TestRARv5(t *testing.T) {
	testRAR(t, "testdata/v5.rar")
}

func testRAR(t *testing.T, path string) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal("failed to open", err)
	}
	defer f.Close()
	r, err := Parse(f, ".bar")
	if err != nil {
		t.Fatal("failed to parse", err)
	}
	bs := make([]byte, r.Size)
	_, err = r.ReadAt(bs, 0)
	if err != nil {
		t.Fatal("failed to read", err)
	}
	util.Snapshot(t, []any{r, r.Name, string(bs)})
}
