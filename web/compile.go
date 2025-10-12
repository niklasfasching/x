package web

import (
	"cmp"
	_ "embed"
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
	"strings"

	"github.com/niklasfasching/x/web/htmpl"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

//go:embed templates.html
var webTemplate string
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
	"keys": func(m map[string]any) []string { return maps.Keys(m) },
	"join": strings.Join,
	"log": func(args ...any) string {
		log.Println(args...)
		return ""
	},
	"json": func(v any) (string, error) {
		bs, err := json.MarshalIndent(v, "", "  ")
		return string(bs), err
	},
	"query": query,
	"html":  func(v string) template.HTML { return template.HTML(v) },
}
var tplExt = ".gohtml"
var tplLibPrefix = "_"
var tplTestPrefix = "test"
var pathPatternRe = regexp.MustCompile(`{(\w+)[.]*}`)

func templateHandler(base *template.Template, tfs fs.FS, dev bool) http.Handler {
	base, err := base.Clone()
	if err != nil {
		panic(fmt.Errorf("failed to clone template: %w", err))
	}
	base = template.Must(base.Option("missingkey=error").Funcs(Funcs).Parse(webTemplate))
	mux := &http.ServeMux{}
	mux.Handle("/", http.FileServer(&FilterFS{http.FS(tfs), func(name string) bool {
		return strings.HasSuffix(name, tplExt)
	}}))
	exts := []string{tplExt}
	if dev {
		exts = append(exts, ".test"+tplExt)
	}
	paths, err := findTemplateSets(tfs, exts)
	if err != nil {
		panic(fmt.Errorf("failed to find templates: %w", err))
	}
	for _, paths := range paths {
		tpl, err := base.Clone()
		if err != nil {
			panic(fmt.Errorf("failed to clone base template: %w", err))
		}
		tpl, err = tpl.ParseFS(tfs, paths...)
		if err != nil {
			panic(fmt.Errorf("failed to parse %s: %w", paths, err))
		}
		if err := htmpl.NewCompiler(htmpl.ProcessDirectives).Compile(tpl); err != nil {
			panic(fmt.Errorf("failed to compile %s: %w", paths, err))
		}
		for _, tpl := range tpl.Templates() {
			if p, contentType, pks := templatePattern(tpl.Name()); p != "" {
				mux.HandleFunc(tpl.Name(), func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", contentType)
					ServeTemplate(tpl, pks, w, r)
				})
			}
		}
	}
	return mux
}

func findTemplateSets(tfs fs.FS, fullExts []string) (map[string][]string, error) {
	entrypoints, libs, m := []string{}, map[string][]string{}, map[string][]string{}
	err := fs.WalkDir(tfs, ".", func(path string, d fs.DirEntry, err error) error {
		parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
		nameParts := strings.SplitN(filepath.Base(path), ".", 2)
		libIndex := slices.IndexFunc(parts, func(name string) bool {
			return strings.HasPrefix(name, "_")
		})
		if hasExt := len(nameParts) == 2; err != nil || d.IsDir() || !hasExt {
			return err
		} else if fullExt := "." + nameParts[1]; !slices.Contains(fullExts, fullExt) {
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

func templatePattern(name string) (string, string, []string) {
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
