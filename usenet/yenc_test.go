package usenet

import (
	"strings"
	"testing"

	"github.com/niklasfasching/x/util"
)

func TestYenc(t *testing.T) {
	encBS := Encode([]byte("foo\nbar"), 42, "foo.bar")
	// duplicate encoded slice since decode modifies it
	off, decBS, err := Decode([]byte(string(encBS)))
	if err != nil {
		t.Fatal("failed to decode", err)
	}
	util.Snapshot(t, []any{
		strings.Split(string(encBS), "\n"), string(decBS), off,
	})
}
