package rar

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/niklasfasching/x/snap"
)

func TestRARv4(t *testing.T) {
	testRAR(t, "testdata/v4.rar", "4")
}

func TestRARv5(t *testing.T) {
	testRAR(t, "testdata/v5.rar", "5")
}

func testRAR(t *testing.T, filename, version string) {
	ensureTestRAR(t, filename, version)
	f, err := os.Open(filename)
	if err != nil {
		t.Fatal("failed to open", err)
	}
	defer f.Close()
	r, err := Parse(f, 0, ".bar")
	if err != nil {
		t.Fatal("failed to parse", err)
	}
	bs := make([]byte, r.Size)
	_, err = r.ReadAt(bs, 0)
	if err != nil {
		t.Fatal("failed to read", err)
	}
	snap.Snap(t, []any{r, r.Name, string(bs)})
}

func ensureTestRAR(t *testing.T, filename, version string) {
	t.Helper()
	if _, err := os.Stat(filename); err == nil {
		return
	}
	cmd := exec.Command("rar", "a",
		"-m0",
		"-ma"+version,
		"-si"+"foo.bar",
		filename)
	cmd.Stdin, cmd.Stderr = strings.NewReader("content of rar v"+version), os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}
