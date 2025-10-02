package component

import (
	"bytes"
	"cmp"
	"fmt"
	"html/template"
	"sort"
	"strings"
	"text/template/parse"

	"golang.org/x/net/html"
)

type Context struct {
	*template.Template
	Placeholders          map[string]parse.Node
	delimLeft, delimRight string
	inline                bool
	i                     int
}

type Component struct {
	*Context
	Token
	Children, Slots []Token
}

// Compile expands syntax sugar for html in a template and all its subtemplates.
// - html tag based template calls for for html tags with dashes
//   `<tpl-foo a="1" b="{{ true }}" />` becomes `{{ template name {a: "1", b: true} }}`
// - html tag attribute directives
//   . => adds a class `<x .foo .bar/>` => `<x class="foo bar"/>`
//   # => sets the id `<x #foo/>` => `<x id="foo"/>`
//   ? => conditionally sets the attribute `<x foo?={{true}}/>` => `<x {{if true}}foo{{end}}/>`
//
// The inline parameter decides which tradeoffs to choose
// - Inlining templates "leaks" variables of the surrounding scope of the callsite
//   to the component template scope (and also allows slots/children from the component
//   call site to access that scope as expected)
// - Outlining (i.e. compiling separate "instances" for template calls with slots/children)
//   isolates the component scope to only the passed params (but prevents slots/children from
//   accessing the parents lexical scope)
func Compile(tpl *template.Template, inline bool) error {
	return compile(tpl, "xCOMPONENTx.", ".xCOMPONENTx", inline)
}

func compile(tpl *template.Template, delimLeft, delimRight string, inline bool) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = r.(error)
		}
	}()
	tpl.Funcs(template.FuncMap{
		"list": func(vs ...any) any { return vs },
		"dict": func(kvs ...any) any {
			m := map[any]any{}
			for i := 0; i < len(kvs); i += 2 {
				m[kvs[i]] = kvs[i+1]
			}
			return m
		},
	})
	c := &Context{tpl, map[string]parse.Node{}, delimLeft, delimRight, inline, 0}
	for _, tpl := range tpl.Templates() {
		c.Compile(tpl.Tree, nil, []string{tpl.Name()})
	}
	return err
}

func (c *Context) Compile(tree *parse.Tree, slots []Token, path []string) bool {
	slotMap, modified := map[string]*Token{}, false
	for _, t := range slots {
		if k := cmp.Or(t.Attr("name"), "rest"); slotMap[k] != nil {
			panic(fmt.Errorf("bad slot %q: %v", k, slotMap))
		} else {
			slotMap[k] = &t
		}
	}
	Walk(tree.Root, func(n parse.Node, p Position) {
		switch n := n.(type) {
		case *parse.ListNode:
			if ts, listModified := c.compile(c.Tokenize(n), path); listModified {
				n.Nodes, modified = c.Parse(ts).Nodes, true
			}
		case *parse.FieldNode:
			if ks := n.Ident; len(ks) == 2 && slotMap[ks[1]] != nil {
				p.Replace(c.Parse(slotMap[ks[1]].Children))
				modified = true
			}
		}
	})
	return modified
}

func (c *Context) Tokenize(n *parse.ListNode) []Token {
	b := &bytes.Buffer{}
	for _, node := range n.Nodes {
		switch node := node.(type) {
		case *parse.TextNode:
			b.Write(node.Text)
		default:
			b.Write([]byte(c.Placeholder(node)))
		}
	}
	return TokenizeHTML(b)
}

func (c *Context) compile(ts []Token, path []string) ([]Token, bool) {
	out, modified := []Token{}, false
	for _, t := range ts {
		children, childrenModified := c.compile(t.Children, path)
		t.Children, modified = children, modified || childrenModified
		name := t.Tag()
		if !strings.Contains(name, "-") || c.Lookup(name) == nil {
			if isEl := name != ""; isEl {
				t, tokenModified := c.compileToken(t)
				modified = modified || tokenModified
				out = append(out, t)
			} else {
				out = append(out, t)
			}
			continue
		}
		slots, rest := t.Slots()
		tree, p := c.Template.Lookup(name).Tree.Copy(), ""
		if c.inline {
			c.Compile(tree, append(slots, Token{Children: rest}), append(path, name))
			dot := c.Dict(t, str("slots"), c.Dicts(slots))
			p = c.Placeholder(with(dot, tree.Root, null[parse.ListNode]()))

		} else {
			if c.Compile(tree, append(slots, Token{Children: rest}), append(path, name)) {
				name = c.AddTemplate(tree, append(path, name))
			}
			dot := c.Dict(t, str("slots"), c.Dicts(slots))
			p = c.Placeholder(call(name, dot))
		}
		modified, out = true, append(out, Token{Type: html.TextToken, Data: p})
	}
	return out, modified
}

