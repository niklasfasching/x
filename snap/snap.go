package snap

import (
	"bytes"
	"cmp"
	"encoding"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

type S struct {
	*testing.T
	Expected map[string]string
	Actual   []X
	Path     string
}

type T struct{ Type, V string }
type X struct{ K, V, Kind string }
type HTML string

var updateSnaps = flag.Bool("snap", false, "update testdata/*.snap")
var snappers = map[*testing.T]*S{}
var jsonSpaceReplacer = strings.NewReplacer("\\n", "\n\t", "\\t", "\t")
var jsonSpaceUnreplacer = strings.NewReplacer("\n\t", "\\n", "\t", "\\t")

func Snap(t *testing.T, v any) {
	t.Helper()
	New(t).Snap(t, v)
}

func KeyedSnap(t *testing.T, k string, v any) {
	New(t).KeyedSnap(t, k, v)
}

func NamedSnap(t *testing.T, name string, v any) {
	NewNamed(t, name).Snap(t, v)
}

func New(t *testing.T, exts ...string) *S {
	t.Helper()
	if snappers[t] == nil {
		ext := cmp.Or(strings.Join(exts, ""), ".html")
		snappers[t] = NewNamed(t, t.Name()+ext)
	}
	return snappers[t]
}

func NewNamed(t *testing.T, name string) *S {
	t.Helper()
	if DoUpdate() && len(snappers) == 0 {
		if err := ClearSnaps(); err != nil {
			t.Fatal(err)
		}
	}
	path := snapPath(t, name)
	expected, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &S{T: t, Path: path, Expected: expected}
	t.Cleanup(func() { s.Write() })
	return s
}

func Cases(t *testing.T, glob string, f func(*testing.T, string, []byte)) {
	t.Helper()
	s := New(t)
	paths, err := filepath.Glob(filepath.Join("testdata", glob))
	if err != nil {
		t.Fatalf("glob: %s", err)
	} else if len(paths) == 0 {
		t.Fatalf("empty glob: %s", glob)
	}
	for _, p := range paths {
		name := strings.SplitN(Must(filepath.Rel("testdata", p)), ".", 2)[0]
		t.Run(name, func(t *testing.T) {
			bs, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			snappers[t] = s
			f(t, name, bs)
		})
	}
}

func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func (s *S) KeyedSnap(t *testing.T, k string, v any) {
	t.Run(k, func(t *testing.T) { s.Snap(t, v) })
}

func (s *S) Snap(t *testing.T, v any) {
	// we pass t instead of using s.T to handle nested t.Run correctly
	t.Helper()
	x := s.Marshal(t, v)
	if expected := s.Expected[x.K]; !DoUpdate() && x.V != expected {
		d, _ := Diff(expected, x.V, "\n").Render(true)
		t.Log("\n" + d)
		t.Cleanup(func() { t.Fatalf("Failed: %q", t.Name()) })
	}
}

func (s *S) Write() {
	s.Helper()
	if len(s.Actual) != len(s.Expected) && !DoUpdate() {
		s.Fatalf("Expected %d but saw %d snaps", len(s.Expected), len(s.Actual))
	} else if s.Failed() || len(s.Actual) == 0 || !DoUpdate() {
		return
	} else if err := Write(s.Path, s.Actual); err != nil {
		s.T.Fatal(err)
	}
}

func (s *S) Marshal(t *testing.T, v any) X {
	s.Helper()
	x, err := Marshal(t.Name(), s.Actual, v)
	if err != nil {
		t.Fatal(err)
	}
	s.Actual = append(s.Actual, x)
	return x
}

func Read(path string) (map[string]string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("failed to create snap dir %q: %w", filepath.Dir(path), err)
	}
	bs, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read snap: %w", err)
	}
	switch filepath.Ext(path) {
	case ".json":
		mRaw, m := map[string]json.RawMessage{}, map[string]string{}
		bs := []byte(cmp.Or(jsonSpaceUnreplacer.Replace(string(bs)), "{}"))
		if err := json.Unmarshal(bs, &mRaw); err != nil {
			return nil, fmt.Errorf("failed to unmarshal json snap: %w", err)
		}
		for k, v := range mRaw {
			unindented := strings.Join(strings.Split(string(v), "\n  "), "\n")
			m[k] = jsonSpaceReplacer.Replace(unindented)
		}
		return m, nil
	}
	d, err := html.Parse(bytes.NewReader(bs))
	if err != nil {
		return nil, fmt.Errorf("failed to parse snap: %w", err)
	}
	m, i := map[string]string{}, 0
	for n := range d.Descendants() {
		if n.Type == html.ElementNode {
			switch tag := n.Data; tag {
			case "script", "noscript":
				k, v := strconv.Itoa(i), n.FirstChild.Data
				for _, a := range n.Attr {
					if a.Key == "name" && a.Val != "" {
						k = a.Val
						break
					}
				}
				v = strings.TrimSuffix(strings.TrimPrefix(v, "\n  "), "\n")
				m[k] = strings.Join(strings.Split(v, "\n  "), "\n")
			}
		}
	}
	return m, nil
}

