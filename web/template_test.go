package web

import (
	"bytes"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/niklasfasching/x/snap"
)

func TestQuery(t *testing.T) {
	c := &Context{Request: &http.Request{Form: url.Values{
		"id":   {"42"},
		"tags": {"foo", "bar"},
	}}}
	q, _ := url.QueryUnescape(string(c.Query(
		"id", "43",
		"+tags", "baz",
		"-tags", "foo",
		"oldid", "42",
	).(template.URL)))
	if q != "id=43&oldid=42&tags=bar&tags=baz" {
		t.Fatal("bad query replacement")
	}
}

func TestDecode(t *testing.T) {
	c := &Context{Request: &http.Request{Form: url.Values{
		"id":               {"42"},
		"tags":             {"foo", "bar"},
		"include-archived": {"true"},
		"excluded":         {"THIS MUST NOT BE IN THE SNAPSHOT"},
	}}}
	v := struct {
		ID              int
		Tags            []string
		IncludeArchived bool
		Excluded        string `web:"-"`
	}{}
	if err := c.Decode(&v); err != nil {
		t.Fatal("decode", err)
	}
	snap.Snap(t, snap.JSON{}, v)
}

func TestShortCode(t *testing.T) {
	base := template.Must(template.New("").Parse(""))
	ts, err := LoadTemplates(base, os.DirFS("./testdata/"), true)
	if err != nil {
		t.Fatal(err)
	}
	tpl := ts["el"]
	w := &bytes.Buffer{}
	if err := tpl.Lookup("el-test").Execute(w, nil); err != nil {
		t.Fatal(err)
	}
	snap.Snap(t, snap.TXT{}, w.String())
}

// func TestServe(t *testing.T) {
// 	db, err := sqlite.New(":memory:", []string{`
//       CREATE TABLE docs (title, tags);
//       INSERT INTO docs (title, tags) VALUES ('doc one', '[1, 2]');
//       INSERT INTO docs (title, tags) VALUES ('doc two', '[2, 3]');`,
// 	}, nil, nil)
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	tp := template.New("").Funcs(sqlite.FuncMap(db))
// 	h := TemplateHandler(tp, os.DirFS("."), true)
// 	m := &http.ServeMux{}
// 	m.Handle("/{template...}", h)
// 	patterns := []string{
// 		"/tpl/{template...}",
// 		"/b/o/{objectname...}",
// 		"/b/{#bucket}/o/{objectname...}/{$}",
// 	}
// 	// pathvalues, parser
// 	for _, pattern := range patterns {
// 		m.Handle(pattern, h)
// 		log.Println(pattern)
// 		for _, m := range pathPatternRe.FindAllStringSubmatch(pattern, -1) {
// 			log.Println("\t", m[1])

// 		}

// 	}

// 	// http.ListenAndServe(":5000", m)
// }
