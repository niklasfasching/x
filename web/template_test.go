package web

import (
	"html/template"
	"net/http"
	"net/url"
	"testing"
	"testing/fstest"

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
	snap.Snap(t, v)
}

func TestFindTemplateSets(t *testing.T) {
	fs, paths := fstest.MapFS{}, []string{
		"app.gohtml",
		"misc.gohtml",
		"misc.foo.gohtml", // should be ignored
		"_lib.gohtml",
		"_lib/misc.gohtml",
		"item/_lib.gohtml",
		"item/_lib/misc.gohtml",
		"item/main.gohtml",
		"other/main.gohtml",
	}
	for _, p := range paths {
		fs[p] = &fstest.MapFile{}
	}
	sets, err := findTemplateSets(fs, []string{".gohtml"})
	if err != nil {
		t.Fatal(err)
	}
	snap.Snap(t, sets)
}
