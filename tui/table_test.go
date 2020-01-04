package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/mattn/go-runewidth"
)

type testNode struct {
	id        int
	values    map[ColumnID]StyledString
	inherited []ColumnID
	children  []*testNode
}

func (n testNode) NodeID() interface{} {
	return n.id
}

func (n testNode) NodeChildren() []TableNode {
	nodes := make([]TableNode, 0, len(n.children))
	for _, child := range n.children {
		nodes = append(nodes, child)
	}
	return nodes
}

func (n testNode) Values(location *time.Location) map[ColumnID]StyledString {
	return n.values
}

func (n testNode) InheritedValues() []ColumnID {
	return n.inherited
}

const (
	column1 ColumnID = iota
	column2
	column3
	column4
)

func rowPaths(t HierarchicalTable) []nodePath {
	ps := make([]nodePath, 0, len(t.rows))
	for _, row := range t.rows {
		ps = append(ps, row.path)
	}
	return ps
}

type nodePaths []nodePath

func (ps nodePaths) Diff(others nodePaths) string {
	return cmp.Diff(ps, others, cmp.AllowUnexported(nodePath{}))
}

func TestHierarchicalTable_Scroll(t *testing.T) {
	t.Run("scrolling an empty table must have no effect at all", func(t *testing.T) {
		table, err := NewHierarchicalTable(nil, nil, 0, 3, nil)
		if err != nil {
			t.Fatal(err)
		}

		for _, amount := range []int{0, -9, 100, -999, +9999} {
			// Must not crash
			table.Scroll(amount)

			if table.pageIndex.Valid || table.cursorIndex.Valid {
				t.Fatal("table.pageIndex and table.cursorIndex must both have .Valid=false")
			}
		}
	})

	const pageSize = 4
	nodes := []TableNode{
		testNode{id: 1},
		testNode{id: 2},
		testNode{id: 3},
		testNode{id: 4},
		testNode{id: 5},
		testNode{id: 6},
	}

	testCases := []struct {
		name          string
		scrollAmounts []int
		pageIndex     nullInt
		cursorIndex   nullInt
	}{
		{
			name:          "scrolling to the middle of the page must move the cursor to that location",
			scrollAmounts: []int{pageSize / 2},
			pageIndex: nullInt{
				Valid: true,
				Int:   0,
			},
			cursorIndex: nullInt{
				Valid: true,
				Int:   pageSize / 2,
			},
		},
		{
			name:          "scrolling to the end of the page must move the cursor to that location",
			scrollAmounts: []int{pageSize - 1},
			pageIndex: nullInt{
				Valid: true,
				Int:   0,
			},
			cursorIndex: nullInt{
				Valid: true,
				Int:   pageSize - 1,
			},
		},
		{
			name:          "scrolling past the end of the page by 1 line must increase the page index by 1",
			scrollAmounts: []int{pageSize},
			pageIndex: nullInt{
				Valid: true,
				Int:   1,
			},
			cursorIndex: nullInt{
				Valid: true,
				Int:   pageSize,
			},
		},
		{
			name:          "scrolling past the end of the table must move the cursor to the last row",
			scrollAmounts: []int{len(nodes) + 1},
			pageIndex: nullInt{
				Valid: true,
				Int:   len(nodes) - pageSize,
			},
			cursorIndex: nullInt{
				Valid: true,
				Int:   len(nodes) - 1,
			},
		},

		{
			name:          "scrolling down and then up by half a page must not have any effect",
			scrollAmounts: []int{pageSize / 2, -pageSize / 2},
			pageIndex: nullInt{
				Valid: true,
				Int:   0,
			},
			cursorIndex: nullInt{
				Valid: true,
				Int:   0,
			},
		},

		{
			name:          "scrolling down and then up by one page must not have any effect",
			scrollAmounts: []int{pageSize, -pageSize},
			pageIndex: nullInt{
				Valid: true,
				Int:   0,
			},
			cursorIndex: nullInt{
				Valid: true,
				Int:   0,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			table, err := NewHierarchicalTable(nil, nodes, 0, pageSize+1, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, amount := range testCase.scrollAmounts {
				table.Scroll(amount)
			}

			if diff := testCase.pageIndex.Diff(table.pageIndex); diff != "" {
				t.Fatal(diff)
			}

			if diff := testCase.cursorIndex.Diff(table.cursorIndex); diff != "" {
				t.Fatal(diff)
			}
		})
	}
}

func TestHierarchicalTable_Replace(t *testing.T) {
	t.Run("traversable state of innerNodes must be preserved across calls to Replace()", func(t *testing.T) {
		table := HierarchicalTable{
			height:      10,
			columnWidth: make(map[ColumnID]int),
		}

		nodes := []TableNode{
			testNode{
				id: 1,
				children: []*testNode{
					{
						id: 2,
					},
				},
			},
			testNode{
				id: 3,
				children: []*testNode{
					{
						id: 4,
					},
				},
			},
		}

		// Load table with innerNodes. Only top-level innerNodes are visible at this point.
		table.Replace(nodes)
		expectedPaths := []nodePath{
			nodePathFromIDs(1),
			nodePathFromIDs(3),
		}
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}

		// Open the first node, one child becomes visible
		table.SetTraversable(true, false)
		expectedPaths = []nodePath{
			nodePathFromIDs(1),
			nodePathFromIDs(1, 2),
			nodePathFromIDs(3),
		}
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}

		// Reload the same innerNodes and check that the traversable state was preserved
		table.Replace(nodes)
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("emptying a table must invalidate both page and cursor indexes", func(t *testing.T) {
		table := HierarchicalTable{
			height:      10,
			columnWidth: make(map[ColumnID]int),
		}

		nodes := []TableNode{
			testNode{
				id: 1,
			},
		}

		table.Replace(nodes)
		table.Replace(nil)

		if table.pageIndex.Valid || table.cursorIndex.Valid {
			t.Fatal("table.pageIndex and table.cursorIndex must both have .Valid=false")
		}
	})

	t.Run("if the cursor was on the first row and the table was never scrolled the cursor must not move", func(t *testing.T) {
		table := HierarchicalTable{
			height:      10,
			columnWidth: make(map[ColumnID]int),
		}

		table.Replace([]TableNode{
			testNode{id: 1},
			testNode{id: 2},
		})

		table.Replace([]TableNode{
			testNode{id: 0},
			testNode{id: 1},
			testNode{id: 2},
		})

		expectedCursorIndex := nullInt{
			Valid: true,
			Int:   0,
		}
		if diff := cmp.Diff(expectedCursorIndex, table.cursorIndex); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("if the cursor was not on the first row it must track the row", func(t *testing.T) {
		table := HierarchicalTable{
			height:      10,
			columnWidth: make(map[ColumnID]int),
		}

		table.Replace([]TableNode{
			testNode{id: 1},
			testNode{id: 2},
		})

		table.Scroll(1)

		table.Replace([]TableNode{
			testNode{id: 0},
			testNode{id: 1},
			testNode{id: 2},
		})

		expectedCursorIndex := nullInt{
			Valid: true,
			Int:   2,
		}
		if diff := cmp.Diff(expectedCursorIndex, table.cursorIndex); diff != "" {
			t.Fatal(diff)
		}
	})
}

func TestHierarchicalTable_SetTraversable(t *testing.T) {
	nodes := []TableNode{
		testNode{
			id: 1,
			children: []*testNode{
				{
					id: 2,
					children: []*testNode{
						{
							id: 3,
						},
					},
				},
			},
		},
		testNode{
			id: 4,
			children: []*testNode{
				{
					id: 5,
					children: []*testNode{
						{
							id: 6,
						},
					},
				},
			},
		},
		testNode{
			id: 7,
		},
	}

	t.Run("opening the first row must make its first-degree children visible", func(t *testing.T) {
		table, err := NewHierarchicalTable(nil, nodes, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}

		table.SetTraversable(true, false)
		expectedPaths := []nodePath{
			nodePathFromIDs(1),
			nodePathFromIDs(1, 2),
			nodePathFromIDs(4),
			nodePathFromIDs(7),
		}
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("opening recursively the first row must make all its children visible", func(t *testing.T) {
		table, err := NewHierarchicalTable(nil, nodes, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}

		table.SetTraversable(true, true)
		expectedPaths := []nodePath{
			nodePathFromIDs(1),
			nodePathFromIDs(1, 2),
			nodePathFromIDs(1, 2, 3),
			nodePathFromIDs(4),
			nodePathFromIDs(7),
		}
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("opening and then closing the first row must have no visible effect", func(t *testing.T) {
		table, err := NewHierarchicalTable(nil, nodes, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}

		table.SetTraversable(true, true)
		table.SetTraversable(false, true)
		expectedPaths := []nodePath{
			nodePathFromIDs(1),
			nodePathFromIDs(4),
			nodePathFromIDs(7),
		}
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("opening recursively row '1' and then closing row '2' must hide row '3'", func(t *testing.T) {
		table, err := NewHierarchicalTable(nil, nodes, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}

		table.SetTraversable(true, true)
		table.Scroll(1)
		table.SetTraversable(false, true)
		expectedPaths := []nodePath{
			nodePathFromIDs(1),
			nodePathFromIDs(1, 2),
			nodePathFromIDs(4),
			nodePathFromIDs(7),
		}
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("closing a terminal node must not have any effect", func(t *testing.T) {
		table, err := NewHierarchicalTable(nil, nodes, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}

		table.SetTraversable(true, true)
		table.Scroll(2)
		table.SetTraversable(false, true)

		expectedCursorIndex := nullInt{
			Valid: true,
			Int:   2,
		}
		if diff := cmp.Diff(expectedCursorIndex, table.cursorIndex); diff != "" {
			t.Fatal(diff)
		}
	})
}

func TestHierarchicalTable_ScrollToMatch(t *testing.T) {
	nodes := []TableNode{
		testNode{
			id: 1,
			values: map[ColumnID]StyledString{
				column1: NewStyledString("1"),
			},
			children: []*testNode{
				{
					id: 2,
					values: map[ColumnID]StyledString{
						column1: NewStyledString("2"),
					},
				},
			},
		},
		testNode{
			id: 3,
			values: map[ColumnID]StyledString{
				column1: NewStyledString("3"),
			},
			children: []*testNode{
				{
					id: 4,
					values: map[ColumnID]StyledString{
						column1: NewStyledString("4"),
					},
				},
			},
		},
	}

	conf := ColumnConfiguration{
		column1: {
			Header:    "column1",
			Order:     0,
			MaxWidth:  42,
			Alignment: Left,
		},
	}

	t.Run("searching an empty table must return false", func(t *testing.T) {
		table, err := NewHierarchicalTable(conf, nil, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if table.ScrollToNextMatch("1", true) != false {
			t.Fatal("expected match NOT to be found")
		}
	})

	t.Run("searching must return true if a match exists and move the cursor to that row", func(t *testing.T) {
		table, err := NewHierarchicalTable(conf, nodes, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		table.SetTraversable(true, true)
		if table.ScrollToNextMatch("2", true) != true {
			t.Fatal("expected match to be found")
		}
		expectedCursorIndex := nullInt{
			Valid: true,
			Int:   1,
		}
		if diff := expectedCursorIndex.Diff(table.cursorIndex); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("searching backwards must return true if a match exists and move the cursor to that row", func(t *testing.T) {
		table, err := NewHierarchicalTable(conf, nodes, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if table.ScrollToNextMatch("3", false) != true {
			t.Fatal("expected match to be found")
		}
		expectedCursorIndex := nullInt{
			Valid: true,
			Int:   1,
		}
		if diff := expectedCursorIndex.Diff(table.cursorIndex); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("searching must cycle back at the top of the table if needed", func(t *testing.T) {
		table, err := NewHierarchicalTable(conf, nodes, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		table.SetTraversable(true, true)
		table.Scroll(1)
		if table.ScrollToNextMatch("1", true) != true {
			t.Fatal("expected match to be found")
		}
		expectedCursorIndex := nullInt{
			Valid: true,
			Int:   0,
		}
		if diff := expectedCursorIndex.Diff(table.cursorIndex); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("searching must ignore hidden innerNodes", func(t *testing.T) {
		table, err := NewHierarchicalTable(conf, nodes, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if table.ScrollToNextMatch("2", true) != false {
			t.Fatal("expected match NOT to be found")
		}
	})
}

func TestHierarchicalTable_Resize(t *testing.T) {
	nodes := []TableNode{
		testNode{
			id: 1,
			values: map[ColumnID]StyledString{
				column1: NewStyledString("1"),
			},
			children: []*testNode{
				{
					id: 2,
					values: map[ColumnID]StyledString{
						column1: NewStyledString("2"),
					},
				},
			},
		},
		testNode{
			id: 3,
			values: map[ColumnID]StyledString{
				column1: NewStyledString("3"),
			},
			children: []*testNode{
				{
					id: 4,
					values: map[ColumnID]StyledString{
						column1: NewStyledString("4"),
					},
				},
			},
		},
	}

	conf := ColumnConfiguration{
		column1: {
			Header:    "column1",
			Order:     0,
			MaxWidth:  42,
			Alignment: Left,
		},
	}

	t.Run("resizing the table to a height of 0 should move the cursor to the first row", func(t *testing.T) {
		table, err := NewHierarchicalTable(conf, nodes, 10, 4, nil)
		if err != nil {
			t.Fatal(err)
		}

		table.SetTraversable(true, true)
		table.Scroll(2)
		table.Resize(table.width, 0)
		table.Resize(table.width, 4)

		expected := nullInt{
			Valid: true,
			Int:   0,
		}
		if diff := cmp.Diff(expected, table.cursorIndex); diff != "" {
			t.Fatal(diff)
		}
		if diff := cmp.Diff(expected, table.pageIndex); diff != "" {
			t.Fatal(diff)
		}

	})
}

func TestHierarchicalTable_headers(t *testing.T) {
	t.Run("", func(t *testing.T) {
		conf := ColumnConfiguration{
			column1: {
				Header:    "column1",
				Order:     0,
				MaxWidth:  999,
				Alignment: Left,
			},
			column2: {
				Header:    "column2",
				Order:     1,
				MaxWidth:  999,
				Alignment: Left,
			},
			column3: {
				Header:    "column3",
				Order:     2,
				MaxWidth:  6,
				Alignment: Left,
			},
			column4: {
				Header:    "column4",
				Order:     3,
				MaxWidth:  6,
				Alignment: Right,
			},
		}

		table, err := NewHierarchicalTable(conf, nil, 0, 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		expectedHeader := strings.Join([]string{"column1 ", "column2 ", "column", "lumn4 "}, table.sep)
		table.Resize(runewidth.StringWidth(expectedHeader), table.height)

		header := table.styledString(table.headers(), "").String()
		if diff := cmp.Diff(expectedHeader, header); diff != "" {
			t.Fatal(diff)
		}
	})
}

func TestInnerTableNode_setPrefix(t *testing.T) {
	tree := innerTableNode{
		traversable: true,
		children: []*innerTableNode{
			{
				traversable: true,
				children: []*innerTableNode{
					{},
					{},
				},
			},
			{},
		},
	}

	tree.setPrefix("", true)

	expectedTree := innerTableNode{
		traversable: true,
		prefix:      "-",
		children: []*innerTableNode{
			{
				traversable: true,
				prefix:      " ├── ",
				children: []*innerTableNode{
					{
						prefix: " │   ├── ",
					},
					{
						prefix: " │   └── ",
					},
				},
			},
			{
				prefix: " └── ",
			},
		},
	}

	if diff := cmp.Diff(expectedTree, tree, cmp.AllowUnexported(innerTableNode{}, nodePath{})); diff != "" {
		t.Fatal(diff)
	}

}

func TestHierarchicalTable_SortBy(t *testing.T) {
	conf := ColumnConfiguration{
		column1: {
			Header:    "column1",
			Order:     0,
			MaxWidth:  999,
			Alignment: Left,
			Less: func(nodes []TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					ni := nodes[i].(testNode)
					nj := nodes[j].(testNode)

					if asc {
						return ni.id < nj.id
					} else {
						return ni.id > nj.id
					}
				}
			},
		},
		column2: {
			Header:    "column2",
			Order:     1,
			MaxWidth:  999,
			Alignment: Left,
		},
	}

	nodes := []TableNode{
		testNode{id: 1},
		testNode{id: 4},
		testNode{id: 3},
		testNode{id: 2},
	}

	t.Run("sort order must be preserved on table creation", func(t *testing.T) {
		table, err := NewHierarchicalTable(conf, nodes, 10, 10, nil)
		if err != nil {
			t.Fatal(err)
		}

		expectedPaths := []nodePath{
			nodePathFromIDs(1),
			nodePathFromIDs(4),
			nodePathFromIDs(3),
			nodePathFromIDs(2),
		}
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("sorting on a column without a comparison function must not have any effect", func(t *testing.T) {
		table, err := NewHierarchicalTable(conf, nodes, 10, 10, nil)
		if err != nil {
			t.Fatal(err)
		}

		table.SortBy(column2, true)

		expectedPaths := []nodePath{
			nodePathFromIDs(1),
			nodePathFromIDs(4),
			nodePathFromIDs(3),
			nodePathFromIDs(2),
		}
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("after sorting, nodes must be ordered as specified", func(t *testing.T) {
		table, err := NewHierarchicalTable(conf, nodes, 10, 10, nil)
		if err != nil {
			t.Fatal(err)
		}

		table.SortBy(column1, true)

		expectedPaths := []nodePath{
			nodePathFromIDs(1),
			nodePathFromIDs(2),
			nodePathFromIDs(3),
			nodePathFromIDs(4),
		}
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("table headers must reflect sorting order", func(t *testing.T) {
		table, err := NewHierarchicalTable(conf, nodes, 10, 10, nil)
		if err != nil {
			t.Fatal(err)
		}

		table.SortBy(column1, false)

		header := table.headers()[column1].String()
		expectedHeader := "column1▼"

		if header != expectedHeader {
			t.Fatalf("expected header %q but got %q", expectedHeader, header)
		}
	})

	t.Run("sort order must be preserved across Replace calls", func(t *testing.T) {
		table, err := NewHierarchicalTable(conf, nodes, 10, 10, nil)
		if err != nil {
			t.Fatal(err)
		}

		table.SortBy(column1, false)

		expectedPaths := []nodePath{
			nodePathFromIDs(4),
			nodePathFromIDs(3),
			nodePathFromIDs(2),
			nodePathFromIDs(1),
		}
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}

		table.Replace(nodes)
		if diff := nodePaths(expectedPaths).Diff(rowPaths(table)); diff != "" {
			t.Fatal(diff)
		}
	})
}
