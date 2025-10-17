package htmpl

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"text/template/parse"

	"golang.org/x/net/html"
)

type Context struct {
	Placeholders map[string]parse.Node
	L, R         string
	id           int
}

type Node struct {
	Type        html.NodeType
	Tag, Text   string
	Attrs       []html.Attribute
	SelfClosing bool
	Children    []*Node
}

var RawAttr = "RawAttrNamespace"
var FragmentNode = html.NodeType(1e6)
var voidTags = map[string]bool{ // html/render.go
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}
var rawTags = map[string]bool{ // html/render.go
	"iframe": true, "noembed": true, "noframes": true, "noscript": true,
	"plaintext": true, "script": true, "style": true, "xmp": true,
}
var unquotedAttrValueCharsRe = regexp.MustCompile(`^[-_a-zA-Z0-9]+$`)
var placeholderRe = regexp.MustCompile(`{{(\d+)}}`)
var keywords = map[parse.NodeType]string{
	parse.NodeRange: "range", parse.NodeIf: "if", parse.NodeWith: "with",
}

func New(delimiters ...string) *Context {
	l, r := "{{", "}}"
	if len(delimiters) == 2 {
		l, r = delimiters[0], delimiters[1]
	} else if len(delimiters) != 0 {
		panic(fmt.Errorf("number of delimiters must be 2"))
	}
	return &Context{Placeholders: map[string]parse.Node{}, L: l, R: r}
}

func (c *Context) RenderHTML(ns ...*Node) string {
	w, kvs := &strings.Builder{}, []string{}
	c.renderHTML(w, ns, 0, false)
	for k, v := range c.Placeholders {
		kvs = append(kvs, k, c.NodeText(v))
	}
	return strings.TrimSpace(strings.NewReplacer(kvs...).Replace(w.String()))
}

func (c *Context) ParseList(n *parse.ListNode) []*Node {
	bs := &bytes.Buffer{}
	for _, n := range n.Nodes {
		switch n := n.(type) {
		case *parse.TextNode:
			bs.Write(n.Text)
		default:
			bs.Write([]byte(c.Placeholder(n)))
		}
	}
	return c.ParseHTML(html.NewTokenizer(bs), false)
}

func (c *Context) ParseHTML(z *html.Tokenizer, isRaw bool) (ns []*Node) {
	for {
		if t := z.Next(); t == html.ErrorToken {
			if z.Err() == io.EOF {
				return ns
			}
			panic(fmt.Errorf("failed to parse: %w", z.Err()))
		} else if t == html.EndTagToken {
			return ns
		} else if t == html.TextToken {
			txt := string(z.Text())
			if ms := placeholderRe.FindAllStringIndex(txt, -1); isRaw || ms == nil {
				ns = append(ns, &Node{Type: html.TextNode, Text: txt})
			} else {
				ns = append(ns, c.parseTextNode(txt, ms)...)
			}
		} else if t == html.StartTagToken || t == html.SelfClosingTagToken {
			tag, more := z.TagName()
			n := &Node{Type: html.ElementNode, Tag: string(tag)}
			n.SelfClosing = t == html.SelfClosingTagToken || voidTags[n.Tag]
			for k, v := []byte{}, []byte{}; more; {
				k, v, more = z.TagAttr()
				// https://github.com/golang/go/issues/64330
				// https://github.com/golang/net/commit/e1fcd82/
				// https://html.spec.whatwg.org/#:~:text=can%20remain%20unquoted
				// we want <tag k=v/> to be parsed as a self-closing tag with k="v",
				// not a start tag with k="v/" - even if the spec says differntly. TIL.
				if !more && len(v) > 0 && v[len(v)-1] == '/' {
					if raw := string(z.Raw()); raw[len(raw)-2] == '/' {
						v, n.SelfClosing = v[:len(v)-1], true
					}
				}
				n.Attrs = append(n.Attrs, html.Attribute{Key: string(k), Val: string(v)})
			}
			if !n.SelfClosing {
				n.Children = c.ParseHTML(z, rawTags[n.Tag])
			}
			ns = append(ns, n)
		} else if t == html.DoctypeToken {
			ns = append(ns, &Node{Type: html.DoctypeNode, Text: string(z.Text())})
		}
	}
}

