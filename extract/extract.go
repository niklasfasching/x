package extract

import (
	"cmp"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/niklasfasching/x/css"
	"github.com/niklasfasching/x/soup"
	"golang.org/x/net/html"
)

type Document struct {
	*soup.Node
	*url.URL
}

type Candidates struct {
	Cs              []*Candidate
	MaxLen, MaxElem int
}

type Candidate struct {
	Node         *soup.Node
	Placeholder  *html.Node
	Len, LenSelf int
	ElemScore    int
	ContentScore float64
}

type ScoringRegExp struct {
	*regexp.Regexp
}

type Selector struct {
	Score func(*html.Node) int
}

var Tags = map[string]int{"main": 3, "article": 4, "section": 1, "nav": -4, "form": -3}
var AttrWeights = map[string]int{"id": 2, "role": 2, "class": 1}
var NegativeAttrs = NewScoringRegExp(map[int][]string{
	2: {"header", "footer", "modal", "nav(igation)?", "side(bar)?", "comment(s|list)?", "metadata", "social(media)?"},
	1: {"related", "overlay", "authors?", "links?", "rating", "likes", "discuss", "cta", "shar(e|ing)", "actions?"},
})
var PositiveAttrs = NewScoringRegExp(map[int][]string{
	3: {"article", "h?entry", "post", "story"},
	2: {"main", "body", "content"},
	1: {"text", "page", "container"},
})
var camelCaseRe = regexp.MustCompile("(\\p{Lu}+\\P{Lu}*)")

func Content(u string, n *soup.Node) (string, string, error) {
	url, err := url.Parse(u)
	if err != nil {
		return "", "", err
	}
	d := &Document{URL: url, Node: n}
	t := d.extractTitle()
	c, err := d.extractContent()
	return t, c, err
}

func NewScoringRegExp(m map[int][]string) *ScoringRegExp {
	r := ""
	for n, vs := range m {
		r += fmt.Sprintf("|(?P<%d>%s)", n, strings.Join(vs, "|"))
	}
	return &ScoringRegExp{regexp.MustCompile(fmt.Sprintf(`(?i)(\b|_|-)(%s)(\b|_|-)`, r[1:]))}
}

func (d *Document) extractContent() (string, error) {
	d.removeElements("head, script, style, noscript, link, #comments, #discuss, #discussion")
	cm := d.findCandidates()
	cs := d.filteredCandidates(cm)
	cs.Log()
	if len(cs.Cs) == 0 {
		return "", fmt.Errorf("no candidates found")
	}
	c := cs.Cs[0]
	for n := range soup.AsHTMLNode(c.Node).Descendants() {
		attrs := n.Attr
		n.Attr = []html.Attribute{}
		for _, a := range attrs {
			if a.Key == "class" && NegativeAttrs.Score(a.Val) > 0 {
				a.Val = "boilerplate"
				n.Attr = append(n.Attr, a)
			} else if a.Key != "id" && a.Key != "class" && !strings.HasPrefix(a.Key, "data-") {
				n.Attr = append(n.Attr, a)
			}
		}
	}
	err := c.fixURLs(d.URL)
	return c.Node.HTML(), err
}

func (d *Document) findCandidates() map[*html.Node]*Candidate {
	nWordCount, nDepth := map[*html.Node]int{}, map[*html.Node]int{}
	for _, n := range d.AllSel(d.notChildOfSelector(d.boilerplateSelector(), css.MustCompile("div, p, li"))) {
		txt := n.TrimmedText()
		wordCount := len(strings.Fields(txt))
		if wordCount < 10 {
			continue
		}
		d, nRevDepth := 0, map[*html.Node]int{}
		for p := soup.AsHTMLNode(n); p != nil; p = p.Parent {
			nRevDepth[p], nWordCount[p], d = d, nWordCount[p]+wordCount, d+1
		}
		for p, rd := range nRevDepth {
			nDepth[p] = d - rd
		}
	}
	ns, maxWordCount := []*html.Node{}, 0
	for n := range nWordCount {
		ns, maxWordCount = append(ns, n), max(maxWordCount, nWordCount[n])
	}
	slices.SortFunc(ns, func(a, b *html.Node) int {
		return cmp.Or(cmp.Compare(nWordCount[b], nWordCount[a]), cmp.Compare(nDepth[b], nDepth[a]))
	})
	cm, top := map[*html.Node]*Candidate{}, map[int]*html.Node{}
	for _, n := range ns {
		if c := nWordCount[n]; c < maxWordCount/5 {
			break
		} else if top[c] == nil {
			top[c] = n
			cm[n] = &Candidate{Node: soup.AsNode(n), Len: len(soup.AsNode(n).TrimmedText())}
		}
	}
	for _, n := range d.AllSel(d.candidateSelector()) {
		if _, ok := cm[soup.AsHTMLNode(n)]; ok {
			continue
		} else if txt := n.TrimmedText(); len(strings.Fields(txt)) > maxWordCount/5 {
			cm[soup.AsHTMLNode(n)] = &Candidate{Node: n, Len: len(txt)}
		}
	}
	return cm
}

