package component

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"
)

type Token struct {
	Type        html.TokenType
	Data        string
	Attrs       []html.Attribute
	SelfClosing bool
	Children    []Token
}

var RawAttr = "RawAttrNamespace"

// We're not using html.Parse / html.Render because that handles
// self-closing tags that aren't void elements in the spec unhelpfully;
// i.e. go renders <a/><b/> as <a><b></b></a> rather than <a></a><b></b>
// We also support a special attribute key to allow setting raw tag content
func RenderHTML(ts []Token) string {
	w := &strings.Builder{}
	render(w, ts)
	return w.String()
}

func TokenizeHTML(r io.Reader) []Token {
	var parse func(*html.Tokenizer) []Token
	parse = func(z *html.Tokenizer) []Token {
		for ts := []Token{}; ; {
			tt := z.Next()
			if tt == html.ErrorToken {
				if z.Err() == io.EOF {
					return ts
				}
				panic(fmt.Errorf("tokenize: %w", z.Err()))
			} else if tt == html.EndTagToken {
				return ts
			}
			t := Token{Type: tt, SelfClosing: tt == html.SelfClosingTagToken}
			if tt == html.TextToken {
				t.Data = string(z.Text())
			} else {
				tag, hasAttr := z.TagName()
				if t.Data = string(tag); hasAttr {
					for {
						k, v, more := z.TagAttr()
						t.Attrs = append(t.Attrs, html.Attribute{Key: string(k), Val: string(v)})
						if !more {
							break
						}
					}
				}
			}
			if tt == html.StartTagToken {
				t.Children = parse(z)
			}
			ts = append(ts, t)
		}
	}
	z := html.NewTokenizer(r)
	return parse(z)
}

func (t Token) Tag() string {
	if t.Type == html.StartTagToken || t.Type == html.SelfClosingTagToken {
		return t.Data
	}
	return ""
}

func (t Token) Attr(k string) string {
	for _, a := range t.Attrs {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func (t Token) Slots() (slots, rest []Token) {
	for _, t := range t.Children {
		if t.Tag() == "slot" {
			slots = append(slots, t)
		} else {
			rest = append(rest, t)
		}
	}
	return slots, rest
}

func render(w *strings.Builder, ts []Token) {
	for _, t := range ts {
		switch t.Type {
		case html.TextToken:
			w.WriteString(t.Data)
		case html.StartTagToken, html.SelfClosingTagToken:
			w.WriteString("<" + t.Data)
			for _, a := range t.Attrs {
				if a.Namespace == RawAttr {
					fmt.Fprintf(w, " %s", html.EscapeString(a.Val))
				} else {
					fmt.Fprintf(w, " %s=%q", a.Key, html.EscapeString(a.Val))
				}
			}
			if t.SelfClosing {
				w.WriteString("/>")
			} else {
				w.WriteString(">")
				render(w, t.Children)
				w.WriteString("</" + t.Data + ">")
			}
		}
	}
}
