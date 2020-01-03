package utils

import (
	"fmt"
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

func (n *TestNode) SetTraversable(open bool, recursive bool) {
	n.traversable = open
	if recursive {
		for _, node := range n.children {
			node.SetTraversable(open, recursive)
		}
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
			t.Fatalf("unexpected node: %q", n)
		}
	}
}

func TestRepositoryHostAndSlug(t *testing.T) {
	testCases := map[string][]string{
		"nbedos/cistern": {
			// SSH git URL
			"git@github.com:nbedos/cistern.git",
			"git@github.com:nbedos/cistern",
			// HTTPS git URL
			"https://github.com/nbedos/cistern.git",
			// Host shouldn't matter
			"git@gitlab.com:nbedos/cistern.git",
			// Web URLs should work too
			"https://gitlab.com/nbedos/cistern",
			// URL scheme shouldn't matter
			"http://gitlab.com/nbedos/cistern",
			"gitlab.com/nbedos/cistern",
			// Trailing slash
			"gitlab.com/nbedos/cistern/",
		},
		"namespace/nbedos/cistern": {
			"https://gitlab.com/namespace/nbedos/cistern",
		},
		"long/namespace/nbedos/cistern": {
			"git@gitlab.com:long/namespace/nbedos/cistern.git",
			"https://gitlab.com/long/namespace/nbedos/cistern",
			"git@gitlab.com:long/namespace/nbedos/cistern.git",
		},
	}

	for expectedSlug, urls := range testCases {
		for _, u := range urls {
			t.Run(fmt.Sprintf("URL: %v", u), func(t *testing.T) {
				_, slug, err := RepositoryHostAndSlug(u)
				if err != nil {
					t.Fatal(err)
				}
				if slug != expectedSlug {
					t.Fatalf("expected %q but got %q", expectedSlug, slug)
				}
			})
		}
	}

	badURLs := []string{
		// Missing 1 path component
		"git@github.com:nbedos.git",
		// Invalid URL (colon)
		"https://github.com:nbedos/cistern.git",
	}

	for _, u := range badURLs {
		t.Run(fmt.Sprintf("URL: %v", u), func(t *testing.T) {
			if _, _, err := RepositoryHostAndSlug(u); err == nil {
				t.Fatalf("expected error but got nil for URL %q", u)
			}
		})
	}
}

func TestStartAndRelease(t *testing.T) {
	if err := StartAndRelease("go", []string{"--version"}); err != nil {
		t.Fatal(err)
	}
}
