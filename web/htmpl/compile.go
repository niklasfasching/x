package htmpl

import (
	"cmp"
	"fmt"
	"html/template"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"

	"golang.org/x/net/html"
)

type Compiler struct {
	*Context
	Calls       map[string][]string
	processElem func(c *Compiler, p *Frame, n *Node)
}

type Frame struct {
	SlotDot string
	Slots   map[string]*Node
	Calls   map[string]int
	*Node
}

var isComponentTplName = regexp.MustCompile(`^<[\w-]+>$`).MatchString
var isAssetTplName = regexp.MustCompile(`^\[[\w-]+\]$`).MatchString

func NewCompiler(processElem func(c *Compiler, f *Frame, n *Node), delimiters ...string) *Compiler {
	return &Compiler{New(delimiters...), map[string][]string{}, processElem}
}

// Compile expands syntax sugar for html in a template and all its subtemplates.
// - html tag based template calls for for html tags with corresponding <$name> template definitions
//   i.e. `<foo a="1" b="{{ true }}" />` becomes `{{ template "<foo>" {a: "1", b: true} }}`
// Inlining templates technically "leaks" variables of the surrounding scope of the callsite
// to the component template. However, templates are first parsed and compiled un-inlined
// by html/template, and references to variables outside the scope of the component template
// thus cause compile errors and thus prevent abuse of the scope leak.
func (c *Compiler) Compile(tpl *template.Template) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = r.(error)
		}
	}()
	tpl.Funcs(DefaultFuncs)
	for _, tpl := range tpl.Templates() {
		name := tpl.Name()
		if !isComponentTplName(name) {
			continue
		}
		nt, rest := &Node{}, []*Node{}
		for _, n := range c.ParseList(tpl.Tree.Root) {
			if n.Tag == "template" && nt.Tag != "" {
				panic(fmt.Errorf("component %q must have a <template>", name))
			} else if n.Tag == "template" {
				nt = n
			} else {
				rest = append(rest, n)
			}
		}
		if nt.Attr("component") == "true" {
			nt.Tag = name[1 : len(name)-1]
		} else {
			nt.Type = FragmentNode
		}
		if _, err := tpl.Parse(c.RenderHTML(nt)); err != nil {
			panic(fmt.Errorf(`failed to parse component %q: %w`, name, err))
		}
		if _, err := tpl.New("[assets]" + name).Parse(c.RenderHTML(rest...)); err != nil {
			panic(fmt.Errorf(`failed to parse component %q: %w`, "[assets]"+name, err))
		}
	}
	tpls := tpl.Templates()
	slices.SortFunc(tpls, func(a, b *template.Template) int { return cmp.Compare(a.Name(), b.Name()) })
	for _, tpl := range tpls {
		k, ns := strings.TrimPrefix(tpl.Name(), "[assets]"), c.ParseList(tpl.Tree.Root)
		f := &Frame{Node: &Node{Tag: tpl.Name()}, Calls: map[string]int{}}
		c.Walk(tpl, ns, f)
		c.Calls[k] = append(slices.Collect(maps.Keys(f.Calls)), c.Calls[k]...)
		if _, err := tpl.Parse(c.RenderHTML(ns...)); err != nil {
			panic(fmt.Errorf(`failed to parse %q: %w`, tpl.Name(), err))
		}
	}
	c.resolveCalls()
	w := &strings.Builder{}
	for _, name := range slices.Sorted(maps.Keys(c.Calls)) {
		fmt.Fprintf(w, "%s if eq . %q %s\n", c.L, name, c.R)
		for _, name := range c.Calls[name] {
			if isComponentTplName(name) {
				fmt.Fprintf(w, "%s template %q . %s\n", c.L, "[assets]"+name, c.R)
			} else if isAssetTplName(name) && name != "[assets]" {
				fmt.Fprintf(w, "%s template %q . %s\n", c.L, name, c.R)
			}
		}
		fmt.Fprintf(w, "%s end %s", c.L, c.R)
	}
	if _, err := tpl.New("[assets]").Parse(w.String()); err != nil {
		panic(fmt.Errorf(`failed to parse "[assets]": %w`, err))
	}
	return err
}

