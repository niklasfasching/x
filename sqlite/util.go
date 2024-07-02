package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/niklasfasching/x/geo"
)

type Connection interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
	Exec(query string, args ...interface{}) (sql.Result, error)
}

type JSON struct{ Value interface{} }

type PureFunc interface{}

var defaultFuncs = map[string]interface{}{
	"json_includes":  PureFunc(jsonIncludes),
	"regexp_extract": PureFunc(regexpExtract),
	"geo_haversine":  PureFunc(geo.Haversine),
	"geo_offset_lat": PureFunc(offsetLat),
	"geo_offset_lng": PureFunc(offsetLng),
}

var regexpExtractRegexps = map[string]*regexp.Regexp{}

func Print(db *DB, debug bool, query string, args ...interface{}) error {
	start := time.Now()
	if debug {
		if err := printQuery(os.Stderr, db, "explain query plan "+query, args...); err != nil {
			return err
		}
	}
	if err := printQuery(os.Stdout, db, query, args...); err != nil {
		return err
	}
	if debug {
		fmt.Fprintf(os.Stderr, `{"time": %q}`+"\n", time.Since(start))
	}
	return nil
}

func printQuery(w io.Writer, db *DB, query string, args ...interface{}) error {
	j := json.NewEncoder(w)
	j.SetIndent("", "  ")
	j.SetEscapeHTML(false)
	rows, err := db.Query(query, args...)
	if err != nil {
		return err
	}
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		for i := range values {
			values[i] = new(interface{})
		}
		if err := rows.Scan(values...); err != nil {
			return err
		}
		m := map[string]interface{}{}
		for i, k := range columns {
			m[k] = values[i]
		}

		if err := j.Encode(m); err != nil {
			return err
		}
	}
	return rows.Err()
}

func Query(c Connection, queryString string, result interface{}, args ...interface{}) error {
	if err := query(c, queryString, result, args...); err != nil {
		return fmt.Errorf("%s: %s", queryString, err)
	}
	return nil
}

func Exec(c Connection, queryString string, args ...interface{}) (sql.Result, error) {
	result, err := c.Exec(queryString, args...)
	if err != nil {
		err = fmt.Errorf("%s: %s", queryString, err)
	}
	return result, err
}

