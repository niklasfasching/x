package htmpl

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"
)

var DefaultFuncs = template.FuncMap{"list": List, "dict": Dict}

var attrKeyDirectiveRe = regexp.MustCompile(`^([\[#.-]+)?(.*?)([:?\]]+)?$`)
var attrValStyleVarRe = regexp.MustCompile(`(--\w+)`)
var kebabToCamelRe = regexp.MustCompile("-(.)")

func ProcessDirectives(c *Compiler, p *Component, n *Node) {
	kvs := map[string]string{}
	for _, a := range n.Attrs {
		m := attrKeyDirectiveRe.FindStringSubmatch(a.Key)
		switch pre, k, suf, v := m[1], m[2], m[3], a.Val; {
		case pre == "..." && c.ExpandString(k) == "." && v == "" && p != nil:
			for _, a := range p.Attrs { // forward all non-flag component attrs
				if isFlag := strings.HasPrefix(a.Key, "-"); isFlag {
					continue
				}
				k, v := a.Key, a.Val
				if placeholderRe.MatchString(a.Key) {
					k = c.FmtPlaceholder("{{with %s}} {{%s}} {{end}}",
						p.SlotDot, c.ExpandString(a.Key))
				}
				if placeholderRe.MatchString(a.Val) {
					v = c.FmtPlaceholder("{{with %s}} {{%s}} {{end}}",
						p.SlotDot, c.ExpandString(a.Val))
				}
				kvs[k] = v
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
		if k, v := kvs[i].(string), kvs[i+1]; k == "..." && len(kvs) == 2 {
			return v // TODO: lowerCamel struct field names
		} else if k == "..." {
			panic(fmt.Errorf("'...' must not be used with other kvs: got %v", kvs))
		} else {
			k := kebabToCamelRe.ReplaceAllStringFunc(strings.Trim(k, "-"),
				func(s string) string { return strings.ToUpper(s[1:]) })
			m[k] = v
		}
	}
	return m
}
