package htmpl

import (
	"cmp"
	"crypto/sha256"
	"fmt"
	"html/template"
	"io"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template/parse"

	"golang.org/x/net/html"
)

type Compiler struct {
	*Context
	Calls       map[string][]string
	Sources     map[string]string
	processElem func(c *Compiler, p *Frame, n *Node)
}

type Template struct {
	*template.Template
	Sources map[string]string
}

var templateErrRe = regexp.MustCompile(`template: (.*?):(\d+):`)

func (t *Template) Execute(w io.Writer, data any) error {
	return t.WrapError(t.Template.Execute(w, data))
}

func (t *Template) Lookup(name string) *Template {
	tpl := t.Template.Lookup(name)
	if tpl == nil {
		return nil
	}
	return &Template{tpl, t.Sources}
}

func (t *Template) WrapError(err error) error {
	if err == nil {
		return nil
	}
	m := templateErrRe.FindStringSubmatch(err.Error())
	if m == nil {
		return err
	}
	name, lineStr := m[1], m[2]
	line, _ := strconv.Atoi(lineStr)
	src, ok := t.Sources[name]
	if !ok {
		return err
	}
	lines := strings.Split(src, "\n")
	start, end := max(0, line-10), min(len(lines), line+10)
	w := &strings.Builder{}
	fmt.Fprintf(w, "%v\n\n", err)
	for i := start; i < end; i++ {
		marker := "  "
		if i+1 == line {
			marker = "> "
		}
		fmt.Fprintf(w, "%s%3d | %s\n", marker, i+1, lines[i])
	}
	return fmt.Errorf("%s", w.String())
}

type Frame struct {
	Slots map[string]*Node
	Calls map[string]int
	*Node
	Root bool
}

var isComponentTplName = regexp.MustCompile(`^<[\w-]+>$`).MatchString
var isAssetTplName = regexp.MustCompile(`^\[[\w-]+\]$`).MatchString

func NewCompiler(processElem func(c *Compiler, f *Frame, n *Node)) *Compiler {
	return &Compiler{New(), map[string][]string{}, map[string]string{}, processElem}
}

