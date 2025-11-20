//go:build fts5

package fts

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var tokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

func HTML(text string, flags int, cb func(token string, flags, start, end int) error) error {
	if flags&int(TokenizeQuery) != 0 {
		for _, ij := range tokenRe.FindAllStringIndex(text, -1) {
			if err := cb(strings.ToLower(text[ij[0]:ij[1]]), 0, ij[0], ij[1]); err != nil {
				return err
			}
		}
		return nil
	}
	return htmlText(text, func(off int, text string) error {
		for _, m := range tokenRe.FindAllStringIndex(text, -1) {
			start, end := off+m[0], off+m[1]
			if err := cb(strings.ToLower(text[m[0]:m[1]]), 0, start, end); err != nil {
				return err
			}
		}
		return nil
	})
}

func htmlText(in string, cb func(off int, text string) error) error {
	z, off, skip := html.NewTokenizer(strings.NewReader(in)), 0, false
	for {
		t := z.Next()
		if t == html.ErrorToken {
			break
		}
		raw := string(z.Raw())
		switch t {
		case html.StartTagToken, html.EndTagToken:
			tag, _ := z.TagName()
			if v := strings.ToLower(string(tag)); v == "script" || v == "style" {
				skip = t == html.StartTagToken
			}
		case html.TextToken:
			if !skip {
				if err := cb(off, raw); err != nil {
					return err
				}
			}
		}
		off += len(raw)
	}
	if err := z.Err(); err != io.EOF {
		return err
	}
	return nil
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
		return fmt.Errorf("not a json array/object: %q %v %v", text, t, err)
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
			if v, _ := v.(json.Delim); v == '{' || v == '[' {
				return fmt.Errorf("non-primitive object value for key %v (%v)", k, text)
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
