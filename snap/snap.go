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
	Actual   [][3]string
	Path     string
}

type T struct{ Type, V string }
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
	if doUpdate() && len(snappers) == 0 {
		clearSnaps()
	}
	s := Read(t, name)
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

func Read(t *testing.T, nameAndOrExt string) *S {
	path := snapPath(t, nameAndOrExt)
	if err := os.MkdirAll("testdata", 0755); err != nil {
		t.Fatalf("failed to create testdata dir: %v", err)
	}
	bs, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("failed to read snap: %v", err)
	}
	switch filepath.Ext(path) {
	case ".json":
		mRaw, m := map[string]json.RawMessage{}, map[string]string{}
		bs := []byte(cmp.Or(jsonSpaceUnreplacer.Replace(string(bs)), "{}"))
		if err := json.Unmarshal(bs, &mRaw); err != nil {
			t.Fatalf("failed to unmarshal json snap: %v", err)
		}
		for k, v := range mRaw {
			unindented := strings.Join(strings.Split(string(v), "\n  "), "\n")
			m[k] = jsonSpaceReplacer.Replace(unindented)
		}
		return &S{T: t, Expected: m, Path: path}
	}
	d, err := html.Parse(bytes.NewReader(bs))
	if err != nil {
		t.Fatalf("failed to parse snap: %v", err)
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
	return &S{T: t, Expected: m, Path: path}
}

func (s *S) KeyedSnap(t *testing.T, k string, v any) {
	t.Run(k, func(t *testing.T) { s.Snap(t, v) })
}

func (s *S) Snap(t *testing.T, v any) {
	// we pass t instead of using s.T to handle nested t.Run correctly
	t.Helper()
	name, actual := s.Marshal(t, v)
	if expected := s.Expected[name]; !doUpdate() && actual != expected {
		d, _ := Diff(expected, actual, "\n").Render(true)
		t.Log("\n" + d)
		t.Cleanup(func() { t.Fatalf("Failed: %q", t.Name()) })
	}
}

func (s *S) Write() {
	s.Helper()
	if s.Failed() || len(s.Actual) == 0 || !doUpdate() {
		return
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		s.Fatalf("FAILED to write snap: %q already exists", s.Path)
	}
	switch filepath.Ext(s.Path) {
	case ".json":
		m := map[string]json.RawMessage{}
		for _, x := range s.Actual {
			m[x[1]] = json.RawMessage(jsonSpaceUnreplacer.Replace(x[2]))
		}
		_, content := s.marshal(s.T, m)
		s.write(strings.TrimSpace(jsonSpaceReplacer.Replace(content)) + "\n")
	default:
		w := &strings.Builder{}
		for _, x := range s.Actual {
			kind, name, body := x[0], x[1], x[2]
			body = "  " + strings.Join(strings.Split(body, "\n"), "\n  ")
			if kind == "text" || kind == "html" {
				fmt.Fprintf(w, "<noscript name=%q>\n%s\n</noscript>\n\n", name, body)
			} else {
				fmt.Fprintf(w, "<script type=%q name=%q>\n%s\n</script>\n\n", kind, name, body)
			}
		}
		s.write(strings.TrimSpace(w.String()) + "\n")
	}
}

func (s *S) write(content string) {
	if err := os.WriteFile(s.Path, []byte(content), 0644); err != nil {
		s.Fatalf("failed to write snap: %v", err)
	}
}

func (s *S) Marshal(t *testing.T, v any) (string, string) {
	s.Helper()
	name, parts := strconv.Itoa(len(s.Actual)), strings.SplitN(t.Name(), "/", 2)
	if len(parts) == 2 {
		name = parts[1]
	}
	if slices.ContainsFunc(s.Actual, func(x [3]string) bool { return x[1] == name }) {
		name += "-" + strconv.Itoa(len(s.Actual))
	}
	kind, actual := s.marshal(t, v)
	s.Actual = append(s.Actual, [3]string{kind, name, actual})
	return name, actual
}

func (s *S) marshal(t *testing.T, v any) (string, string) {
	t.Helper()
	switch v := v.(type) {
	case T:
		return v.Type, v.V
	case string:
		return "text", v
	case []byte:
		return "text", string(v)
	case fmt.Stringer:
		return "text", v.String()
	case encoding.TextMarshaler:
		bs, err := v.MarshalText()
		if err != nil {
			t.Fatalf("failed to marshal %T: %v", v, err)
		}
		return "text", string(bs)
	case HTML:
		return "html", string(v)
	case json.RawMessage:
		return "application/json", jsonSpaceReplacer.Replace(string(v))
	default:
		w := &bytes.Buffer{}
		e := json.NewEncoder(w)
		e.SetEscapeHTML(false)
		e.SetIndent("", "  ")
		if err := e.Encode(v); err != nil {
			t.Fatalf("failed to marshal %T: %v", v, err)
		}
		s := jsonSpaceReplacer.Replace(string(w.Bytes()))
		return "application/json", strings.TrimSpace(s)
	}
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

func clearSnaps() {
	pattern := "testdata/*.snap.*"
	log.Printf("clearing old snaps: %q", pattern)
	ps, err := filepath.Glob(pattern)
	if err != nil {
		panic(fmt.Sprintf("failed to clear snaps: glob: %v", err))
	}
	for _, p := range ps {
		if err := os.Remove(p); err != nil {
			panic(fmt.Sprintf("failed to clear snaps: rm %q: %v", p, err))
		}
	}
}

func doUpdate() bool {
	v := strings.ToLower(os.Getenv("UPDATE_SNAPS"))
	return *updateSnaps || v == "true" || v == "1"
}