// Compile expands syntax sugar for html in a template and all its subtemplates.
// - html tag based template calls for for html tags with corresponding <$name> template definitions
//   i.e. `<foo a="1" b="{{ true }}" />` becomes `{{ template "<foo>" {a: "1", b: true} }}`
// Inlining templates technically "leaks" variables of the surrounding scope of the callsite
// to the component template. However, templates are first parsed and compiled un-inlined
// by html/template, and references to variables outside the scope of the component template
// thus cause compile errors and thus prevent abuse of the scope leak.
func (c *Compiler) Compile(tpl *template.Template) (t *Template, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = r.(error)
		}
	}()
	tpl.Delims("{{", "}}").Funcs(DefaultFuncs)
	for _, tpl := range c.SortedTemplates(tpl.Templates()) {
		name := tpl.Name()
		if !isComponentTplName(name) {
			continue
		}
		nt, rest := &Node{}, []*Node{}
		prefix := "$_" + kebabToCamel(name[1:len(name)-1]) + "_"
		c.NamespaceVars(tpl.Tree.Root, prefix)
		for _, n := range c.ParseList(tpl.Tree.Root) {
			if n.Tag == "template" && nt.Tag != "" {
				panic(fmt.Errorf("component %q must have one <template>", name))
			} else if n.Tag == "template" {
				nt = n
			} else {
				rest = append(rest, n)
			}
		}
		if nt.Tag == "" {
			panic(fmt.Errorf("component %q must have one <template>", name))
		} else if nt.Attr("component").Val == "true" {
			nt.Tag = name[1 : len(name)-1]
		} else {
			nt.Type = FragmentNode
		}
		c.Sources[name] = c.RenderHTML(nt)
		if _, err := tpl.Parse(c.Sources[name]); err != nil {
			panic(fmt.Errorf(`failed to parse component %q: %w`, name, err))
		}
		if _, err := tpl.New("[assets]" + name).Parse(c.RenderHTML(rest...)); err != nil {
			panic(fmt.Errorf(`failed to parse component %q: %w`, "[assets]"+name, err))
		}
	}
	for _, tpl := range c.SortedTemplates(tpl.Templates()) {
		if tpl.Tree == nil {
			continue
		}
		k, ns := strings.TrimPrefix(tpl.Name(), "[assets]"), c.ParseList(tpl.Tree.Root)
		f := &Frame{Node: &Node{Tag: tpl.Name()}, Calls: map[string]int{}, Root: true}
		c.Walk(tpl, ns, f)
		c.Calls[k] = append(slices.Sorted(maps.Keys(f.Calls)), c.Calls[k]...)
		h := sha256.New()
		h.Write([]byte(tpl.Tree.Root.String()))
		c.Sources[tpl.Name()] = c.RenderHTML(ns...)
		if _, err := tpl.Parse(c.Sources[tpl.Name()]); err != nil {
			panic(fmt.Errorf(`failed to parse %q: %w`, tpl.Name(), err))
		}
		tpl.Tree.ParseName = fmt.Sprintf("%s (%.12x)", tpl.Tree.ParseName, h.Sum(nil))
		c.Sources[tpl.Tree.ParseName] = c.Sources[tpl.Name()]
	}
	c.resolveCalls()
	w := &strings.Builder{}
	for i, name := range slices.Sorted(maps.Keys(c.Calls)) {
		if i == 0 {
			fmt.Fprintf(w, "{{if eq . %q}}\n", name)
		} else {
			fmt.Fprintf(w, "{{else if eq . %q}}\n", name)
		}
		for _, name := range c.Calls[name] {
			if isComponentTplName(name) {
				fmt.Fprintf(w, "{{template %q .}}\n", "[assets]"+name)
			} else if isAssetTplName(name) && name != "[assets]" {
				fmt.Fprintf(w, "{{template %q .}}\n", name)
			}
		}
	}
	if len(c.Calls) > 0 {
		fmt.Fprintf(w, `{{else}} "{{.}}" {{index "unknown [asset]" -1}}{{end}}`)
	}
	c.Sources["[assets]"] = w.String()
	if _, err := tpl.New("[assets]").Parse(c.Sources["[assets]"]); err != nil {
		panic(fmt.Errorf(`failed to parse "[assets]": %w`, err))
	}
	return &Template{tpl, c.Sources}, nil
}

