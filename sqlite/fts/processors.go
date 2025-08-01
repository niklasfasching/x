package fts

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

func HTMLSnippet(nTokenContext, nMaxOccurences int, startDelim, endDelim, ellipsis, htmlText string, idxs [][2]int) string {
	d, err := html.Parse(strings.NewReader(htmlText))
	if err != nil {
		panic(err)
	}
	txt := (&html2text{}).extract(d).String()
	if len(idxs) == 0 {
		return ""
	}
	tokens := tokenRe.FindAllStringIndex(txt, -1)
	if len(tokens) == 0 {
		return ""
	}
	out, lastTo := &strings.Builder{}, -1
	for k, il := range idxs {
		if nMaxOccurences != -1 && k > nMaxOccurences {
			out.WriteString(fmt.Sprintf(" %s(+%dx)%s", startDelim, len(idxs)-k, endDelim))
			break
		}
		i, j := il[0], il[0]+il[1]-1
		from := max(lastTo+1, i-nTokenContext)
		if from < i {
			out.WriteString(ellipsis)
			out.WriteString(txt[tokens[from][0]:tokens[i][0]])
		}
		out.WriteString(startDelim)
		out.WriteString(txt[tokens[i][0]:tokens[j][1]])
		out.WriteString(endDelim)
		to := min(j+nTokenContext, len(tokens)-1)
		if to > j {
			out.WriteString(txt[tokens[j][1]:tokens[to][1]])
		}
		lastTo = to
	}
	return out.String() + ellipsis
}

func NewHTMLSnippetProcessor(nTokenContext, nMaxOccurences int, startDelim, endDelim, ellipsis string) Processor {
	return func(htmlText string, idxs [][2]int) string {
		return HTMLSnippet(nTokenContext, nMaxOccurences, startDelim, endDelim, ellipsis, htmlText, idxs)
	}
}
