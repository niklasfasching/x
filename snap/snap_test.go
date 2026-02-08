package snap

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/niklasfasching/x/format/jml"
	"golang.org/x/exp/slices"
)

type textMarshaller struct{ v string }
type stringer struct{ v string }

func (v textMarshaller) MarshalText() ([]byte, error) { return []byte(v.v), nil }
func (v stringer) String() string                     { return v.v }

func TestSnap(t *testing.T) {
	Snap(t, "multiple snaps in the same file")
	Snap(t, []byte("of all kinds of types..."))
	Snap(t, json.RawMessage(`[1, 2, 3, "this should be a json tag"]`))
	Snap(t, textMarshaller{"types implementing fmt.Stringer"})
	Snap(t, textMarshaller{"types implementing encoding.TextMarshaler"})
	Snap(t, map[string]any{
		"1. and any other": "marshallable type as json",
		"2. with":          "pretty\nprinted\n\tmultiline\nstrings",
	})
}

func TestNamedSnap(t *testing.T) {
	NamedSnap(t, "NamedSnap1.gohtml", "snaps can be named explictly")
	s := NewNamed(t, "NamedSnap2.gohtml")
	s.Snap(t, "and thereby we can create multiple snaps")
	t.Run("Nested", func(t *testing.T) {
		s.Snap(t, "for a single test")
	})
	Snap(t, "Even next to an unnamed default snap")
	Snap(t, "Or multiple")
}

func TestExtSnap(t *testing.T) {
	j := NewNamed(t, ".json")
	j.Snap(t, json.RawMessage(`"snaps with well known extensions"`))
	j.Snap(t, json.RawMessage(`"are marshalled into\n the corresponding format"`))
}

func TestKeyedSnap(t *testing.T) {
	s := New(t)
	s.KeyedSnap(t, "key", "snaps can be keyed")
	t.Run("nested", func(t *testing.T) {
		s.KeyedSnap(t, "parent file", "nested snaps can also be keyed")
		KeyedSnap(t, "separate file", "also as separate files")
	})
}

func TestNestedSnap(t *testing.T) {
	s := New(t)
	s.Snap(t, "multiple snaps in the same file")
	t.Run("Nested Test With / Special [] Chars", func(t *testing.T) {
		s.Snap(t, "even across nested tests")
		Snap(t, "this will be a separate file though")
	})

}

func TestSnapName(t *testing.T) {
	m, vs := map[string]string{}, []string{
		".just-an-ext",
		".just.multiple.ext",
		"name.with-an-ext",
		"name.with-multiple.ext",
		"name/with/slashes.ext",
		"name/with/slashes",
		".ext/with/slashes",
	}
	for _, v := range vs {
		m[v] = snapPath(t, v)
	}
	Snap(t, m)
}

func TestCasesSnap(t *testing.T) {
	Cases(t, "*.case.json", func(t *testing.T, name string, bs []byte) {
		vs := []int{}
		vs = Must(vs, json.Unmarshal(bs, &vs))
		slices.Reverse(vs)
		NewNamed(t, ".json").Snap(t, vs)
	})
}

func TestDiff(t *testing.T) {
	s, join := New(t), func(xs ...int) (s string) {
		for _, x := range xs {
			s += strconv.Itoa(x) + "\n"
		}
		return strings.TrimSpace(s)
	}
	run := func(k, a, b string) {
		t.Run(k, func(t *testing.T) {
			d, eq := Diff(a, b, "\n").Render(false, nil)
			s.Snap(t, &jml.JML{V: map[string]any{"a": a, "b": b, "diff": d, "eq": eq}})
		})
	}
	run("insert", join(1, 2, 3, 3), join(1, 2, 3, 6, 3))
	run("delete", join(1, 2, 3, 3), join(1, 3, 3))
	run("replace", join(1, 2, 3, 3), join(4, 5, 6))
	run("complex", join(1, 2, 3, 3), join(4, 1, 2, 5, 3, 6, 3))
}