func (c *Compiler) Walk(tpl *template.Template, ns []*Node, f *Frame) {
	for _, n := range ns {
		if n.Type != FragmentNode && n.Type != html.ElementNode {
			continue
		} else if tpl.Lookup("<"+n.Tag+">") != nil && n.Attr("component").Val != "true" {
			c.inlineComponent(tpl, n, f)
		} else if n.Tag == "t:template" {
			f.Calls[n.Attr("name").Val]++
			if name := n.Attr("name").Val; isAssetTplName(name) && name != "[assets]" {
				*n = Node{Type: FragmentNode}
			}
		} else if n.Tag == "t:action" {
			if pipe := c.Placeholders[n.Attr("pipe").Val]; pipe == nil {
				panic(fmt.Errorf("non-placeholder pipe: %q", n.Attr("pipe").Val))
			} else if name, ok := strings.CutPrefix(pipe.String(), ".slots."); !ok {
				continue
			} else if slot, ok := f.Slots[name]; ok {
				*n = *c.BranchNode("with", "$dot", slot.Children, nil)
			} else if !f.Root {
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
	c.Walk(tpl, n.Children, f)
	for _, n := range n.Children {
		if n.Tag != "slot" {
			slots["rest"].Children = append(slots["rest"].Children, n)
		} else if k := n.Attr("name").Val; slots[k] != nil {
			panic(fmt.Errorf("duplicate slot %q in %q", k, "<"+n.Tag+">"))
		} else {
			slots[k], slotNodes = n, append(slotNodes, n)
		}
	}
	w, attrs := &strings.Builder{}, []html.Attribute{}
	fmt.Fprintf(w, "(dict %q %q %q (list", "caller", f.Node.Tag, "slots")
	for _, n := range slotNodes {
		w.WriteString(" (dict")
		for _, a := range n.Attrs {
			fmt.Fprintf(w, " %s %s", c.ExpandString(a.Key), c.ExpandString(a.Val))
		}
		w.WriteString(")")
	}
	w.WriteString(")")
	for _, a := range n.Attrs {
		if p, ok := strings.CutPrefix(a.Key, "..."); ok && a.Val == "" && c.Placeholders[p] != nil {
			for v := range strings.FieldsSeq(c.ExpandString(p)) {
				ks := strings.Split(strings.TrimPrefix(v, "$"), ".")
				k := camelToKebab(ks[len(ks)-1])
				attrs = append(attrs, html.Attribute{Key: k, Val: c.FmtPlaceholder("{{%s}}", v)})
				fmt.Fprintf(w, " %q %s", k, v)
			}
		} else {
			fmt.Fprintf(w, " (%s) (%s)", c.ExpandString(a.Key), c.ExpandString(a.Val))
			attrs = append(attrs, a)
		}
	}
	componentNodes := c.ParseList(tpl.Lookup(name).Tree.Root)
	if len(componentNodes) > 0 && componentNodes[0].Tag == "template" {
		for _, a := range componentNodes[0].Attrs {
			if p, ok := strings.CutPrefix(a.Key, "..."); ok && a.Val == "" && c.Placeholders[p] != nil {
				for v := range strings.FieldsSeq(c.ExpandString(p)) {
					ks := strings.Split(strings.TrimPrefix(v, "$"), ".")
					k := camelToKebab(ks[len(ks)-1])
					fmt.Fprintf(w, " %q %s", k, v)
				}
			} else {
				fmt.Fprintf(w, " (%s) (%s)", c.ExpandString(a.Key), c.ExpandString(a.Val))
			}
		}
	}
	n.Attrs = attrs
	w.WriteString(")")
	c.Walk(tpl, componentNodes, &Frame{slots, f.Calls, n, false})
	*n = *c.BranchNode("with", "$dot := .",
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

func (c *Compiler) SortedTemplates(tpls []*template.Template) []*template.Template {
	slices.SortFunc(tpls, func(a, b *template.Template) int { return cmp.Compare(a.Name(), b.Name()) })
	return tpls
}

func (c *Compiler) NamespaceVars(n parse.Node, prefix string) {
	switch n := n.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, n := range n.Nodes {
			c.NamespaceVars(n, prefix)
		}
	case *parse.VariableNode:
		n.Ident[0] = prefix + n.Ident[0][1:]
	case *parse.BranchNode:
		c.NamespaceVars(n.Pipe, prefix)
		c.NamespaceVars(n.List, prefix)
		c.NamespaceVars(n.ElseList, prefix)
	case *parse.WithNode:
		c.NamespaceVars(&n.BranchNode, prefix)
	case *parse.IfNode:
		c.NamespaceVars(&n.BranchNode, prefix)
	case *parse.RangeNode:
		c.NamespaceVars(&n.BranchNode, prefix)
	case *parse.TemplateNode:
		c.NamespaceVars(n.Pipe, prefix)
	case *parse.PipeNode:
		if n == nil {
			return
		}
		for _, n := range n.Decl {
			c.NamespaceVars(n, prefix)
		}
		for _, n := range n.Cmds {
			c.NamespaceVars(n, prefix)
		}
	case *parse.CommandNode:
		for _, n := range n.Args {
			c.NamespaceVars(n, prefix)
		}
	case *parse.ActionNode:
		c.NamespaceVars(n.Pipe, prefix)
	case *parse.ChainNode:
		c.NamespaceVars(n.Node, prefix)
	}
}
