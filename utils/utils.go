package utils

import (
	"github.com/mattn/go-runewidth"
	"strings"
)

func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func Bounded(a, lower, upper int) int {
	return MaxInt(lower, MinInt(a, upper))
}

type TreeNode interface {
	Children() []TreeNode
	Traversable() bool
	SetPrefix(prefix string) // meh. This forces us to pass a pointer to DepthFirstTraversal which is read only.
}

func DepthFirstTraversal(node TreeNode, traverseAll bool) []TreeNode {
	explored := make([]TreeNode, 0)
	toBeExplored := []TreeNode{node}

	for len(toBeExplored) > 0 {
		node = toBeExplored[len(toBeExplored)-1]
		toBeExplored = toBeExplored[:len(toBeExplored)-1]
		if traverseAll || node.Traversable() {
			children := node.Children()
			for i := len(children) - 1; i >= 0; i-- {
				toBeExplored = append(toBeExplored, children[i])
			}
		}

		explored = append(explored, node)
	}

	return explored
}

func DepthFirstTraversalPrefixing(node TreeNode) {
	depthFirstTraversalPrefixing(node, "", true)
}

func depthFirstTraversalPrefixing(node TreeNode, indent string, last bool) {
	var prefix string
	// Special behavior for the root node which is prefixed by "+" if its children are hidden
	if indent == "" {
		if len(node.Children()) == 0 || node.Traversable() {
			prefix = " "
		} else {
			prefix = "+"
		}
	} else {
		if last {
			prefix = "└─"
		} else {
			prefix = "├─"
		}

		if len(node.Children()) == 0 || node.Traversable() {
			prefix += "─ "
		} else {
			prefix += "+ "
		}
	}

	node.SetPrefix(indent + prefix)

	if node.Traversable() {
		children := node.Children()
		for i := range children {
			var childIndent string
			if last {
				childIndent = " "
			} else {
				childIndent = "│"
			}

			paddingLength := runewidth.StringWidth(prefix) - runewidth.StringWidth(childIndent)
			childIndent += strings.Repeat(" ", paddingLength)

			depthFirstTraversalPrefixing(children[i], indent+childIndent, i == len(children)-1)
		}
	}
}
