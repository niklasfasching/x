package component

import (
	. "text/template/parse"
)

type Position struct {
	List  *ListNode
	Index int
}

func Walk(n *ListNode, f func(Node, Position)) {
	walk(n, Position{List: n, Index: -1}, f)
}

func walk(n Node, p Position, f func(Node, Position)) {
	f(n, p)
	switch n := n.(type) {
	case *PipeNode:
		for _, c := range n.Decl {
			walk(c, p, f)
		}
		for _, c := range n.Cmds {
			walk(c, p, f)
		}
	case *BranchNode:
		if n.Pipe != nil {
			walk(n.Pipe, p, f)
		}
		if n.List != nil {
			walk(n.List, Position{n.List, -1}, f)
		}
		if n.ElseList != nil {
			walk(n.ElseList, Position{n.ElseList, -1}, f)
		}
	case *ListNode:
		for i, c := range n.Nodes {
			walk(c, Position{n, i}, f)
		}
	case *CommandNode:
		for _, c := range n.Args {
			walk(c, p, f)
		}
	case *ActionNode:
		walk(n.Pipe, p, f)
	case *ChainNode:
		walk(n.Node, p, f)
	case *TemplateNode:
		if n.Pipe != nil {
			walk(n.Pipe, p, f)
		}
	case *IfNode:
		walk(&n.BranchNode, p, f)
	case *RangeNode:
		walk(&n.BranchNode, p, f)
	case *WithNode:
		walk(&n.BranchNode, p, f)
	}
}

func (p Position) Replace(n Node) {
	if p.Index == -1 {
		*p.List = *n.(*ListNode)
	} else {
		p.List.Nodes[p.Index] = n
	}
}

func cmd(cmd Node, args ...Node) *CommandNode {
	return &CommandNode{NodeType: NodeCommand, Args: append([]Node{cmd}, args...)}
}

func pipe(cmds ...*CommandNode) *PipeNode {
	return &PipeNode{NodeType: NodePipe, Cmds: cmds}
}

func str(s string) *StringNode {
	return &StringNode{NodeType: NodeString, Text: s, Quoted: `"` + s + `"`}
}

func id(id string) Node {
	return &IdentifierNode{NodeType: NodeIdentifier, Ident: id}
}

func text(s string) Node {
	return &TextNode{NodeType: NodeText, Text: []byte(s)}
}

func call(name string, dot *PipeNode) Node {
	return &TemplateNode{NodeType: NodeTemplate, Name: name, Pipe: dot}
}

func list(ns ...Node) *ListNode {
	return &ListNode{NodeType: NodeList, Nodes: ns}
}

func null[T any]() *T {
	return (*T)(nil)
}

func ifElse(pipe *PipeNode, l1, l2 *ListNode) *IfNode {
	return &IfNode{BranchNode: branch(NodeIf, pipe, l1, l2)}
}

func with(pipe *PipeNode, l1, l2 *ListNode) Node {
	return &WithNode{BranchNode: branch(NodeWith, pipe, l1, l2)}
}

func branch(t NodeType, pipe *PipeNode, l1, l2 *ListNode) BranchNode {
	return BranchNode{NodeType: t, Pipe: pipe, List: l1, ElseList: l2}
}
