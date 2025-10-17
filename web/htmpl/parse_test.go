package htmpl

import (
	"html/template"
	"strings"
	"testing"

	"github.com/niklasfasching/x/snap"
)

func TestCompile(t *testing.T) {
	data := map[string]any{
		"a": "value-of-a",
	}
	snap.Cases(t, "*.case.gohtml", func(t *testing.T, name string, bs []byte) {
		sHTML := snap.NewNamed(t, ".gohtml")
		sJSON := snap.NewNamed(t, ".json")
		tpl, err := template.New(name).Parse(string(bs))
		if err != nil {
			sHTML.KeyedSnap(t, "compiled", "err: "+err.Error())
			return
		}
		tpl.Funcs(DefaultFuncs)
		for _, tpl := range tpl.Templates() {
			if !strings.HasPrefix(tpl.Name(), "test-") {
				continue
			}
			t.Run(tpl.Name(), func(t *testing.T) {
				c := NewCompiler(ProcessDirectives)
				ns := c.ParseList(tpl.Tree.Root)
				sJSON.KeyedSnap(t, "nodes", ns)
				sHTML.KeyedSnap(t, "nodes-to-html", snap.HTML(c.RenderHTML(ns...)))
				if err := c.Compile(tpl); err != nil {
					t.Fatal(err)
				}
				sJSON.KeyedSnap(t, "calls", c.Calls)
				sHTML.KeyedSnap(t, "compiled", snap.HTML(tpl.Tree.Root.String()))
				for _, name := range c.Calls[tpl.Name()] {
					sHTML.KeyedSnap(t, "compiled:"+name, snap.HTML(tpl.Lookup(name).Tree.Root.String()))
					sJSON.KeyedSnap(t, "compiled:"+name, c.ParseList(tpl.Lookup(name).Tree.Root))
					if name := "[assets]" + name; tpl.Lookup(name) != nil {
						sHTML.KeyedSnap(t, "compiled:"+name, snap.HTML(tpl.Lookup(name).Tree.Root.String()))
						sJSON.KeyedSnap(t, "compiled:"+name, c.ParseList(tpl.Lookup(name).Tree.Root))
					}
				}
				w := &strings.Builder{}
				if err := tpl.Execute(w, data); err != nil {
					sHTML.KeyedSnap(t, "executed", "err: "+err.Error())
				} else {
					sHTML.KeyedSnap(t, "executed", snap.HTML(w.String()))
				}
			})
		}
	})
}
