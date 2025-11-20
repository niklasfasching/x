package fts

import (
	"fmt"
	"io"
	"strings"
)

func HTMLSnippet(nCtx, nMax int, sDel, eDel, ell, text string, idxs [][2]int) string {
	if len(idxs) == 0 || len(text) == 0 {
		return ""
	}
	ts, more := map[int]bool{}, ""
	if nMax != -1 && len(idxs) > nMax {
		more, idxs = fmt.Sprintf(" %s(+%dx)%s", sDel, len(idxs)-nMax, eDel), idxs[:nMax]
	}
	for _, m := range idxs {
		for i := 0; i < m[1]; i++ {
			ts[m[0]+i] = true
		}
	}
	w, t, i, end, prev := &strings.Builder{}, 0, 0, -1, -1
	htmlText(text, func(off int, s string) error {
		j := 0
		for _, m := range tokenRe.FindAllStringIndex(s, -1) {
			for i < len(idxs) && t >= idxs[i][0]-nCtx {
				end, i = max(end, idxs[i][0]+idxs[i][1]+nCtx), i+1
			}
			if t < end {
				if prev != -1 {
					if gap := t > prev+1; ts[prev] && (gap || !ts[t]) {
						w.WriteString(eDel)
					} else if gap {
						w.WriteString(ell)
					} else if m[0] == 0 {
						w.WriteString(" ")
					}
				}
				w.WriteString(s[j:m[0]])
				if ts[t] && (prev == -1 || !ts[prev] || t > prev+1) {
					w.WriteString(sDel)
				}
				w.WriteString(s[m[0]:m[1]])
			} else if i >= len(idxs) {
				return io.EOF
			}
			j, t, prev = m[1], t+1, t
		}
		return nil
	})
	if prev != -1 && ts[prev] {
		w.WriteString(eDel)
	}
	return w.String() + more
}

func NewHTMLSnippetProcessor(nTokenContext, nMaxOccurences int, startDelim, endDelim, ellipsis string) Processor {
	return func(htmlText string, idxs [][2]int) string {
		return HTMLSnippet(nTokenContext, nMaxOccurences, startDelim, endDelim, ellipsis, htmlText, idxs)
	}
}
