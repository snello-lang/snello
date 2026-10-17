// Version 1: idiomatic Go.
//
// Nodes point at each other. This works because the garbage collector
// keeps a node alive as long as anything points at it; nobody decides
// who owns a node. This file is the reference point: what a Go developer
// expects to write, and what Teak must stay close to.

package tree

import "fmt"

type Node struct {
	value    string
	parent   *Node
	children []*Node
}

type Tree struct {
	root *Node
}

func NewTree(value string) *Tree {
	return &Tree{root: &Node{value: value}}
}

func (t *Tree) AddChild(parent *Node, value string) *Node {
	n := &Node{value: value, parent: parent}
	parent.children = append(parent.children, n)
	return n
}

// Remove detaches a subtree. Nothing is freed here; the GC collects the
// nodes once no pointer reaches them. A caller still holding a *Node into
// the removed subtree keeps it alive, silently.
func (t *Tree) Remove(n *Node) {
	if n.parent == nil {
		return
	}
	siblings := n.parent.children
	for i, c := range siblings {
		if c == n {
			n.parent.children = append(siblings[:i], siblings[i+1:]...)
			break
		}
	}
	n.parent = nil
}

func (t *Tree) Path(n *Node) []string {
	var path []string
	for cur := n; cur != nil; cur = cur.parent {
		path = append(path, cur.value)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func (t *Tree) Walk(n *Node, visit func(*Node)) {
	visit(n)
	for _, c := range n.children {
		t.Walk(c, visit)
	}
}

func main() {
	t := NewTree("root")
	a := t.AddChild(t.root, "a")
	b := t.AddChild(a, "b")
	fmt.Println(t.Path(b)) // [root a b]

	count := 0
	t.Walk(t.root, func(*Node) { count++ })
	fmt.Println(count) // 3

	t.Remove(a)
	fmt.Println(t.Path(b)) // [a b]: b is still reachable through the stale pointer
}
