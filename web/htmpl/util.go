package htmpl

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"slices"
)

var DefaultFuncs = template.FuncMap{"list": List, "dict": Dict}

var attrKeyDirectiveRe = regexp.MustCompile(`^([\[#.-]+)?(.*?)([:?\]]+)?$`)
var attrValStyleVarRe = regexp.MustCompile(`(--\w+)`)
var kebabToCamelRe = regexp.MustCompile("-(.)")
var camelToKebabRe = regexp.MustCompile("([a-z0-9])([A-Z])")

func ProcessDirectives(c *Compiler, p *Frame, n *Node) {
	kvs := map[string]string{}
	for _, a := range n.Attrs {
		m := attrKeyDirectiveRe.FindStringSubmatch(a.Key)
		switch pre, k, suf, v := m[1], m[2], m[3], a.Val; {
		case pre == "..." && suf == "" && !p.Root: // ... spread (defined) component params
			if v := c.ExpandString(k); v == "." || c.Placeholders[k] == nil {
				panic(fmt.Errorf("unexpected ... arg: %q (%q)", v, k))
			} else {
				v, ne := strings.CutPrefix(v, "ne ")
				ks := strings.Fields(v)
				for _, a := range p.Attrs {
					if ok := slices.Contains(ks, "."+kebabToCamel(a.Key)); ne && !ok || !ne && ok {
						if placeholderRe.MatchString(a.Val) {
							kvs[a.Key] = c.FmtPlaceholder("{{with $dot}}{{%s}}{{end}}",
								c.ExpandString(a.Val))
						} else {
							kvs[a.Key] = a.Val
						}
					}
				}
			}
		case pre == "." && suf == ":": // add class k and set --var k to v
			v = attrValStyleVarRe.ReplaceAllString(v, "var($1)")
			kvs["class"] += " " + k
			kvs["style"] += "; --" + k + ": " + v + ";"
		case suf == ":": // set css property k to v
			v = attrValStyleVarRe.ReplaceAllString(v, "var($1)")
			kvs["style"] += "; " + k + ": " + v + ";"
		case pre == "[" && suf == "]": // set html attribute data-<k> to v
			kvs["data-"+k] = v
		case pre == "." && a.Val != "": // add class k if v evaluates to true
			kvs["class"] += " " + c.FmtPlaceholder("{{ if %s -}} %s {{- end }}", c.ExpandString(v), k)
		case pre == "#": // set id to k
			kvs["id"] = k
		case pre == ".": // add class k
			kvs["class"] += " " + k
		case pre == "-" || pre == "--": // set --var k to v
			kvs["style"] += "; --" + k + ": " + v + ";"
		case suf == "?": // set html attribute k if v evaluates to true
			kvs[""] += c.FmtPlaceholder("{{ if %s -}} %s {{- end }}", c.ExpandString(a.Val), k)
		default:
			kvs[a.Key] = a.Val
		}
	}
	c.SetAttrs(n, kvs)
}

func List(vs ...any) any { return vs }

func Dict(kvs ...any) any {
	m := map[string]any{}
	for i := 0; i < len(kvs); i += 2 {
		k := kebabToCamelRe.ReplaceAllStringFunc(kvs[i].(string),
			func(s string) string { return strings.ToUpper(s[1:]) })
		m[k] = kvs[i+1]
	}
	return m
}

func kebabToCamel(s string) string {
	return kebabToCamelRe.ReplaceAllStringFunc(strings.Trim(s, "-"),
		func(s string) string { return strings.ToUpper(s[1:]) })
}

func camelToKebab(s string) string {
	return strings.ToLower(camelToKebabRe.ReplaceAllString(s, "${1}-${2}"))
}
