//go:build goexperiment.jsonv2

package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json/v2"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	"golang.org/x/exp/slices"
)

type Context struct {
	*H
	*http.Request
	http.ResponseWriter
	*template.Template
	*bytes.Buffer
}

type H struct {
	T *template.Template
	fs.FS
	Dev bool
	http.ServeMux
}

var TemplateExitErr = fmt.Errorf("render partial template")
var TemplateHandledErr = fmt.Errorf("skip template rendering")
var camelToKebabRe = regexp.MustCompile("([a-z0-9])([A-Z])")

func (h *H) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Dev {
		ts, err := h.Compile()
		if err != nil {
			w.WriteHeader(500)
			fmt.Fprintf(w, "%v", err)
			return
		}
		h.Register(ts)
	}
	h.ServeMux.ServeHTTP(w, r)
}

func NewHandler(t *template.Template, tfs fs.FS, dev bool) *H {
	h := &H{T: t, FS: tfs, Dev: dev}
	if !h.Dev {
		ts, err := h.Compile()
		if err != nil {
			panic(err)
		}
		h.Register(ts)
	}
	return h
}

func (h *H) ServeTemplate(t *template.Template, pathKeys []string, w http.ResponseWriter, r *http.Request) {
	if c, err := h.NewContext(t, pathKeys, w, r); err != nil {
		c.Respond(400, []byte(err.Error()))
	} else if err := c.Execute(c.Buffer, c); errors.Is(err, TemplateHandledErr) {
	} else if err == nil || errors.Is(err, TemplateExitErr) {
		c.Respond(200, c.Bytes())
	} else {
		c.Respond(500, []byte(err.Error()))
	}
}

func (h *H) NewContext(t *template.Template, pathKeys []string, w http.ResponseWriter, r *http.Request) (*Context, error) {
	c := &Context{h, r, w, t, &bytes.Buffer{}}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(int64(10 * 1e6)); err != nil {
			return c, err
		}
	} else if err := r.ParseForm(); err != nil {
		return c, fmt.Errorf("failed to parse form: %w", err)
	}
	if len(r.PostForm) != 0 {
		r.Form = r.PostForm
	}
	for _, p := range pathKeys {
		c.Form[p] = []string{c.PathValue(p)}
	}
	return c, nil
}

func (c *Context) Respond(statusCode int, bs []byte) {
	if w := c.ResponseWriter; c.AcceptsEncoding("gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(statusCode)
		gzw := gzip.NewWriter(w)
		defer gzw.Close()
		gzw.Write(bs)
	} else {
		w.WriteHeader(statusCode)
		w.Write(bs)
	}
}

func (c *Context) AcceptsEncoding(enc string) bool {
	xs := strings.Split(c.Request.Header.Get("Accept-Encoding"), ",")
	for _, x := range xs {
		x = strings.ToLower(strings.TrimSpace(strings.SplitN(x, ";", 2)[0]))
		if x == enc {
			return true
		}
	}
	return false
}

func (c *Context) Get(k string) any {
	return c.Form.Get(k)
}

func (c *Context) Set(k, v string) string {
	c.Form.Set(k, v)
	return ""
}

func (c *Context) IsFragment() bool {
	return c.Request.Header.Get("Sec-Fetch-Dest") != "document"
}

func (c *Context) InvalidateBFCache() {
	// bfcache ignores cache-control: no-store (ccns) *by design*;
	// setting a http-only secure cookie AND setting ccns
	// forces bfcache eviction; note updating the cookie alone is not enough even though
	// it really should be. I can't follow their reasoning for not providing a way to
	// opt out of this via the cache-control header nor why no-cache / no-store are reasonable
	// to ignore.
	// https://github.com/fergald/explainer-bfcache-ccns
	// https://web.dev/articles/bfcache
	c.ResponseWriter.Header().Set("Cache-Control", "no-store")
	http.SetCookie(c.ResponseWriter, &http.Cookie{Name: "no-store", Secure: true, HttpOnly: true})
}

func (c *Context) Exit() (any, error) {
	return nil, TemplateExitErr
}

func (c *Context) JSON(code int, v any) (any, error) {
	bs, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	c.ResponseWriter.Header().Set("Content-Type", "application/json")
	c.Respond(code, bs)
	return nil, TemplateHandledErr
}

func (c *Context) Redirect(code int, url string) error {
	if c.IsFragment() {
		c.ResponseWriter.Header().Set("x-redirect", url)
	} else {
		http.Redirect(c.ResponseWriter, c.Request, url, code)
	}
	return TemplateHandledErr
}

func (c *Context) Query(kvs ...string) any {
	return query(c.Form.Encode(), kvs...)
}

func (c *Context) Decode(v any) error {
	rv := reflect.ValueOf(v).Elem()
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		ft, fv := rt.Field(i), rv.Field(i)
		tag := ft.Tag.Get("web")
		if tag == "-" {
			continue
		}
		k := strings.ToLower(ft.Name[:1]) + ft.Name[1:]
		if _, ok := c.Form[k]; !ok {
			k = strings.ToLower(camelToKebabRe.ReplaceAllString(ft.Name, "${1}-${2}"))
		}
		if vs, ok := c.Form[k]; ok && len(vs) > 0 {
			if err := setFormValue(fv, vs); err != nil {
				return err
			}
		} else if tag == "required" {
			return fmt.Errorf("form is missing field %q", ft.Name)
		}
	}
	return nil
}

func setFormValue(rv reflect.Value, vs []string) error {
	switch rv.Kind() {
	case reflect.Slice:
		rv.Set(reflect.MakeSlice(rv.Type(), len(vs), len(vs)))
		for i := range vs {
			f2 := reflect.New(rv.Type().Elem()).Elem()
			if err := setFormValue(f2, vs[i:i+1]); err != nil {
				return err
			}
			rv.Index(i).Set(f2)
		}
	case reflect.String:
		rv.SetString(vs[0])
	case reflect.Bool:
		switch strings.ToLower(vs[0]) {
		case "on", "true", "1":
			rv.SetBool(true)
		case "off", "false", "0":
			rv.SetBool(false)
		default:
			return fmt.Errorf("invalid bool: %q", vs[0])
		}
	default:
		return json.Unmarshal([]byte(vs[0]), rv.Addr().Interface(),
			json.MatchCaseInsensitiveNames(true))
	}
	return nil
}

func query(q string, kvs ...string) any {
	m, _ := url.ParseQuery(q)
	for i := 0; i < len(kvs)-1; i += 2 {
		k, v := kvs[i], kvs[i+1]
		switch k[0] {
		case '~':
			m[k[1:]] = []string{strings.ReplaceAll(v, "%s", strings.Join(m[k[1:]], " "))}
		case '+':
			m[k[1:]] = append(m[k[1:]], v)
			slices.Sort(m[k[1:]])
			m[k[1:]] = slices.Compact(m[k[1:]])
		case '-':
			m[k[1:]] = slices.DeleteFunc(m[k[1:]], func(v2 string) bool { return v == v2 })
		default:
			m[k] = []string{v}
		}
	}
	return template.URL(m.Encode())
}