func Insert(c Connection, table string, v interface{}, or string) (sql.Result, error) {
	rv, ks, qs, vs := reflect.ValueOf(v), []string{}, []string{}, []interface{}{}
	add := func(k string, v reflect.Value) error {
		ks = append(ks, k)
		qs = append(qs, "?")
		switch v.Kind() {
		case reflect.Slice, reflect.Struct, reflect.Map:
			bs, err := json.Marshal(v.Interface())
			if err != nil {
				return err
			}
			vs = append(vs, string(bs))
		default:
			vs = append(vs, v.Interface())
		}
		return nil
	}
	switch rv.Kind() {
	case reflect.Map:
		for m := rv.MapRange(); m.Next(); {
			if err := add(m.Key().String(), m.Value().Elem()); err != nil {
				return nil, err
			}
		}
	case reflect.Struct:
		for i, rt := 0, rv.Type(); i < rv.NumField(); i++ {
			if err := add(rt.Field(i).Name, rv.Field(i)); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("unhandled type %T", v)
	}
	query := fmt.Sprintf("INSERT %s INTO %s (%s) VALUES (%s)", or, table, strings.Join(ks, ", "), strings.Join(qs, ", "))
	return c.Exec(query, vs...)
}

func query(c Connection, query string, result interface{}, args ...interface{}) error {
	xs := reflect.ValueOf(result)
	if xs.Kind() != reflect.Ptr || xs.Type().Elem().Kind() != reflect.Slice {
		return fmt.Errorf("cannot unmarshal query results into %t (%v)", result, result)
	}
	rows, err := c.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if err := unmarshal(rows, xs.Elem()); err != nil {
		return err
	}
	return rows.Err()
}

func unmarshal(rows *sql.Rows, xs reflect.Value) error {
	t, isPtr := xs.Type().Elem(), false
	switch t.Kind() {
	case reflect.Ptr:
		t, isPtr = t.Elem(), true
		fallthrough
	case reflect.Struct:
		return unmarshalStruct(rows, xs, t, isPtr)
	case reflect.Interface:
		t = reflect.TypeOf(map[string]interface{}{})
		fallthrough
	case reflect.Map:
		return unmarshalMap(rows, xs, t, isPtr)
	default:
		for rows.Next() {
			x := reflect.New(t)
			if err := scan(rows, []interface{}{x.Interface()}); err != nil {
				return err
			}
			if !isPtr {
				x = x.Elem()
			}
			xs.Set(reflect.Append(xs, x))
		}
	}
	return nil
}

func unmarshalStruct(rows *sql.Rows, xs reflect.Value, t reflect.Type, isPtr bool) error {
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	for rows.Next() {
		x := reflect.New(t).Elem()
		values := []interface{}{}
		for _, column := range columns {
			field := x.FieldByName(column) // TODO: use tags / case conversion for struct -> sql field mapping
			if field.IsValid() {
				values = append(values, field.Addr().Interface())
			} else {
				values = append(values, new(interface{}))
			}
		}
		if err = scan(rows, values); err != nil {
			return err
		}
		if isPtr {
			x = x.Addr()
		}
		xs.Set(reflect.Append(xs, x))
	}
	return nil
}

func unmarshalMap(rows *sql.Rows, xs reflect.Value, t reflect.Type, isPtr bool) error {
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	mt := reflect.MapOf(reflect.TypeOf(""), t.Elem())
	values := []interface{}{}
	for range columns {
		values = append(values, reflect.New(t.Elem()).Interface())
	}
	for rows.Next() {
		if err = scan(rows, values); err != nil {
			return err
		}
		x := reflect.MakeMapWithSize(mt, len(columns))
		for i, column := range columns {
			x.SetMapIndex(reflect.ValueOf(column), reflect.ValueOf(values[i]).Elem())
		}
		if isPtr {
			x = x.Addr()
		}
		xs.Set(reflect.Append(xs, x))
	}
	return nil
}

func scan(rows *sql.Rows, values []interface{}) error {
	tmp := make([]interface{}, len(values))
	for i := range values {
		tmp[i] = new(interface{})
	}
	if err := rows.Scan(tmp...); err != nil {
		return err
	}
	for i := range values {
		if err := convert(tmp[i], values[i]); err != nil {
			return err
		}
	}

	return nil
}

func convert(src, dst interface{}) error {
	bs, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(bs, dst)
}

func (j JSON) MarshalJSON() ([]byte, error) {
	switch s, ok := j.Value.(string); {
	case ok && isJSONArrayString(s):
		xs := []JSON{}
		if err := json.Unmarshal([]byte(s), &xs); err != nil {
			break
		}
		return json.Marshal(xs)
	case ok && isJSONObjectString(s):
		xs := map[string]JSON{}
		if err := json.Unmarshal([]byte(s), &xs); err != nil {
			break
		}
		return json.Marshal(xs)
	}
	return json.Marshal(j.Value)
}

func (j *JSON) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, &j.Value)
}

func isJSONObjectString(s string) bool {
	return len(s) >= 2 && s[0] == '{' && s[len(s)-1] == '}'
}

func isJSONArrayString(s string) bool {
	return len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']'
}

func ReadMigrations(directory string) (map[string]string, error) {
	m := map[string]string{}
	sqlFiles, err := filepath.Glob(filepath.Join(directory, "*.sql"))
	if err != nil {
		return nil, err
	}
	for _, sqlFile := range sqlFiles {
		bs, err := ioutil.ReadFile(sqlFile)
		if err != nil {
			return nil, err
		}
		m[sqlFile] = string(bs)
	}
	return m, nil
}

func jsonIncludes(s string, vs ...interface{}) (bool, error) {
	m, xs := map[string]bool{}, []interface{}{}
	if err := json.Unmarshal([]byte(s), &xs); err != nil {
		return false, err
	}
	for _, v := range vs {
		m[fmt.Sprintf("%v", v)] = true
	}
	for _, x := range xs {
		delete(m, fmt.Sprintf("%v", x))
	}
	return len(m) == 0, nil
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

func offsetLat(lat, lng, bearing, km float64) float64 {
	lat2, _ := geo.Offset(lat, lng, bearing, km)
	return lat2
}

func offsetLng(lat, lng, bearing, km float64) float64 {
	_, lng2 := geo.Offset(lat, lng, bearing, km)
	return lng2
}
