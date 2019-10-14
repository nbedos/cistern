package utils

import (
	"fmt"
	"github.com/google/go-cmp/cmp"
	"strings"
	"testing"
)

type TestNode struct {
	value       string
	prefix      string
	traversable bool
	children    []TestNode
}

func (n TestNode) String() string {
	return n.value
}

func (n TestNode) Children() []TreeNode {
	children := make([]TreeNode, len(n.children))
	for i := range n.children {
		children[i] = &n.children[i]
	}
	return children
}

func (n TestNode) Traversable() bool {
	return n.traversable
}

func (n *TestNode) SetPrefix(prefix string) {
	n.prefix = prefix
}

func TestPrintFoldableTree(t *testing.T) {
	root := TestNode{
		value:       "root",
		traversable: true,
		children: []TestNode{
			{
				value: "a",
				children: []TestNode{
					{
						value:       "aa",
						traversable: true,
						children: []TestNode{
							{value: "aaa"},
							{value: "aab"},
							{value: "aac"},
						},
					},
					{value: "ab"},
				},
				traversable: true,
			},
			{value: "b"},
			{
				value: "c",
				children: []TestNode{
					{value: "ca"},
					{value: "cb"},
				},
				traversable: false,
			},
			{
				value: "d",
				children: []TestNode{
					{value: "da"},
					{value: "db"},
				},
				traversable: true,
			},
		},
	}

	DepthFirstTraversalPrefixing(&root)
	result := make([]string, 0)
	for _, node := range DepthFirstTraversal(&root, false) {
		switch n := node.(type) {
		case *TestNode:
			result = append(result, fmt.Sprintf("%s%s", n.prefix, n.value))
		}

	}

	expected := strings.Split(strings.Trim(`
 root
 ├── a
 │   ├── aa
 │   │   ├── aaa
 │   │   ├── aab
 │   │   └── aac
 │   └── ab
 ├── b
 ├─+ c
 └── d
     ├── da
     └── db
`, "\n"), "\n")

	if !cmp.Equal(result, expected) {
		for _, line := range result {
			t.Log(line)
		}
		t.Fatal("invalid result")
	}

}

func TestDepthFirstTraversal(t *testing.T) {
	node := TestNode{
		value:       "root",
		traversable: true,
		children: []TestNode{
			{
				value:       "a",
				traversable: true,
				children: []TestNode{
					{
						value:       "aa",
						traversable: false,
						children:    nil,
					},
					{
						value:       "ab",
						traversable: false,
						children:    nil,
					},
				},
			},
			{
				value:       "b",
				traversable: false,
				children: []TestNode{
					{
						value:       "ba",
						traversable: false,
						children:    nil,
					},
				},
			},
			{
				value:       "c",
				traversable: true,
				children: []TestNode{
					{
						value:       "ca",
						traversable: false,
						children:    nil,
					},
					{
						value:       "cb",
						traversable: false,
						children:    nil,
					},
					{
						value:       "cc",
						traversable: false,
						children:    nil,
					},
				},
			},
		},
	}

	expectedvalues := []string{"root", "a", "aa", "ab", "b", "c", "ca", "cb", "cc"}
	for i, n := range DepthFirstTraversal(&node, false) {
		if fmt.Sprintf("%s", n) != expectedvalues[i] {
			t.Fatalf("unexpected node: %s", n)
		}
	}
}
