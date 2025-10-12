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
	processElem func(c *Compiler, n *Node)
}

var DefaultFuncs = template.FuncMap{
	"list": func(vs ...any) any { return vs },
	"dict": func(kvs ...any) any {
		m := map[string]any{}
		for i := 0; i < len(kvs); i += 2 {
			if k, v := kvs[i].(string), kvs[i+1]; k == "..." && len(kvs) == 2 {
				return v // TODO: lowerCamel struct field names
			} else if k == "..." {
				panic(fmt.Errorf("'...' must not be used with other kvs: got %v", kvs))
			} else {
				k := kebabToCamelRe.ReplaceAllStringFunc(k, func(s string) string {
					return strings.ToUpper(s[1:])
				})
				m[k] = v
			}
		}
		return m
	},
}

var kebabToCamelRe = regexp.MustCompile("-(.)")
var isComponentTplName = regexp.MustCompile(`^<[\w-]+>$`).MatchString

func NewCompiler(f func(c *Compiler, n *Node), delimiters ...string) *Compiler {
	return &Compiler{New(delimiters...), map[string][]string{}, f}
}

// Compile expands syntax sugar for html in a template and all its subtemplates.
// - html tag based template calls for for html tags with dashes and corresponding <$name> definitions
//   i.e. `<tpl-foo a="1" b="{{ true }}" />` becomes `{{ template "<tpl-foo>" {a: "1", b: true} }}`
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
		templates, assets := c.partition(c.ParseList(tpl.Tree.Root), "template")
		if n := len(templates); n != 1 {
			panic(fmt.Errorf("component must have one <template>: %q (%d)", name, n))
		}
		if _, err := tpl.Parse(c.RenderHTML(templates[0].Children)); err != nil {
			panic(fmt.Errorf(`failed to parse component %q: %w`, name, err))
		}
		if _, err := tpl.New(name + "[assets]").Parse(c.RenderHTML(assets)); err != nil {
			panic(fmt.Errorf(`failed to parse component %q: %w`, name+"[assets]", err))
		}
	}
	tpls := tpl.Templates()
	slices.SortFunc(tpls, func(a, b *template.Template) int { return cmp.Compare(a.Name(), b.Name()) })
	for _, tpl := range tpls {
		ns, calls := c.ParseList(tpl.Tree.Root), map[string]int{}
		c.Walk(tpl, ns, calls, nil, "")
		c.Calls[tpl.Name()] = maps.Keys(calls)
		if _, err := tpl.Parse(c.RenderHTML(ns)); err != nil {
			panic(fmt.Errorf(`failed to parse %q: %w`, tpl.Name(), err))
		}
	}
	c.resolveCalls()
	w := &strings.Builder{}
	for name := range c.Calls {
		fmt.Fprintf(w, "%s if eq . %q %s\n", c.L, name, c.R)
		for _, name := range c.Calls[name] {
			fmt.Fprintf(w, "%s template %q . %s\n", c.L, name+"[assets]", c.R)
		}
		fmt.Fprintf(w, "%s end %s\n", c.L, c.R)
	}
	if _, err := tpl.New("[assets]").Parse(w.String()); err != nil {
		panic(fmt.Errorf(`failed to parse "[assets]": %w`, err))
	}
	return err
}

func (c *Compiler) Walk(tpl *template.Template, ns []*Node, calls map[string]int, slots map[string]*Node, slotDot string) {
	for _, n := range ns {
		if n.Type != html.ElementNode {
			continue
		} else if strings.Contains(n.Tag, "-") && tpl.Lookup("<"+n.Tag+">") != nil {
			c.Walk(tpl, n.Children, calls, slots, slotDot)
			c.inlineComponent(tpl, n, calls)
		} else if n.Tag == "t:template" {
			calls[n.Attr("name")]++
		} else if n.Tag == "t:action" {
			if pipe := c.Placeholders[n.Attr("pipe")]; pipe == nil {
				panic(fmt.Errorf("non-placeholder pipe: %q", n.Attr("pipe")))
			} else if name, ok := strings.CutPrefix(pipe.String(), ".slots."); !ok {
				continue
			} else if slot, ok := slots[name]; ok {
				*n = *c.BranchNode("with", slotDot, slot.Children, nil)
			} else if slotDot != "" {
				panic(fmt.Errorf("unknown slot: %q", name))
			}
		} else {
			if !strings.HasPrefix(n.Tag, "t:") {
				c.processElem(c, n)
			}
			c.Walk(tpl, n.Children, calls, slots, slotDot)
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
	slotNodes, rest := c.partition(n.Children, "slot")
	slots := map[string]*Node{"rest": {Children: rest}}
	for _, n := range slotNodes {
		k := n.Attr("name")
		if _, ok := slots[k]; ok {
			panic(fmt.Errorf("duplicate slot %q in %q", k, "<"+n.Tag+">"))
		}
		slots[k] = n
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
	*n = *c.BranchNode("with", slotDot+" := .",
		[]*Node{c.BranchNode("with", w.String(), componentNodes, nil)}, nil)
	c.Walk(tpl, componentNodes, calls, slots, slotDot)
}

func (c *Compiler) partition(ns []*Node, tag string) (xs, rest []*Node) {
	for _, n := range ns {
		if n.Tag == tag {
			xs = append(xs, n)
		} else {
			rest = append(rest, n)
		}
	}
	return xs, rest
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