func (d *Document) filteredCandidates(cm map[*html.Node]*Candidate) Candidates {
	for _, c := range cm {
		c.Placeholder = &html.Node{Type: html.CommentNode}
		c.Node.Parent.InsertBefore(c.Placeholder, soup.AsHTMLNode(c.Node))
		c.Node.Parent.RemoveChild(soup.AsHTMLNode(c.Node))
	}
	cs, cSel, bpSel := []*Candidate{}, d.candidateSelector(), d.boilerplateSelector()
	maxLen, maxElemScore := 0, 1
	for _, c := range cm {
		l, bpLen, actionLen := float64(len(c.Node.TrimmedText())), 0.0, 0.0
		for _, n := range c.Node.AllSel(d.notChildOfSelector(bpSel, bpSel)) {
			bpLen += float64(len(n.TrimmedText()))
		}
		for _, n := range c.Node.All("a, button") {
			actionLen += float64(len(n.TrimmedText()))
		}
		if n, aScore, bpScore := soup.AsHTMLNode(c.Node), actionLen/l, bpLen/l; aScore <= 0.7 && bpScore <= 0.3 {
			c.LenSelf, c.ElemScore, c.ContentScore = int(l), cSel.Score(n)-bpSel.Score(n), ((1-bpScore)+(1-aScore))/2
			cs, maxLen, maxElemScore = append(cs, c), max(maxLen, c.Len), max(maxElemScore, c.ElemScore)
		}
	}
	for _, c := range cm {
		c.Placeholder.Parent.InsertBefore(soup.AsHTMLNode(c.Node), c.Placeholder)
		c.Placeholder.Parent.RemoveChild(c.Placeholder)
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].Score(maxLen, maxElemScore) > cs[j].Score(maxLen, maxElemScore) })
	return Candidates{cs, maxLen, maxElemScore}
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

func (d *Document) removeElements(selector string) {
	for _, n := range d.All(selector) {
		n.Parent.RemoveChild(soup.AsHTMLNode(n))
	}
}

func (c *Candidate) Score(maxLen, maxElem int) float64 {
	return 5*c.ContentScore + 4*(float64(c.Len)/float64(maxLen)) + 3*(float64(c.LenSelf)/float64(maxLen)) + 2*float64(c.ElemScore)/float64(maxElem)
}

func (cs *Candidates) Log() {
	for _, c := range cs.Cs {
		log.Println(c.Node.Data, c.ElemScore, c.ContentScore, c.Len, c.LenSelf, cs.MaxLen, cs.MaxElem, c.Score(cs.MaxLen, cs.MaxElem), c.Node.Attr)
	}
}

func (c *Candidate) fixURLs(base *url.URL) error {
	for _, el := range c.Node.All("[src], [href], [srcset]") {
		for i, a := range el.Attr {
			if k := a.Key; k == "href" || k == "src" {
				el.Attr[i].Val = c.rebaseURL(a.Val, base)
			} else if k == "srcset" {
				vs := []string{}
				for _, x := range strings.Split(a.Val, ", ") {
					if kv := strings.Split(x, " "); len(kv) == 1 {
						vs = append(vs, c.rebaseURL(kv[0], base))
					} else if len(kv) == 2 {
						vs = append(vs, c.rebaseURL(kv[0], base)+" "+kv[1])
					} else {
						vs = append(vs, x)
					}
				}
				el.Attr[i].Val = strings.Join(vs, ", ")
			}
		}
	}
	return nil
}

func (c *Candidate) rebaseURL(urlString string, base *url.URL) string {
	if u, err := url.Parse(urlString); err == nil && u.Host == "" {
		return base.ResolveReference(u).String()
	}
	return urlString
}

func (s *Selector) Match(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	return s.Score(n) > 0
}

func (s *Selector) String() string { return "" }

func (d *Document) notChildOfSelector(ancestor, selector css.Selector) css.Selector {
	return &Selector{func(n *html.Node) int {
		if !selector.Match(n) {
			return 0
		}
		for p := n.Parent; p != nil; p = p.Parent {
			if ancestor.Match(n) {
				return 0
			}
		}
		return 1
	}}
}
func (d *Document) boilerplateSelector() *Selector {
	f := func(n *html.Node) int {
		vs, s := map[string]int{}, 0
		if v, ok := Tags[n.Data]; ok {
			vs[n.Data] = -v
		}
		for _, a := range n.Attr {
			if w := AttrWeights[a.Key]; w > 0 {
				NegativeAttrs.Match(a.Val, vs, w)
			}
		}
		for _, v := range vs {
			s += v
		}
		return max(0, s)
	}
	return &Selector{f}
}

func (d *Document) candidateSelector() *Selector {
	return &Selector{func(n *html.Node) int {
		vs, s := map[string]int{}, 0
		if v, ok := Tags[n.Data]; ok {
			vs[n.Data] = v
		}
		for _, a := range n.Attr {
			if w := AttrWeights[a.Key]; w > 0 {
				NegativeAttrs.Match(a.Val, vs, -w)
				PositiveAttrs.Match(a.Val, vs, w)
			}
		}
		for _, v := range vs {
			s += v
		}
		return max(0, s)
	}}
}

func (r *ScoringRegExp) Score(s string) int {
	sc := 0
	for _, v := range r.Match(s, map[string]int{}, 1) {
		sc += v
	}
	return sc
}

func (r *ScoringRegExp) Match(s string, vs map[string]int, weight int) map[string]int {
	if r == nil {
		return vs
	}
	ms := r.FindAllStringSubmatch(camelCaseRe.ReplaceAllString(s, "${1}-"), -1)
	if ms == nil {
		return vs
	}
	for i, k := range r.SubexpNames() {
		for _, m := range ms {
			if i != 0 && k != "" && m[i] != "" {
				c, _ := strconv.Atoi(k)
				vs[m[i]] = max(weight*c, vs[m[i]])
			}
		}
	}
	return vs
}
