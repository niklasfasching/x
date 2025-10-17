package htmpl

import (
	"cmp"
	"fmt"
	"html/template"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
	"golang.org/x/net/html"
)

type Compiler struct {
	*Context
	Calls       map[string][]string
	processElem func(c *Compiler, p *Component, n *Node)
}

type Component struct {
	SlotDot string
	Slots   map[string]*Node
	*Node
}

var isComponentTplName = regexp.MustCompile(`^<[\w-]+>$`).MatchString

func NewCompiler(processElem func(c *Compiler, p *Component, n *Node), delimiters ...string) *Compiler {
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
		m := map[string]*Node{}
		for _, n := range c.ParseList(tpl.Tree.Root) {
			if n.Tag == "" {
				continue
			} else if m[n.Tag] != nil {
				panic(fmt.Errorf("duplicate tag %q", n.Tag))
			} else {
				m[n.Tag] = n
			}
		}
		nt, nsc, nst := m["template"], m["script"], m["style"]
		if nt == nil {
			panic(fmt.Errorf("component %q must have a <template>", name))
		} else if len(m) > 3 {
			panic(fmt.Errorf("component %q has too many nodes %v", name, maps.Keys(m)))
		}
		if nt.Attr("component") == "true" {
			nt.Tag = name[1 : len(name)-1]
			if nsc != nil {
				nsc.Attrs = append(nsc.Attrs, []html.Attribute{
					{Key: "type", Val: "module"}, {Key: "defer", Val: "true"}}...,
				)
			}
		} else {
			nt.Type = FragmentNode
		}
		if _, err := tpl.Parse(c.RenderHTML(nt)); err != nil {
			panic(fmt.Errorf(`failed to parse component %q: %w`, name, err))
		}
		if _, err := tpl.New("[assets]" + name).Parse(c.RenderHTML(nsc, nst)); err != nil {
			panic(fmt.Errorf(`failed to parse component %q: %w`, "[assets]"+name, err))
		}
	}
	tpls := tpl.Templates()
	slices.SortFunc(tpls, func(a, b *template.Template) int { return cmp.Compare(a.Name(), b.Name()) })
	for _, tpl := range tpls {
		ns, calls := c.ParseList(tpl.Tree.Root), map[string]int{}
		c.Walk(tpl, ns, calls, nil)
		c.Calls[tpl.Name()] = maps.Keys(calls)
		if _, err := tpl.Parse(c.RenderHTML(ns...)); err != nil {
			panic(fmt.Errorf(`failed to parse %q: %w`, tpl.Name(), err))
		}
	}
	c.resolveCalls()
	w := &strings.Builder{}
	for name := range c.Calls {
		fmt.Fprintf(w, "%s if eq . %q %s\n", c.L, name, c.R)
		for _, name := range c.Calls[name] {
			fmt.Fprintf(w, "%s template %q . %s\n", c.L, "[assets]"+name, c.R)
		}
		fmt.Fprintf(w, "%s end %s\n", c.L, c.R)
	}
	if _, err := tpl.New("[assets]").Parse(w.String()); err != nil {
		panic(fmt.Errorf(`failed to parse "[assets]": %w`, err))
	}
	return err
}

func (c *Compiler) Walk(tpl *template.Template, ns []*Node, calls map[string]int, p *Component) {
	for _, n := range ns {
		if n.Type != FragmentNode && n.Type != html.ElementNode {
			continue
		} else if tpl.Lookup("<"+n.Tag+">") != nil && n.Attr("component") != "true" {
			c.Walk(tpl, n.Children, calls, p)
			c.inlineComponent(tpl, n, calls)
		} else if n.Tag == "t:template" {
			calls[n.Attr("name")]++
		} else if n.Tag == "t:action" {
			if p == nil {
				continue
			} else if pipe := c.Placeholders[n.Attr("pipe")]; pipe == nil {
				panic(fmt.Errorf("non-placeholder pipe: %q", n.Attr("pipe")))
			} else if name, ok := strings.CutPrefix(pipe.String(), ".slots."); !ok {
				continue
			} else if slot, ok := p.Slots[name]; ok {
				*n = *c.BranchNode("with", p.SlotDot, slot.Children, nil)
			} else if p.SlotDot != "" {
				panic(fmt.Errorf("unknown slot: %q", name))
			}
		} else {
			if !strings.HasPrefix(n.Tag, "t:") {
				c.processElem(c, p, n)
			}
			c.Walk(tpl, n.Children, calls, p)
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

func (c *Compiler) inlineComponent(tpl *template.Template, n *Node, calls map[string]int) {
	name := "<" + n.Tag + ">"
	calls[name], c.id = calls[name]+1, c.id+1
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
	c.Walk(tpl, componentNodes, calls, &Component{slotDot, slots, n})
	*n = *c.BranchNode("with", slotDot+" := .",
		[]*Node{c.BranchNode("with", w.String(), componentNodes, nil)}, nil)

}

func (c *Compiler) resolveCalls() {
	resolved := map[string][]string{}
	var f func(string, map[string]bool) []string
	f = func(name string, path map[string]bool) []string {
		if calls, ok := resolved[name]; ok {
			return calls
		} else if _, ok := path[name]; ok {
			panic(fmt.Errorf("cycle: %s", name))
		}
		path[name] = true
		m := map[string]bool{}
		for _, name := range c.Calls[name] {
			if isComponentTplName(name) {
				m[name] = true
			}
			for _, name := range f(name, path) {
				if isComponentTplName(name) {
					m[name] = true
				}
			}
		}
		delete(path, name)
		calls := maps.Keys(m)
		sort.Strings(calls)
		resolved[name] = calls
		return calls
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