func (c *Context) Parse(ts []Token) *parse.ListNode {
	html, p := RenderHTML(ts), parse.New("")
	p.Mode = parse.SkipFuncCheck
	t, err := p.Parse(html, c.delimLeft, c.delimRight, map[string]*parse.Tree{})
	if err != nil {
		panic(fmt.Errorf("failed to parse html: %w", err))
	} else if len(c.Placeholders) != 0 {
		Walk(t.Root, func(n parse.Node, p Position) {
			switch n := n.(type) {
			case *parse.FieldNode:
				k := c.delimLeft + "." + n.Ident[0] + c.delimRight
				if placeholderNode, ok := c.Placeholders[k]; ok {
					p.Replace(placeholderNode)
				}
			}
		})
	}
	return t.Root
}

func (c *Context) compileToken(t Token) (t2 Token, modified bool) {
	m := map[string]string{}
	for _, a := range t.Attrs {
		x, v := a.Key[0], a.Key[1:]
		if a.Val != "" && (x == '.' || x == '#') {
			v = c.Placeholder(ifElse(c.parseAttr(v, a.Val), list(str(v)), null[parse.ListNode]()))
		} else if k := a.Key; a.Val != "" && k[len(k)-1] == '?' {
			x, v = '?', c.Placeholder(ifElse(c.parseAttr(v, a.Val), list(text(k[:len(k)-1])), null[parse.ListNode]()))
		}
		switch {
		case x == '.':
			m["class"], modified = m["class"]+" "+v, true
		case x == '#':
			m["id"], modified = v, true
		case x == '?':
			m[""], modified = m[""]+v, true
		default:
			m[a.Key] = a.Val
		}
	}
	if !modified {
		return t, false
	}
	t2 = t
	t2.Attrs = nil
	for k, v := range m {
		if k == "" {
			t2.Attrs = append(t2.Attrs, html.Attribute{Namespace: RawAttr, Val: v})
		} else {
			t2.Attrs = append(t2.Attrs, html.Attribute{Key: k, Val: v})
		}
	}
	sort.Slice(t2.Attrs, func(i, j int) bool { return cmp.Less(t2.Attrs[i].Key, t2.Attrs[j].Key) })
	return t2, true
}

func (c *Context) Placeholder(n parse.Node) string {
	c.i++
	k := fmt.Sprintf("%s._%d%s", c.delimLeft, c.i, c.delimRight)
	c.Placeholders[k] = n
	return k
}

func (c *Context) AddTemplate(tree *parse.Tree, path []string) string {
	name, i := "", 1
	for ; ; i++ {
		name = fmt.Sprintf(">%s-%d", strings.Join(path, ">"), i)
		if c.Lookup(name) == nil {
			break
		}
	}
	c.AddParseTree(name, tree)
	return name
}

func (c *Context) Dicts(ts []Token, dicts ...parse.Node) parse.Node {
	for _, t := range ts {
		dicts = append(dicts, c.Dict(t))
	}
	return pipe(cmd(id("list"), dicts...))
}

func (c *Context) Dict(t Token, kvs ...parse.Node) *parse.PipeNode {
	for k, v := range c.ParseAttrs(t) {
		kvs = append(kvs, str(k), v)
	}
	return pipe(cmd(id("dict"), kvs...))
}

func (c *Context) ParseAttrs(t Token) map[string]parse.Node {
	m := map[string]parse.Node{}
	for _, a := range t.Attrs {
		m[a.Key] = c.parseAttr(a.Key, a.Val)
	}
	return m
}

func (c *Context) parseAttr(k, v string) *parse.PipeNode {
	if !strings.Contains(v, c.delimLeft) {
		return pipe(cmd(str(v)))
	} else if n, ok := c.Placeholders[v]; ok {
		if an, ok := n.(*parse.ActionNode); ok {
			return an.Pipe
		} else {
			panic(fmt.Errorf("bad component attr %q: %#v", k, n))
		}
	}
	args := []parse.Node{}
	for len(v) > 0 {
		if i := strings.Index(v, c.delimLeft); i == -1 {
			args = append(args, str(v))
			break
		} else {
			args, v = append(args, str(v[:i])), v[i:]
			end := strings.Index(v, c.delimRight) + len(c.delimRight)
			if n, ok := c.Placeholders[v[:end]].(*parse.ActionNode); ok {
				v, args = v[end:], append(args, n.Pipe)
			} else {
				panic(fmt.Errorf("bad component attr %q: %#v", k, c.Placeholders[v[:end]]))
			}
		}
	}
	return pipe(cmd(id("print"), args...))
}
