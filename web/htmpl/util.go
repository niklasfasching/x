package htmpl

import (
	"fmt"
	"regexp"
)

var attrKeyDirectiveRe = regexp.MustCompile("^([#.-]+)?(.*?)([:?]+)?$")
var attrValStyleVarRe = regexp.MustCompile(`(--\w+)`)
var attrClassValStyleVarRe = regexp.MustCompile(`(([^ ]+):([^ ]+))`)

func ProcessDirectives(c *Compiler, n *Node) {
	kvs := map[string]string{}
	for _, a := range n.Attrs {
		m := attrKeyDirectiveRe.FindStringSubmatch(a.Key)
		switch pre, k, suf, v := m[1], m[2], m[3], a.Val; {
		case pre == "." && suf == ":": // TODO
			v = attrValStyleVarRe.ReplaceAllString(v, "var($1)")
			kvs["style"] += attrClassValStyleVarRe.ReplaceAllString(v,
				fmt.Sprintf(";--%s-$2: $3;", k))
			kvs["class"] += " " + k
		case suf == ":": // TODO
			v = attrValStyleVarRe.ReplaceAllStringFunc(v, func(v string) string {
				return fmt.Sprintf("var(--%s-%s, var(--%s))", k, v[2:], v[2:])
			})
			kvs["style"] += "; " + k + ": " + v + ";"
		case pre == "." && a.Val != "": // .class={{condition}}
			kvs["class"] += " " + c.FmtPlaceholder("{{ if %s -}} %s {{- end }}", c.ExpandString(v), k)
		case pre == "#": // #id
			kvs["id"] = k
		case pre == ".": // .class
			kvs["class"] += " " + k
		case pre == "-" || pre == "--": // --var=xyz
			kvs["style"] += "; --" + k + ": " + v + ";"
		case suf == "?": // checked={{condition}}
			kvs[""] += c.FmtPlaceholder("{{ if %s -}} %s {{- end }}",
				c.ExpandString(a.Val), k)
		default:
			kvs[a.Key] = a.Val
		}
	}
	c.SetAttrs(n, kvs)
}
