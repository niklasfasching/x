//go:build fts5

package fts

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

type html2text struct {
	strings.Builder
	endsInWhitespace bool
}

var tokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

func HTML(text string, flags int, cb func(token string, flags, start, end int) error) error {
	if flags&int(TokenizeQuery) == 0 {
		doc, err := html.Parse(strings.NewReader(text))
		if err != nil {
			return err
		}
		text = (&html2text{}).extract(doc).String()
	}
	for _, ij := range tokenRe.FindAllStringIndex(text, -1) {
		if err := cb(strings.ToLower(text[ij[0]:ij[1]]), 0, ij[0], ij[1]); err != nil {
			return err
		}
	}
	return nil
}

func (h *html2text) extract(n *html.Node) *html2text {
	if n.Type == html.TextNode && len(n.Data) != 0 {
		if !h.endsInWhitespace {
			h.WriteString(" ")
		}
		h.endsInWhitespace = unicode.IsSpace(rune(n.Data[(len(n.Data) - 1)]))
		h.WriteString(n.Data)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "script" || c.Data == "style") {
			continue
		}
		h.extract(c)
	}
	return h
}

func JSON(text string, flags int, cb func(token string, flags, start, end int) error) error {
	if text == "null" || text == "" {
		return nil
	}
	if flags&TokenizeQuery != 0 {
		return cb(text, 0, 0, len(text))
	}
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	t, err := d.Token()
	r, _ := t.(json.Delim)
	if err != nil || (r != '[' && r != '{') {
		return fmt.Errorf("not a json array/object: %q %v", text, t)
	}
	for d.More() {
		i, out := d.InputOffset(), ""
		if r == '{' {
			k, err := d.Token()
			if err != nil {
				return fmt.Errorf("failed to parse object key: %w", err)
			}
			v, err := d.Token()
			if err != nil {
				return fmt.Errorf("failed to parse object value: %w", err)
			}
			// sqlite does not like [=:] tokens in unquoted fts queries. to keep
			// prefix queries simple we'll go with another dot like char that's uncommon
			// could also use [_] but using a char that won't be part of common kvs seems useful.
			out = fmt.Sprintf("%v•%v", k, v)
		} else {
			v, err := d.Token()
			if err != nil {
				return fmt.Errorf("failed to parse array value: %w", err)
			}
			out = fmt.Sprintf("%v", v)
		}
		if err := cb(out, 0, int(i), int(d.InputOffset())); err != nil {
			return err
		}
	}
	return nil
}
