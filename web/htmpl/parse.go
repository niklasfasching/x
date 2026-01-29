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

func New() *Context {
	return &Context{Placeholders: map[string]parse.Node{}}
}

func (c *Context) RenderHTML(ns ...*Node) string {
	w, kvs := &strings.Builder{}, []string{}
	c.renderHTML(w, ns, 0, false, false)
	for k, v := range c.Placeholders {
		kvs = append(kvs, k, v.String())
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
			txt := string(z.Raw()) // NOTE: raw (rather than unescaped text)
			if ms := placeholderRe.FindAllStringIndex(txt, -1); isRaw || ms == nil {
				ns = append(ns, &Node{Type: html.TextNode, Text: txt})
			} else {
				ns = append(ns, c.ExpandTextNode(txt, ms)...)
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
		bs, name, p := &bytes.Buffer{}, html.EscapeString(n.Name), ""
		if n.Pipe != nil {
			p = c.Placeholder(n.Pipe)
		}
		fmt.Fprintf(bs, "<t:template name=%q pipe=%s />", name, p)
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

func (c *Context) NodeString(n parse.Node) string {
	switch n := n.(type) {
	case *parse.ActionNode:
		return n.Pipe.String()
	case *parse.TextNode:
		s := n.String()
		if v := strings.Trim(s, "{{}}"); len(s)-len(v) == len("{{}}") {
			return v
		}
		panic(fmt.Errorf("cannot string non-action text node: %q", s))
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
	return c.Placeholder(&parse.TextNode{Text: fmt.Appendf(nil, tpl, args...)})
}

func (c *Context) Placeholder(n parse.Node) string {
	c.id++
	k := fmt.Sprintf("{{%d}}", c.id)
	c.Placeholders[k] = n
	return k
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

func (c *Context) collapseSpace(s string) (trimmed, lPad, rPad string) {
	tmp := strings.TrimLeft(s, "\n\t ")
	trimmed = strings.TrimRight(tmp, "\n\t ")
	if len(tmp) != len(s) {
		lPad = " "
	}
	if len(tmp) != len(trimmed) {
		rPad = " "
	}
	return
}

func (c *Context) renderHTML(w *strings.Builder, ns []*Node, lvl int, isRaw, isSVG bool) bool {
	indent, isEmpty, prevType := strings.Repeat("  ", lvl), true, html.ElementNode
	for _, n := range ns {
		switch n.Type {
		case html.DoctypeNode:
			fmt.Fprintf(w, "<!DOCTYPE %s>", n.Text)
		case html.TextNode:
			// NOTE: text is not unescaped during parsing and thus not escaped here
			if s, lPad, rPad := c.collapseSpace(n.Text); len(s) == 0 {
				continue
			} else if prevType != html.TextNode {
				fmt.Fprintf(w, "\n%s%s%s", indent, s, rPad)
			} else {
				fmt.Fprintf(w, "%s%s", lPad, s)
			}
		case FragmentNode:
			c.renderHTML(w, n.Children, lvl+1, rawTags[n.Tag], isSVG)
		case html.ElementNode:
			if strings.HasPrefix(n.Tag, "t:") {
				c.renderTHTML(w, n, lvl, isRaw, isSVG)
				if n.Tag == "t:action" {
					prevType = html.TextNode
				}
				continue
			}
			isSVG = isSVG || n.Tag == "svg"
			fmt.Fprintf(w, "\n%s<%s", indent, n.Tag)
			for _, a := range n.Attrs {
				if a.Namespace == RawAttr {
					fmt.Fprintf(w, " %s", a.Val)
				} else if a.Val == "" {
					fmt.Fprintf(w, " %s", a.Key)
				} else if !isSVG && unquotedAttrValueCharsRe.MatchString(a.Val) {
					fmt.Fprintf(w, " %s=%s", a.Key, a.Val)
				} else {
					fmt.Fprintf(w, ` %s="%s"`, a.Key, a.Val)
				}
			}
			if voidTags[n.Tag] {
				w.WriteString(">")
			} else if n.SelfClosing && voidTags[n.Tag] {
				w.WriteString(" />")
			} else {
				w.WriteString(">")
				if isEmpty := c.renderHTML(w, n.Children, lvl+1, rawTags[n.Tag], isSVG); !isEmpty {
					fmt.Fprintf(w, "\n%s", indent)
				}
				fmt.Fprintf(w, "</%s>", n.Tag)
			}
		}
		isEmpty, prevType = false, n.Type
	}
	return isEmpty
}

func (c *Context) renderTHTML(w *strings.Builder, n *Node, lvl int, isRaw, isSVG bool) {
	indent, pipe := strings.Repeat("  ", lvl), n.Attr("pipe").Val
	if n, ok := c.Placeholders[pipe]; ok {
		pipe = n.String()
	}
	switch n.Tag {
	case "t:action":
		fmt.Fprintf(w, "{{%s}}", pipe)
	case "t:template":
		fmt.Fprintf(w, "\n%s{{template %q %s}}", indent, n.Attr("name").Val, pipe)
	case "t:branch":
		keyword := n.Attr("keyword").Val
		fmt.Fprintf(w, "\n%s{{%s %s}}", indent, keyword, pipe)
		for _, n := range n.Children {
			if n.Attr("kind").Val == "else" {
				fmt.Fprintf(w, "\n%s{{else}}", indent)
			}
			c.renderHTML(w, n.Children, lvl+1, isRaw, isSVG)
		}
		fmt.Fprintf(w, "\n%s{{end}}", indent)
	default:
		panic(fmt.Errorf("unknown t: tag: %q", n.Tag))
	}
}

func (c *Context) ExpandTextNode(s string, ms [][]int) (ns []*Node) {
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

func (n Node) Attr(k string) (a html.Attribute) {
	for _, a := range n.Attrs {
		if a.Key == k {
			return a
		}
	}
	return a
}
