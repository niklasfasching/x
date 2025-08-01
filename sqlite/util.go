package sqlite

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"text/template"
)

type Handler struct {
	Stmts map[string]*Stmt
}
type result struct {
	Value any
	Err   error
}

//go:embed templates.tpl
var templatesString string
var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"panic": func(args ...any) string { panic(args) },
	"lower": strings.ToLower,
	"join":  strings.Join,
}).Parse(templatesString))
var regexpExtractRegexps = map[string]*regexp.Regexp{}

func Template(name string, v any) string {
	w := &strings.Builder{}
	if err := templates.ExecuteTemplate(w, name, v); err != nil {
		panic(err)
	}
	return w.String()
}

func JSONIndex(name, table, id string, cols ...string) string {
	return Template("fts", map[string]any{
		"name":      name,
		"id":        id,
		"table":     table,
		"tokenizer": "json",
		"cols":      cols,
	})
}

func HTMLIndex(name, table, id string, cols ...string) string {
	return Template("fts", map[string]any{
		"name":      name,
		"id":        id,
		"table":     table,
		"tokenizer": "html",
		"cols":      cols,
	})
}

func NewHandler(db *DB, stmts ...*Stmt) (*Handler, error) {
	m := map[string]*Stmt{}
	for _, s := range stmts {
		if _, ok := m[s.Name]; ok {
			return nil, fmt.Errorf("duplicate stmt %q", s.Name)
		}
		stmt, err := db.Prepare(s.SQL)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare stmt %q: %w", s.SQL, err)
		}
		s.Stmt = stmt
		m[s.Name] = s
	}
	return &Handler{m}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if code, err := h.Handle(w, r); code != -1 {
		http.Error(w, err.Error(), code)
	}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) (int, error) {
	name := r.PathValue("stmt")
	if name == "" {
		return 404, fmt.Errorf("{stmt} path value not set")
	}
	stmt, ok := h.Stmts[name]
	if !ok {
		return 404, fmt.Errorf("stmt %q not found", name)
	}
	if r.Method != http.MethodPost && !stmt.IsQuery {
		return 400, fmt.Errorf("non-query stmt %q must not be called as %s", name, r.Method)
	}
	args := map[string]any{}
	switch r.Method {
	case "GET":
		for k, v := range r.URL.Query() {
			args[k] = v
		}
	case "POST":
		if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
			if err := r.ParseForm(); err != nil {
				return 400, fmt.Errorf("failed to parse form: %s", err)
			}
			for k, vs := range r.Form {
				if strings.HasPrefix(k, "[]") {
					bs, err := json.Marshal(vs)
					if err != nil {
						return 400, fmt.Errorf("invalid form value %q(%s): %s", k, vs, err)
					}
					args[strings.TrimPrefix(k, "[]")] = string(bs)
				} else {
					args[k] = vs[0]
				}
			}
		} else {
			body := map[string]any{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				return 400, fmt.Errorf("failed to unmarshal body: %s", err)
			}
			for k, v := range body {
				args[k] = v
			}
		}
	default:
		return 405, fmt.Errorf("%s not allowed", r.Method)
	}
	for _, k := range stmt.Args {
		if _, ok := args[k]; !ok {
			return 400, fmt.Errorf("missing required arg %q for %s", k, name)
		}
	}
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	e.SetEscapeHTML(false)
	if err := e.Encode(newResult(stmt.Do(args))); err != nil {
		panic(err)
	}
	return -1, nil
}

func FuncMap(db *DB) template.FuncMap {
	return template.FuncMap{
		"query": func(q string, args ...any) any {
			r := newResult(scan[Map[any]](queryRows(db, true, q, args...)))
			return r
		},
		"queryOne": func(q string, args ...any) any {
			return newResult(scanOne[Map[any]](queryRows(db, true, q, args...)))
		},
		"exec": func(q string, args ...any) result {
			id, count, err := CheckExec(db, q, args...)
			return newResult(map[string]any{"id": id, "count": count}, err)
		},
	}
}

func GenerateTypeMethods(pkg, path string, vs ...any) {
	w, types := &bytes.Buffer{}, []any{}
	for _, v := range vs {
		t := reflect.TypeOf(v)
		type field struct{ Name, Kind, Fallback, Extra string }
		fields, pk := []field{}, ""
		for i := 0; i < t.NumField(); i++ {
			f, kind, fallback := t.Field(i), "", ""
			switch f.Type.Kind() {
			case reflect.Struct, reflect.Map:
				kind, fallback = "JSON_TEXT", "{}"
			case reflect.Slice, reflect.Array:
				kind, fallback = "JSON_TEXT", "[]"
			case reflect.Int:
				if name := strings.ToLower(f.Name); name == "rowid" || name == "id" {
					kind, pk = "INTEGER PRIMARY KEY AUTOINCREMENT", f.Name
				}
			}
			fields = append(fields, field{f.Name, kind, fallback, f.Tag.Get("sql")})
		}
		types = append(types, map[string]any{"name": t.Name(), "pk": pk, "fields": fields})
	}
	data := map[string]any{"pkg": pkg, "types": types}
	if err := templates.ExecuteTemplate(w, "type-interface-methods", data); err != nil {
		panic(err)
	} else if err := os.WriteFile(path, w.Bytes(), 0644); err != nil {
		panic(err)
	}
	log.Printf("Updated pkg %s (%q)", pkg, path)
}

func regexpExtract(input, regexpString string, i int) (string, error) {
	r, err := regexpExtractRegexps[regexpString], error(nil)
	if r == nil {
		r, err = regexp.Compile(regexpString)
		if err != nil {
			return "", err
		}
		regexpExtractRegexps[regexpString] = r
	}
	if m := r.FindStringSubmatch(input); len(m) > i {
		return m[i], nil
	}
	return "", nil
}

func newResult(v any, err error) result { return result{v, err} }
func (r result) MarshalJSON() ([]byte, error) {
	if r.Err != nil {
		return json.Marshal(map[string]any{"err": r.Err.Error()})
	} else {
		return json.Marshal(map[string]any{"value": r.Value})
	}
}