func Write(path string, actual []X) error {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return fmt.Errorf("FAILED to write snap: %q already exists", path)
	}
	content := ""
	switch filepath.Ext(path) {
	case ".json":
		m := map[string]json.RawMessage{}
		for _, x := range actual {
			m[x.K] = json.RawMessage(jsonSpaceUnreplacer.Replace(x.V))
		}
		_, v, err := marshal(m)
		if err != nil {
			return err
		}
		content = strings.TrimSpace(jsonSpaceReplacer.Replace(v)) + "\n"
	default:
		w := &strings.Builder{}
		for _, x := range actual {
			body := "  " + strings.Join(strings.Split(x.V, "\n"), "\n  ")
			if x.Kind == "text" || x.Kind == "html" {
				fmt.Fprintf(w, "<noscript name=%q>\n%s\n</noscript>\n\n", x.K, body)
			} else {
				fmt.Fprintf(w, "<script type=%q name=%q>\n%s\n</script>\n\n", x.Kind, x.K, body)
			}
		}
		content = strings.TrimSpace(w.String()) + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write snap: %w", err)
	}
	return nil
}

func Marshal(name string, actual []X, v any) (X, error) {
	name, parts := strconv.Itoa(len(actual)), strings.SplitN(name, "/", 2)
	if len(parts) == 2 {
		name = parts[1]
	}
	if slices.ContainsFunc(actual, func(x X) bool { return x.K == name }) {
		name += "-" + strconv.Itoa(len(actual))
	}
	kind, s, err := marshal(v)
	if err != nil {
		return X{}, err
	}
	return X{name, s, kind}, nil
}

func marshal(v any) (string, string, error) {
	switch v := v.(type) {
	case T:
		return v.Type, v.V, nil
	case string:
		return "text", v, nil
	case []byte:
		return "text", string(v), nil
	case json.RawMessage:
		return "application/json", jsonSpaceReplacer.Replace(string(v)), nil
	case fmt.Stringer:
		return "text", v.String(), nil
	case encoding.TextMarshaler:
		bs, err := v.MarshalText()
		if err != nil {
			return "", "", fmt.Errorf("failed to marshal %T: %w", v, err)
		}
		return "text", string(bs), nil
	case HTML:
		return "html", string(v), nil
	default:
		w := &bytes.Buffer{}
		e := json.NewEncoder(w)
		e.SetEscapeHTML(false)
		e.SetIndent("", "  ")
		if err := e.Encode(v); err != nil {
			return "", "", fmt.Errorf("failed to marshal %T: %w", v, err)
		}
		s := jsonSpaceReplacer.Replace(w.String())
		return "application/json", strings.TrimSpace(s), nil
	}
}

func ClearSnaps() error {
	pattern := "testdata/*.snap.*"
	log.Printf("clearing old snaps: %q", pattern)
	ps, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("failed to clear snaps: glob: %w", err)
	}
	for _, p := range ps {
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("failed to clear snaps: rm %q: %w", p, err)
		}
	}
	return nil
}

func snapPath(t *testing.T, nameAndOrExt string) string {
	parts, name, ext := strings.SplitN(nameAndOrExt, ".", 2), "", ""
	if justExt := strings.HasPrefix(nameAndOrExt, "."); justExt {
		name, ext = t.Name(), parts[1]
	} else if withoutExt := len(parts) == 1; withoutExt {
		name, ext = parts[0], ".html"
	} else {
		name, ext = parts[0], parts[1]
	}
	name = filepath.Clean(strings.ReplaceAll(name+".snap."+ext, "/", "_"))
	return filepath.Join("testdata", name)
}

func DoUpdate() bool {
	v := strings.ToLower(os.Getenv("UPDATE_SNAPS"))
	return *updateSnaps || v == "true" || v == "1"
}
