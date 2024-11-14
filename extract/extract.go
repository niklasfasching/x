package extract

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/niklasfasching/x/soup"
	"golang.org/x/exp/slices"
	"golang.org/x/net/html"
)

var Positive = regexp.MustCompile(`(?i)(\b|_)(body|main|article|entry|page|post|story|content|text)(\b|_)`)
var Negative = regexp.MustCompile(`(?i)(\b|_)(header|footer|related|author|rating|related\w*|likes|comments?|discuss|cta|widget|shar(e|ing)|modal|nav(igation)?)(\b|_)`)

type Document struct {
	*soup.Node
	URL, Title string
	Content    string
}

type candidate struct {
	*soup.Node
	Len, NegLen, AddLen int
	Density, AddDensity float64
}

type selector struct {
	Tags     []string
	Pos, Neg *regexp.Regexp
	Nodes    map[*html.Node]struct{}
}

func (d *Document) Parse() error {
	d.Title = d.extractTitle()
	c, err := d.extractContent()
	d.Content = c
	return err
}

func (s *selector) Match(n *html.Node) bool {
	if t := n.Data; slices.Contains(s.Tags, t) {
		return true
	} else if s.Nodes != nil {
		for p := n.Parent; p != nil; p = p.Parent {
			if _, ok := s.Nodes[p]; ok {
				return false
			}
		}
	}
	match := false
	for _, a := range n.Attr {
		if a.Key == "class" || a.Key == "id" {
			if s.Neg != nil && s.Neg.MatchString(a.Val) {
				return false
			}
			match = match || s.Pos.MatchString(a.Val)
		}
	}
	if s.Nodes != nil && match {
		s.Nodes[n] = struct{}{}
	}
	return match
}

func (s *selector) String() string { return "" }

func (d *Document) extractContent() (string, error) {
	d.removeElements("head, script, noscript, style, link, #comments, #discuss, #discussion")
	m := map[*html.Node]*candidate{}
	for _, n := range d.AllSel(&selector{[]string{"body", "main", "article"}, Positive, Negative, nil}) {
		if l, ad := d.score(n); ad < 0.4 && l > 100 {
			p := &html.Node{Type: html.CommentNode, Data: "placeholder"}
			n.Parent.InsertBefore(p, soup.AsHTMLNode(n))
			n.Parent.RemoveChild(soup.AsHTMLNode(n))
			m[p] = &candidate{n, l, 0, 0, ad, 0.0}
		}
	}
	cs := []*candidate{}
	for _, c := range m {
		negLen := 0
		for _, n := range c.Node.AllSel(&selector{[]string{"nav", "form", "aside"}, Negative, nil, map[*html.Node]struct{}{}}) {
			negLen += len(n.TrimmedText())
		}
		if l, ad := d.score(c.Node); ad < 0.4 && float64(negLen)/float64(l) < 0.4 {
			c.AddLen, c.NegLen, c.AddDensity = l, negLen, ad
			cs = append(cs, c)
		}
	}
	for p, c := range m {
		p.Parent.InsertBefore(soup.AsHTMLNode(c.Node), p)
		p.Parent.RemoveChild(p)
	}
	if len(cs) == 0 {
		return "", fmt.Errorf("no content candidates found")
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Len > cs[j].Len })
	c := cs[0]
	d.trimFooter(c)
	err := d.fixURLs(c)
	return c.HTML(), err
}

func (d *Document) trimFooter(c *candidate) {
	s := &selector{[]string{"nav", "form", "aside"}, Negative, nil, nil}
	for n := c.Node.LastChild; n != nil; {
		if pn := n.PrevSibling; n.Type != html.ElementNode {
			n = pn
		} else if soup.AsNode(n).TrimmedText() == "" || s.Match(n) {
			n.Parent.RemoveChild(n)
			n = pn
		} else {
			n = n.LastChild
		}
	}
}

func (d *Document) fixURLs(c *candidate) error {
	base, err := url.Parse(d.URL)
	if err != nil {
		return err
	}
	for _, el := range c.All("[src], [href]") {
		for i, a := range el.Attr {
			if k := a.Key; k == "href" || k == "src" {
				u, err := url.Parse(a.Val)
				if err != nil {
					continue
				} else if u.Host == "" {
					el.Attr[i].Val = base.ResolveReference(u).String()
				}
			}
		}
	}
	return nil
}

func (d *Document) extractTitle() string {
	t := d.First(`title`).Text()
	if h := d.First("#title, h1[class~=title]"); h != nil && strings.Contains(t, h.Text()) {
		t = h.Text()
	}
	if m := d.First(`meta[property="og:title"]`); m != nil {
		t = m.Attribute("content")
	}
	return t
}

func (d *Document) score(n *soup.Node) (int, float64) {
	actionLength, textLength := 0, len(n.TrimmedText())
	for _, el := range n.All("a, button") {
		actionLength += len(el.TrimmedText())
	}
	return textLength, float64(actionLength) / float64(textLength)
}

func (d *Document) removeElements(selector string) {
	for _, n := range d.All(selector) {
		n.Parent.RemoveChild(soup.AsHTMLNode(n))
	}
}
