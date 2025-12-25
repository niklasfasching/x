package sq

import (
	"database/sql"
	"database/sql/driver"
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"strings"
	"text/template"
	"time"

	"slices"

	"golang.org/x/exp/maps"
)

type Args map[string]any
type JSON struct{ V any }
type PureFunc struct{ F any }

var MigrateErr = fmt.Errorf("schema needs to be rebuilt")

//go:embed templates.tpl
var templatesString string
var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"panic": func(args ...any) string { panic(args) },
}).Parse(templatesString))
var defaultFuncs = map[string]any{
	"re_extract": PureFunc{regexpExtract},
	"dt":         PureFunc{timeDT},
}
var regexpExtractRegexps = map[string]*regexp.Regexp{}
var sqlNameRe = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
var sqlQuoteRe = regexp.MustCompile(`{'([a-zA-Z0-9_]+)}`)
var sqlBindRe = regexp.MustCompile(`{[$@:]([a-zA-Z0-9_]+)}`)
var sqlLineIfRe = regexp.MustCompile(`(?m)(^.*){<< (.*)}\s*$`)
var typeTime = reflect.TypeOf(time.Time{})
var driverIndex = 0

func Template(name string, v any) string {
	w := &strings.Builder{}
	if err := templates.ExecuteTemplate(w, name, v); err != nil {
		panic(err)
	}
	return w.String()
}

func Schema(v any, rest ...string) string {
	auto := map[string]string{
		"CreatedAt": "create",
		"UpdatedAt": "update",
	}
	t := reflect.TypeOf(v)
	type field struct{ Name, Kind, Fallback, Extra string }
	fields, pk, raw := []field{}, "", ""
	for i := 0; i < t.NumField(); i++ {
		f, kind, fallback, extra := t.Field(i), "", "", t.Field(i).Tag.Get("sq")
		if ft := f.Type; ft == typeTime {
			kind = "TIMESTAMP"
			if on, ok := auto[f.Name]; ok && extra == "AUTO" {
				extra, raw = "", raw+Template("schema-field-default", map[string]any{
					"on":      on,
					"table":   t.Name(),
					"field":   f.Name,
					"when":    "'0001-01-01 00:00:00+00:00'",
					"default": "CURRENT_TIMESTAMP",
				})
			}
		} else if v := jsonDefault(f.Type); v != "" {
			kind, fallback = "JSON_TEXT", v
		} else if name := strings.ToLower(f.Name); f.Type.Kind() == reflect.Int &&
			(name == "rowid" || name == "id") {
			kind, pk = "INTEGER PRIMARY KEY AUTOINCREMENT", f.Name
		}
		fields = append(fields, field{f.Name, kind, fallback, extra})
	}
	return Template("schema", map[string]any{
		"name":   t.Name(),
		"pk":     pk,
		"fields": fields,
		"rest":   strings.Join(rest, ", "),
		"raw":    raw,
	})
}

func RowMap[T any](v T) (string, any, map[string]any) {
	rv, t := reflect.ValueOf(v), reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		rv, t = rv.Elem(), t.Elem()
	}
	kvs, idK, idV := make(map[string]any, t.NumField()), "", any(nil)
	for i := 0; i < t.NumField(); i++ {
		f, v := t.Field(i), rv.Field(i).Interface()
		if sq := f.Tag.Get("sq"); strings.Contains(sq, " AS (") {
			continue
		} else if k := f.Name; k == "ID" || k == "RowID" {
			idK, idV = k, v
		} else if jsonDefault(f.Type) != "" {
			kvs[k] = &JSON{v}
		} else {
			kvs[k] = v
		}
	}
	return idK, idV, kvs
}

func Tables(c Connection, caseInsensitive bool) (map[string][]string, error) {
	sql := `SELECT name, (SELECT group_concat(name) FROM pragma_table_info(tl.name)) as columns
            FROM pragma_table_list tl
            WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != '_migrations'`
	ts, err := QueryMap[string](c, sql)
	m := map[string][]string{}
	for _, t := range ts {
		if !caseInsensitive {
			m[t["name"]] = strings.Split(t["columns"], ",")
		} else {
			m[strings.ToLower(t["name"])] = strings.Split(strings.ToLower(t["columns"]), ",")
		}
	}
	return m, err
}

