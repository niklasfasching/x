package snap

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type S struct {
	Marshaller
	t          *testing.T
	snaps      map[string]any
	name, path string
}

type Marshaller interface {
	Marshal(any) ([]byte, error)
	Unmarshal([]byte, any) error
	Ext() string
}

var updateSnapshots = flag.Bool("update-snapshots", false, "update testdata snapshots")
var updateOnce = sync.Once{}
var snapPaths = map[string]struct{}{}

func Snap(t *testing.T, m Marshaller, namePartsAndValue ...any) {
	t.Helper()
	l := len(namePartsAndValue)
	if l == 0 {
		t.Fatal("missing snap value")
	}
	v, nameParts := namePartsAndValue[l-1], []string{}
	for _, v := range namePartsAndValue[:l-1] {
		if s, ok := v.(string); ok {
			nameParts = append(nameParts, s)
		} else {
			t.Fatalf("bad name part: %v", v)
		}
	}
	if name, path := snapName(t, m.Ext(), nameParts...); doUpdate() {
		updateOnce.Do(deleteSnaps(t))
		writeSnap(t, name, path, m, v)
	} else if actual, expected := Marshal(t, m, v), string(readSnap(t, path)); actual != expected {
		t.Fatal("\n" + Diff(expected, actual, "\n").Render(true))
	}
}

func Unmarshal[T any](t *testing.T, m Marshaller, bs []byte, v *T) T {
	if err := m.Unmarshal(bs, v); err != nil {
		t.Fatalf("unmarshal %v: %s", v, err)
	}
	return *v
}

func Marshal(t *testing.T, m Marshaller, v any) string {
	bs, err := m.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %s", v, err)
	}
	return string(bs)
}

func New(t *testing.T, m Marshaller, nameParts ...string) *S {
	name, path := snapName(t, m.Ext(), nameParts...)
	s := &S{m, t, map[string]any{}, name, path}
	if doUpdate() {
		updateOnce.Do(deleteSnaps(t))
		t.Cleanup(func() { writeSnap(t, s.name, path, s.Marshaller, s.snaps) })
	} else if bs := readSnap(t, path); len(bs) != 0 {
		if err := s.Marshaller.Unmarshal(bs, &s.snaps); err != nil {
			t.Fatalf("unmarshal snapshot: %s", err)
		}
	}
	return s
}

func (s *S) Snap(t *testing.T, k string, v any) {
	if doUpdate() {
		if _, ok := s.snaps[k]; ok {
			t.Fatalf("duplicate snap key: %s", k)
		}
		s.snaps[k] = v
	} else if actual, expected := s.Marshal(t, v), s.Marshal(t, s.snaps[k]); actual != expected {
		t.Fatal("\n" + Diff(expected, actual, "\n").Render(true))
	}
}

func (s *S) Marshal(t *testing.T, v any) string {
	return Marshal(t, s.Marshaller, v)
}

func (s *S) Unmarshal(t *testing.T, bs []byte, v any) {
	if err := s.Marshaller.Unmarshal(bs, v); err != nil {
		t.Fatalf("unmarshal %v: %s", v, err)
	}
}

func Cases(t *testing.T, pattern string, f func(*testing.T, string, []byte)) {
	ps, err := filepath.Glob(filepath.Join("testdata", pattern))
	if err != nil {
		t.Fatalf("glob: %s", err)
	} else if len(ps) == 0 {
		t.Fatalf("empty glob: %s", pattern)
	}
	for _, p := range ps {
		rp, _ := filepath.Rel("testdata", p)
		name := strings.SplitN(rp, ".", 2)[0]
		t.Run("[]/"+name, func(t *testing.T) {
			bs, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			f(t, name, bs)
		})
	}
}

func snapName(t *testing.T, ext string, nameParts ...string) (string, string) {
	name := strings.Join(nameParts, "_")
	if nameExt := filepath.Ext(name); nameExt != "" && name == nameExt {
		name, ext = "", nameExt
	}
	if name == "" {
		if i := strings.Index(t.Name(), "/[]/"); i != -1 {
			nameParts = strings.Split(t.Name()[i+4:], "/")
		} else {
			nameParts = strings.Split(t.Name(), "/")
		}
		name = strings.Join(nameParts, "_")
	}
	return name, filepath.Join("testdata", name+".snap"+ext)
}

func writeSnap(t *testing.T, name, path string, m Marshaller, v any) {
	if _, ok := snapPaths[path]; ok {
		t.Fatalf("duplicate snap path: %s", path)
	}
	snapPaths[path] = struct{}{}
	t.Logf("writing snapshot %q", name)
	if err := os.MkdirAll("testdata", 0755); err != nil {
		t.Fatalf("mkdir testdata: %s", err)
	} else if err := os.WriteFile(path, []byte(Marshal(t, m, v)), 0644); err != nil {
		t.Fatalf("write snapshot: %s", err)
	}
}

func readSnap(t *testing.T, path string) []byte {
	bs, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read snapshot: %s", err)
	}
	return bs
}

func deleteSnaps(t *testing.T) func() {
	return func() {
		pattern := "testdata/*.snap.*"
		log.Printf("clearing old snapshots: %q", pattern)
		ps, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob: %s", err)
		}
		for _, p := range ps {
			if err := os.Remove(p); err != nil {
				t.Fatalf("failed to rm old snapshot: %s", err)
			}
		}
	}
}

func doUpdate() bool {
	v := strings.ToLower(os.Getenv("UPDATE_SNAPSHOTS"))
	return *updateSnapshots || v == "true" || v == "1"
}
