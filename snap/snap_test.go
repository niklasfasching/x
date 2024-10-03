package snap

import (
	"strconv"
	"strings"
	"testing"

	"github.com/niklasfasching/x/jml"
	"golang.org/x/exp/slices"
)

type JML struct{}

func (JML) Marshal(v any) ([]byte, error)    { return jml.Marshal(v) }
func (JML) Unmarshal(bs []byte, v any) error { return jml.Unmarshal(bs, v) }
func (JML) Ext() string                      { return ".yml" }

func TestSnap(t *testing.T) {
	Snap(t, TXT{}, "snap text")
	Snap(t, TXT{}, "filename", "snap text")
}

func TestCasesSnap(t *testing.T) {
	Cases(t, "*.case.json", func(t *testing.T, name string, bs []byte) {
		vs := Unmarshal(t, JSON{}, bs, &[]int{})
		slices.Reverse(vs)
		Snap(t, JSON{}, vs)
	})
}

func TestMultiSnap(t *testing.T) {
	s := New(t, JML{})
	s.Snap(t, "key 1", "value 1")
	s.Snap(t, "key 2 ", 2)
	s.Snap(t, "key 3", struct{ Value int }{3})

	s2 := New(t, JML{}, "custom", "name")
	s2.Snap(t, "k", "v")

	s3 := New(t, JML{}, ".custom-ext")
	s3.Snap(t, "k", "v")
}

func TestDiff(t *testing.T) {
	s, join := New(t, JML{}), func(xs ...int) (s string) {
		for _, x := range xs {
			s += strconv.Itoa(x) + "\n"
		}
		return strings.TrimSpace(s)
	}

	run := func(k, a, b string) {
		d := Diff(a, b, "\n").Render(false)
		s.Snap(t, k, map[string]string{"a": a, "b": b, "diff": d})
	}

	run("insert", join(1, 2, 3, 3), join(1, 2, 3, 6, 3))
	run("delete", join(1, 2, 3, 3), join(1, 3, 3))
	run("replace", join(1, 2, 3, 3), join(4, 5, 6))
	run("complex", join(1, 2, 3, 3), join(4, 1, 2, 5, 3, 6, 3))

}