func (c *Context) ExpandText(n parse.Node) ([]*Node, bool) {
	switch n := n.(type) {
	case *parse.ListNode:
		return c.ParseList(n), true
	case *parse.ActionNode:
		bs := &bytes.Buffer{}
		fmt.Fprintf(bs, "<t:action pipe=%s />", c.Placeholder(n.Pipe))
		return c.ParseHTML(html.NewTokenizer(bs), false), true
	case *parse.TemplateNode:
		bs, name := &bytes.Buffer{}, html.EscapeString(n.Name)
		fmt.Fprintf(bs, "<t:template name=%q pipe=%s />", name, c.Placeholder(n.Pipe))
		return c.ParseHTML(html.NewTokenizer(bs), false), true
	case *parse.IfNode:
		return c.ExpandText(&n.BranchNode)
	case *parse.WithNode:
		return c.ExpandText(&n.BranchNode)
	case *parse.RangeNode:
		return c.ExpandText(&n.BranchNode)
	case *parse.BranchNode:
		bs := &bytes.Buffer{}
		fmt.Fprintf(bs, `<t:branch pipe=%s keyword=%s>`, c.Placeholder(n.Pipe), keywords[n.Type()])
		if n.List != nil {
			fmt.Fprintf(bs, "<t:list kind=then>%s</t:list>", c.Placeholder(n.List))
		}
		if n.ElseList != nil {
			fmt.Fprintf(bs, "<t:list kind=else>%s</t:list>", c.Placeholder(n.ElseList))
		}
		fmt.Fprintf(bs, "</t:branch>")
		return c.ParseHTML(html.NewTokenizer(bs), false), true
	}
	return nil, false
}

func (c *Context) NodeText(n parse.Node) string {
	switch n := n.(type) {
	case *parse.ActionNode:
		return fmt.Sprintf("%s %s %s", c.L, n.Pipe, c.R)
	case *parse.TemplateNode:
		return fmt.Sprintf("%s template %q %s %s", c.L, n.Name, n.Pipe, c.R)
	}
	return n.String()
}

func (c *Context) NodeString(n parse.Node) string {
	switch n := n.(type) {
	case *parse.ActionNode:
		return n.Pipe.String()
	default:
		panic(fmt.Errorf("cannot string value node of type %T", n))
	}
}

func (c *Context) ExpandString(s string) string {
	if n, ok := c.Placeholders[s]; ok {
		return c.NodeString(n)
	}
	ms := placeholderRe.FindAllStringIndex(s, -1)
	if ms == nil {
		return fmt.Sprintf("%q", s)
	}
	beg, xs := 0, []string{}
	for _, m := range ms {
		k := s[m[0]:m[1]]
		n, x := c.Placeholders[k], fmt.Sprintf("%q", s[beg:m[0]])
		beg, xs = m[1], append(xs, x, c.NodeString(n))
	}
	if beg != len(s) {
		xs = append(xs, fmt.Sprintf("%q", s[beg:]))
	}
	return fmt.Sprintf("(print %s)", strings.Join(xs, " "))
}

func (c *Context) FmtPlaceholder(tpl string, args ...any) string {
	tpl = strings.NewReplacer("{{", c.L, "}}", c.R).Replace(tpl)
	return c.Placeholder(&parse.TextNode{Text: fmt.Appendf(nil, tpl, args...)})
}

func (c *Context) Placeholder(n parse.Node) string {
	c.id++
	k := fmt.Sprintf("{{%d}}", c.id)
	c.Placeholders[k] = n
	return k
}

func (c *Context) ActionNode(pipe string) *Node {
	return &Node{Tag: "t:action", Type: html.ElementNode, Attrs: []html.Attribute{
		{Key: "pipe", Val: pipe},
	}}
}

func (c *Context) BranchNode(kind, pipe string, ns, elseNS []*Node) *Node {
	n := &Node{Tag: "t:branch", Type: html.ElementNode, Attrs: []html.Attribute{
		{Key: "pipe", Val: pipe}, {Key: "keyword", Val: kind},
	}}
	if ns != nil {
		n.Children = append(n.Children, &Node{Tag: "t:list", Type: html.ElementNode,
			Attrs: []html.Attribute{{Key: "kind", Val: "then"}}, Children: ns,
		})
	}
	if elseNS != nil {
		n.Children = append(n.Children, &Node{Tag: "t:list", Type: html.ElementNode,
			Attrs: []html.Attribute{{Key: "kind", Val: "else"}}, Children: elseNS,
		})
	}
	return n
}

