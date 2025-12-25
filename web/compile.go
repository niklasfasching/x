package web

import (
	"cmp"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"maps"

	"github.com/niklasfasching/x/web/htmpl"
	"github.com/niklasfasching/x/web/server"
)

//go:embed templates/*
var baseFS embed.FS

var Funcs = template.FuncMap{
	"get": func(m any, k string) map[string]any {
		if ms, ok := m.(map[string]any); ok {
			if _, ok := ms[k]; !ok {
				return nil
			}
			return map[string]any{k: ms[k]}
		} else if ma, ok := m.(map[any]any); ok {
			if _, ok := ma[k]; !ok {
				return nil
			}
			return map[string]any{k: ma[k]}
		}
		return nil
	},
	"keys": func(m map[string]any) any { return maps.Keys(m) },
	"join": strings.Join,
	"log": func(args ...any) string {
		log.Println(args...)
		return ""
	},
	"json": func(v any) (string, error) {
		bs, err := json.MarshalIndent(v, "", "  ")
		return string(bs), err
	},
	"fromJSON": func(s string) (v any, err error) {
		return v, json.Unmarshal([]byte(s), &v)
	},
	"exec": func(t *template.Template, v any) (template.HTML, error) {
		w := &strings.Builder{}
		err := t.Execute(w, v)
		return template.HTML(w.String()), err
	},
	"query": query,
	"html":  func(v string) template.HTML { return template.HTML(v) },
	"_":     func() any { return util{} },
}
var tplExt = ".gohtml"
var testTplPrefix = "TEST "
var pathPatternRe = regexp.MustCompile(`{(\w+)[.]*}`)

func (h *H) Compile() (ts []*template.Template, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = r.(error)
		}
	}()
	t, err := h.BaseTemplate()
	if err != nil {
		return nil, fmt.Errorf("failed to parse base template: %w", err)
	}
	paths, err := findTemplateSets(h.FS, h.Dev)
	if err != nil {
		return nil, fmt.Errorf("failed to find templates: %w", err)
	}
	for _, paths := range paths {
		t, err := t.Clone()
		if err != nil {
			return nil, fmt.Errorf("failed to clone base template: %w", err)
		}
		t, err = t.ParseFS(h.FS, paths...)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", paths, err)
		}
		c := htmpl.NewCompiler(htmpl.ProcessDirectives)
		if err := c.Compile(t); err != nil {
			return nil, fmt.Errorf("failed to compile %s: %w", paths, err)
		}
		ts = append(ts, t)
	}
	return ts, nil
}

func (h *H) BaseTemplate() (*template.Template, error) {
	t, err := h.T.Clone()
	if err != nil {
		return nil, fmt.Errorf("failed to clone template: %w", err)
	}
	basePaths, err := findTemplateSets(baseFS, h.Dev)
	if err != nil {
		return nil, fmt.Errorf("failed to find base templates: %w", err)
	}
	return t.Option("missingkey=error").Funcs(Funcs).ParseFS(
		baseFS, basePaths["templates/base.gohtml"]...,
	)
}

func (h *H) HandleTemplate(pattern string, t *template.Template) {
	pattern, contentType, pathKeys := h.templatePattern(pattern)
	h.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if name := r.URL.Query().Get("debug"); h.Dev && name != "" {
			log.Println("TODO TEMPLATE", t.Lookup(cmp.Or(name, t.Name())).Tree.Root)
		}
		w.Header().Set("Content-Type", contentType)
		h.ServeTemplate(t, pathKeys, w, r)
	})
}

func (h *H) Register(ts []*template.Template) {
	h.ServeMux = http.ServeMux{}
	h.Handle("/", http.FileServer(&server.FilterFS{
		FileSystem: http.FS(h.FS),
		Filter:     func(name string) bool { return strings.HasSuffix(name, tplExt) },
	}))
	for _, t := range ts {
		for _, t := range t.Templates() {
			if p, _, _ := h.templatePattern(t.Name()); p != "" {
				h.HandleTemplate(p, t)
			}
		}
	}
}

func (h *H) TestTemplates() (map[string]*template.Template, error) {
	ts, err := h.Compile()
	if err != nil {
		return nil, err
	}
	m := map[string]*template.Template{}
	for _, t := range ts {
		for _, t := range t.Templates() {
			if name := t.Name(); !strings.HasPrefix(name, testTplPrefix) {
				continue
			} else if m[name] == nil {
				m[name] = t
			} else if m[name].Tree.ParseName != t.Tree.ParseName {
				return nil, fmt.Errorf("duplicate test %q: \n%v\n\t!=\n%v", name,
					m[name].Tree.Root, t.Tree.Root)
			}
		}
	}
	return m, nil
}

func findTemplateSets(tfs fs.FS, includeTests bool) (map[string][]string, error) {
	exts := []string{tplExt}
	if includeTests {
		exts = append(exts, ".test"+tplExt)
	}
	entrypoints, libs, m := []string{}, map[string][]string{}, map[string][]string{}
	err := fs.WalkDir(tfs, ".", func(path string, d fs.DirEntry, err error) error {
		parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
		nameParts := strings.SplitN(filepath.Base(path), ".", 2)
		libIndex := slices.IndexFunc(parts, func(name string) bool {
			return strings.HasPrefix(name, "_")
		})
		if hasExt := len(nameParts) == 2; err != nil || d.IsDir() || !hasExt {
			return err
		} else if fullExt := "." + nameParts[1]; !slices.Contains(exts, fullExt) {
			return nil
		} else if libIndex != -1 {
			dir := cmp.Or(strings.Join(parts[:libIndex], string(filepath.Separator)), ".")
			libs[dir] = append(libs[dir], path)
		} else {
			entrypoints = append(entrypoints, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, path := range entrypoints {
		for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
			if m[path] = append(m[path], libs[dir]...); dir == "." {
				break
			}
		}
		slices.Reverse(m[path])
		m[path] = append(m[path], path)
	}
	return m, nil
}

func (h *H) templatePattern(name string) (string, string, []string) {
	parts, pathKeys := strings.Split(name, " "), []string{}
	method, pth := parts[0], parts[len(parts)-1]
	if !slices.Contains([]string{"GET", "POST", "PUT"}, method) && !path.IsAbs(pth) {
		return "", "", nil
	}
	for _, m := range pathPatternRe.FindAllStringSubmatch(pth, -1) {
		pathKeys = append(pathKeys, m[1])
	}
	contentType := mime.TypeByExtension(cmp.Or(path.Ext(pth), ".html"))
	return name, contentType, pathKeys
}
