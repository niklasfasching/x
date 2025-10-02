package component

import (
	"html/template"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"text/template/parse"

	"github.com/niklasfasching/x/snap"
)

func TestCompile(t *testing.T) {
	m := map[string]any{
		"bool":  true,
		"map":   map[string]string{"k": "v"},
		"name":  "_name_",
		"class": "_class_",
		"text":  "_text_",
	}
	fs, err := filepath.Glob("testdata/*.case.gohtml")
	if err != nil {
		t.Fatal(err)
	}

	tplInline := template.Must(template.ParseFiles(fs...))
	tplOutline := template.Must(template.ParseFiles(fs...))
	if err := Compile(tplInline, true); err != nil {
		t.Fatal(err)
	} else if err := Compile(tplOutline, false); err != nil {
		t.Fatal(err)
	}
	for _, tpli := range tplInline.Templates() {
		if k := tpli.Name(); strings.HasPrefix(k, "test-") {
			t.Run(k, func(t *testing.T) {
				tplo := tplOutline.Lookup(tpli.Name())
				wi, wo := &strings.Builder{}, &strings.Builder{}
				if err := tpli.Execute(wi, m); err != nil {
					t.Fatal(err)
				} else if err := tplo.Execute(wo, m); err != nil {
					t.Fatal(err)
				}
				tn, ok := tplo.Tree.Root.Nodes[1].(*parse.TemplateNode)
				if !ok {
					t.Fatalf("Unexpectedly missing template node: %#v", tplo.Tree.Root)
				}
				snap.Snap(t, snap.TXT{Extension: ".in.gohtml"}, tpli.Tree.Root)
				snap.Snap(t, snap.TXT{Extension: ".ou.gohtml"},
					list(tplo.Tree.Root, tplo.Lookup(tn.Name).Tree.Root))
				snap.Snap(t, snap.TXT{Extension: ".html"}, wi.String())
				if wi.String() != wo.String() {
					t.Fatal("mismatched outputs for", tpli.Name())
				}
			})
		}
	}
}

func TestCompileHTML(t *testing.T) {
	tpl, err := template.ParseFiles("testdata/html.html")
	if err != nil {
		t.Fatal(err)
	}
	c := &Context{
		Placeholders: map[string]parse.Node{},
		delimLeft:    "{", delimRight: "}",
	}
	ts := c.Tokenize(tpl.Tree.Root)
	for i, t := range ts {
		ts[i], _ = c.compileToken(t)
	}
	n := c.Parse(ts)
	snap.Snap(t, snap.JSON{}, map[string]any{
		"tokens":   ts,
		"template": n.String(),
	})
}

func TestDebugTemplate(t *testing.T) {
	t.Skip() // manual
	tpl, err := template.New("").Parse(`<input {{if .}}checked{{end}}>
    `)
	if err != nil {
		t.Fatal(err)
	}
	log.Println(tpl.Tree.Root)
}