func (c *Context) renderHTML(w *strings.Builder, ns []*Node, lvl int, isRaw bool) bool {
	indent, isEmpty := strings.Repeat("  ", lvl), true
	for _, n := range ns {
		isWhitespace := n == nil || n.Type == html.TextNode && strings.TrimSpace(n.Text) == ""
		if isWhitespace {
			continue
		}
		switch isEmpty = false; n.Type {
		case html.DoctypeNode:
			fmt.Fprintf(w, "<!DOCTYPE %s>", n.Text)
		case html.TextNode:
			if txt := strings.TrimSpace(n.Text); isRaw {
				fmt.Fprintf(w, "\n%s%s", indent, txt)
			} else {
				fmt.Fprintf(w, "\n%s%s", indent, html.EscapeString(txt))
			}
		case FragmentNode:
			c.renderHTML(w, n.Children, lvl+1, rawTags[n.Tag])
		case html.ElementNode:
			if strings.HasPrefix(n.Tag, "t:") {
				c.renderTHTML(w, n, lvl, isRaw)
				continue
			}
			fmt.Fprintf(w, "\n%s<%s", indent, n.Tag)
			for _, a := range n.Attrs {
				if a.Namespace == RawAttr {
					fmt.Fprintf(w, " %s", a.Val)
				} else if a.Val == "" {
					fmt.Fprintf(w, " %s", a.Key)
				} else if unquotedAttrValueCharsRe.MatchString(a.Val) {
					fmt.Fprintf(w, " %s=%s", a.Key, a.Val)
				} else {
					fmt.Fprintf(w, " %s=%q", a.Key, a.Val)
				}
			}
			if voidTags[n.Tag] {
				w.WriteString(">")
			} else if n.SelfClosing && voidTags[n.Tag] {
				w.WriteString(" />")
			} else {
				w.WriteString(">")
				if isEmpty := c.renderHTML(w, n.Children, lvl+1, rawTags[n.Tag]); !isEmpty {
					fmt.Fprintf(w, "\n%s", indent)
				}
				fmt.Fprintf(w, "</%s>", n.Tag)
			}
		}
	}
	return isEmpty
}

func (c *Context) renderTHTML(w *strings.Builder, n *Node, lvl int, isRaw bool) {
	indent, pipe := strings.Repeat("  ", lvl), n.Attr("pipe")
	if n, ok := c.Placeholders[pipe]; ok {
		pipe = n.String()
	}
	fmt.Fprintf(w, "\n%s", indent)
	switch n.Tag {
	case "t:action":
		fmt.Fprintf(w, "%s %s %s", c.L, pipe, c.R)
	case "t:template":
		fmt.Fprintf(w, "%s template %q %s %s", c.L, n.Attr("name"), pipe, c.R)
	case "t:branch":
		keyword := n.Attr("keyword")
		fmt.Fprintf(w, "%s %s %s %s", c.L, keyword, pipe, c.R)
		for _, n := range n.Children {
			if n.Attr("kind") == "else" {
				fmt.Fprintf(w, "\n%s%s else %s", indent, c.L, c.R)
			}
			c.renderHTML(w, n.Children, lvl+1, isRaw)
		}
		fmt.Fprintf(w, "\n%s%s end %s", indent, c.L, c.R)
	default:
		panic(fmt.Errorf("unknown t: tag: %q", n.Tag))
	}
}

func (c *Context) parseTextNode(s string, ms [][]int) (ns []*Node) {
	beg := 0
	for _, m := range ms {
		k := s[m[0]:m[1]]
		if cns, ok := c.ExpandText(c.Placeholders[k]); ok {
			ns = append(ns, &Node{Type: html.TextNode, Text: s[beg:m[0]]})
			beg, ns = m[1], append(ns, cns...)
		}
	}
	if beg != len(s) {
		ns = append(ns, &Node{Type: html.TextNode, Text: s[beg:]})
	}
	return ns
}

func (n *Node) String() string {
	return fmt.Sprintf("%#v", n)
}

func (n Node) Attr(k string) string {
	for _, a := range n.Attrs {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}
