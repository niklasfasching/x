package snap

import (
	"fmt"
	"strings"
)

// adapted from https://archive.is/RzNUm#selection-192.0-192.1
// PATTERN MATCHING: THE GESTALT APPROACH
// John W. Ratcliff, David E. Metzener

type Ops []Op

type Op struct {
	Type string
	Vs   []string
}

func (ops Ops) Render(color bool) string {
	w, cs := &strings.Builder{}, map[string]int{"=": 37, "-": 31, "+": 32}
	for _, op := range ops {
		for _, v := range op.Vs {
			if s := fmt.Sprintf("%s %s", op.Type, v); !color {
				fmt.Fprintf(w, "%s\n", s)
			} else {
				fmt.Fprintf(w, "\033[0;%dm%s\033[0m\n", cs[op.Type], s)
			}
		}
	}
	return w.String()
}

func Diff(v1, v2, sep string) Ops {
	return diff(strings.Split(v1, sep), strings.Split(v2, sep))
}

func diff(v1s, v2s []string) []Op {
	v1m := map[string][]int{}
	for i, v := range v1s {
		v1m[v] = append(v1m[v], i)
	}
	// We're looking for the longest match between v1s and v2s
	// m1 and m2 keep are the starting positions of the longest match in v1s and v2s respectively
	// we build a new (n)mls map for each value in v2s since we're only ever interested in the mls
	// that matched the previous v2 value as well
	mls, ml, m1, m2 := map[int]int{}, 0, 0, 0
	for i2, v := range v2s {
		nmls := map[int]int{}
		for _, i1 := range v1m[v] {
			if nmls[i1] = mls[i1-1] + 1; nmls[i1] > ml {
				ml = nmls[i1]
				m1, m2 = i1-ml+1, i2-ml+1
			}
		}
		mls = nmls
	}
	if ml > 0 {
		return append(append(
			diff(v1s[:m1], v2s[:m2]),
			Op{"=", v2s[m2 : m2+ml]}),
			diff(v1s[m1+ml:], v2s[m2+ml:])...,
		)
	}
	ops := []Op{}
	if len(v1s) != 0 {
		ops = append(ops, Op{"-", v1s})
	}
	if len(v2s) != 0 {
		ops = append(ops, Op{"+", v2s})
	}
	return ops
}