func (c *Compiler) Walk(tpl *template.Template, ns []*Node, f *Frame) {
	for _, n := range ns {
		if n.Type != FragmentNode && n.Type != html.ElementNode {
			continue
		} else if tpl.Lookup("<"+n.Tag+">") != nil && n.Attr("component") != "true" {
			c.Walk(tpl, n.Children, f)
			c.inlineComponent(tpl, n, f)
		} else if n.Tag == "t:template" {
			f.Calls[n.Attr("name")]++
			if name := n.Attr("name"); isAssetTplName(name) && name != "[assets]" {
				*n = Node{Type: FragmentNode}
			}
		} else if n.Tag == "t:action" {
			if pipe := c.Placeholders[n.Attr("pipe")]; pipe == nil {
				panic(fmt.Errorf("non-placeholder pipe: %q", n.Attr("pipe")))
			} else if name, ok := strings.CutPrefix(pipe.String(), ".slots."); !ok {
				continue
			} else if slot, ok := f.Slots[name]; ok {
				*n = *c.BranchNode("with", f.SlotDot, slot.Children, nil)
			} else if f.SlotDot != "" {
				panic(fmt.Errorf("unknown slot: %q", name))
			}
		} else {
			if !strings.HasPrefix(n.Tag, "t:") {
				c.processElem(c, f, n)
			}
			c.Walk(tpl, n.Children, f)
		}
	}
}

func (c *Compiler) SetAttrs(n *Node, kvs map[string]string) {
	updated, rest := []html.Attribute{}, []html.Attribute{}
	for _, a := range n.Attrs {
		if v, ok := kvs[a.Key]; ok {
			delete(kvs, a.Key)
			a.Val = v
			updated = append(updated, a)
		}
	}
	for k, v := range kvs {
		if k == "" {
			rest = append(rest, html.Attribute{Namespace: RawAttr, Val: v})
		} else {
			rest = append(rest, html.Attribute{Key: k, Val: v})
		}
	}
	sort.Slice(rest, func(i, j int) bool { return cmp.Less(rest[i].Key, rest[j].Key) })
	n.Attrs = append(updated, rest...)
}

func (c *Compiler) inlineComponent(tpl *template.Template, n *Node, f *Frame) {
	name := "<" + n.Tag + ">"
	f.Calls[name], c.id = f.Calls[name]+1, c.id+1
	slots, slotNodes := map[string]*Node{"rest": {}}, []*Node{}
	for _, n := range n.Children {
		if n.Tag != "slot" {
			slots["rest"].Children = append(slots["rest"].Children, n)
		} else if k := n.Attr("name"); slots[k] != nil {
			panic(fmt.Errorf("duplicate slot %q in %q", k, "<"+n.Tag+">"))
		} else {
			slots[k], slotNodes = n, append(slotNodes, n)
		}
	}
	w, slotDot := &strings.Builder{}, fmt.Sprintf("$_dot_%d", c.id)
	w.WriteString(`$ := (dict "slots" (list`)
	for _, n := range slotNodes {
		w.WriteString(" (dict")
		for _, a := range n.Attrs {
			fmt.Fprintf(w, " %s %s", c.ExpandString(a.Key), c.ExpandString(a.Val))
		}
		w.WriteString(")")
	}
	w.WriteString(")")
	for _, a := range n.Attrs {
		fmt.Fprintf(w, " %s %s", c.ExpandString(a.Key), c.ExpandString(a.Val))
	}
	w.WriteString(")")
	componentNodes := c.ParseList(tpl.Lookup(name).Tree.Root)
	f = &Frame{slotDot, slots, f.Calls, n}
	c.Walk(tpl, componentNodes, f)
	*n = *c.BranchNode("with", slotDot+" := .",
		[]*Node{c.BranchNode("with", w.String(), componentNodes, nil)}, nil)

}

func (c *Compiler) resolveCalls() {
	resolved := map[string][]string{}
	var f func(string, map[string]bool) []string
	f = func(name string, path map[string]bool) []string {
		if calls, ok := resolved[name]; ok {
			return calls
		} else if path[name] {
			panic(fmt.Errorf("cycle: %s", name))
		}
		path[name] = true
		m, names := map[string]bool{}, []string{}
		for _, name := range c.Calls[name] {
			for _, name := range f(name, path) {
				if (isComponentTplName(name) || isAssetTplName(name)) && !m[name] {
					m[name], names = true, append(names, name)
				}
			}
			if (isComponentTplName(name) || isAssetTplName(name)) && !m[name] {
				m[name], names = true, append(names, name)
			}
		}
		delete(path, name)
		resolved[name] = names
		return names
	}
	for name := range c.Calls {
		f(name, map[string]bool{})
	}
	for name, calls := range resolved {
		if len(calls) == 0 {
			delete(resolved, name)
		}
	}
	c.Calls = resolved
}