func Copy(name string, oldDB, newDB *DB) error {
	oldTables, err := Tables(oldDB, true)
	if err != nil {
		return fmt.Errorf("failed to list tables to rebuild: %w", err)
	}
	if _, err := oldDB.Exec("PRAGMA journal_mode=delete"); err != nil {
		return fmt.Errorf("failed to ensure default journal mode: %w", err)
	}
	if err := oldDB.Close(); err != nil {
		return fmt.Errorf("failed to close old db: %w", err)
	}
	newTX, err := newDB.Begin()
	if err != nil {
		return fmt.Errorf("failed to open rebuild tx: %w", err)
	}
	defer newTX.Rollback()
	newTables, err := Tables(newDB, true)
	if err != nil {
		return fmt.Errorf("failed to list tables to rebuild: %w", err)
	}
	if _, err := newTX.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS old", name)); err != nil {
		return fmt.Errorf("failed to attach existing db: %w", err)
	}
	for name, oldCols := range oldTables {
		newCols := newTables[name]
		if len(newCols) == 0 {
			continue
		}
		cols := []string{}
		for _, c := range newCols {
			if slices.Contains(oldCols, c) {
				cols = append(cols, c)
			}
		}
		sql := fmt.Sprintf(`INSERT INTO %[1]s (%[2]s) SELECT %[2]s FROM old.%[1]s`,
			name, strings.Join(cols, ","))
		if _, err := newTX.Exec(sql); err != nil {
			return fmt.Errorf("failed to copy data from table %q: %w", name, err)
		}
	}
	if err := newTX.Commit(); err != nil {
		return fmt.Errorf("failed to commit rebuild tx: %w", err)
	}
	return nil
}

func FTSIndex(name, table, id, tokenizer string, cols ...string) string {
	return Template("fts", map[string]any{
		"name":      name,
		"id":        id,
		"table":     table,
		"tokenizer": tokenizer,
		"cols":      cols,
	})
}

func (a Args) Render(tpl string) (string, []any, error) {
	a = maps.Clone(a)
	if a["args"] != nil {
		return "", nil, fmt.Errorf("special kv Args.args must not be set")
	}
	a["args"] = []any{}
	tpl = sqlLineIfRe.ReplaceAllString(tpl, "{if $2} $1 {end}")
	tpl = sqlBindRe.ReplaceAllString(tpl, `{bind "$1"}`)
	tpl = sqlQuoteRe.ReplaceAllString(tpl, `{quote "$1"}`)
	t, err := template.New("").Delims("{", "}").Option("missingkey=error").Funcs(template.FuncMap{
		"quote":  a.quote,
		"bind":   a.bind,
		"values": a.values,
		"cols":   a.cols,
		"set":    a.set,
		"raw":    a.raw,
	}).Parse(tpl)
	if err != nil {
		return "", nil, err
	}
	w := &strings.Builder{}
	if err := t.Execute(w, a); err != nil {
		return "", nil, err
	}
	if a["_debug"] == true {
		log.Printf("DEBUG: sq.Args Render:\n%s (%v)", w.String(), a["args"])
	}
	return w.String(), a["args"].([]any), nil
}

func (a Args) vals(k, sep string) (string, error) {
	kvs, ks, qs := a[k].(map[string]any), []string{}, []string{}
	for k, v := range kvs {
		if !sqlNameRe.MatchString(k) {
			return "", fmt.Errorf("vals: %s is not a valid column name", k)
		}
		ks, qs, a["args"] = append(ks, k), append(qs, "$"+k), append(a["args"].([]any), sql.Named(k, v))
	}
	return fmt.Sprintf("(%s) %s (%s)", strings.Join(ks, ","), sep, strings.Join(qs, ",")), nil
}

func (a Args) values(k string) (string, error) {
	return a.vals(k, "VALUES")
}

func (a Args) set(k string) (string, error) {
	return a.vals(k, "=")
}

func (a Args) cols(k string) (string, error) {
	cols := slices.Compact(slices.Sorted(slices.Values(a[k].([]string))))
	for i, c := range cols {
		if c, err := a.ident(c); err != nil {
			return "", err
		} else {
			cols[i] = c
		}
	}
	return strings.Join(cols, ", "), nil
}

func (a Args) ident(v string) (string, error) {
	if !sqlNameRe.MatchString(v) {
		return "", fmt.Errorf("(%q) is not a valid sql identifier", v)
	}
	return "`" + v + "`", nil
}

func (a Args) raw(k string) string {
	return a[k].(string)
}

func (a Args) quote(k string) (string, error) {
	return a.ident(a[k].(string))
}

func (a Args) bind(k string) (string, error) {
	v, ok := a[k]
	if !ok {
		return "", fmt.Errorf("bind: %q not found", k)
	}
	a["args"] = append(a["args"].([]any), sql.Named(k, v))
	return "$" + k, nil
}

func (v *JSON) Scan(src any) error {
	switch src := src.(type) {
	case nil:
		return nil
	case []byte:
		return json.Unmarshal(src, v.V)
	case string:
		return json.Unmarshal([]byte(src), v.V)
	default:
		return fmt.Errorf("unsupported JSON scan %T => %T", src, v.V)
	}
}

func (v *JSON) Value() (driver.Value, error) {
	bs, err := json.Marshal(v.V)
	return string(bs), err
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

func timeDT(duration string) (string, error) {
	d, err := time.ParseDuration(duration)
	if err != nil {
		return "", err
	}
	return time.Now().Add(d).Format(time.RFC3339Nano), nil
}

func normalizedCol(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", ""))
}

func jsonDefault(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == typeTime {
		return ""
	}
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		return "{}"
	case reflect.Slice, reflect.Array:
		return "[]"
	}
	return ""
}
