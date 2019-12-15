package tui

import (
	"context"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
)

type testRow struct {
	value       string
	prefix      string
	traversable bool
	children    []testRow
}

func (r testRow) String() string {
	return r.value
}

func (r testRow) Children() []utils.TreeNode {
	children := make([]utils.TreeNode, len(r.children))
	for i := range r.children {
		children[i] = &r.children[i]
	}
	return children
}

func (r testRow) Traversable() bool {
	return r.traversable
}

func (r *testRow) SetTraversable(open bool, recursive bool) {
	r.traversable = open
	if recursive {
		for _, node := range r.children {
			node.SetTraversable(open, recursive)
		}
	}
}

func (r *testRow) SetPrefix(s string) {
	r.prefix = s
}

func (r *testRow) Tabular(loc *time.Location) map[string]text.StyledString {
	return map[string]text.StyledString{
		"VALUE": text.NewStyledString(r.value),
	}
}

func (r testRow) Key() interface{} {
	return r.value
}

func (r testRow) URL() utils.NullString {
	return utils.NullString{
		String: r.value,
		Valid:  true,
	}
}

type testSource struct {
	rows []testRow
}

func (s testSource) Rows() []cache.HierarchicalTabularSourceRow {
	rows := make([]cache.HierarchicalTabularSourceRow, 0, len(s.rows))
	for _, row := range s.rows {
		row := row
		rows = append(rows, &row)
	}
	return rows
}

func (s testSource) Headers() []string { return []string{"VALUE"} }
func (s testSource) Alignment() map[string]text.Alignment {
	return map[string]text.Alignment{"VALUE": text.Left}
}

func (s testSource) Log(context.Context, interface{}) (string, error) {
	return "", nil
}

var source = testSource{
	rows: []testRow{
		{value: "a"},
		{value: "b"},
		{
			value:       "c",
			prefix:      "",
			traversable: true,
			children: []testRow{
				{value: "c.d"},
				{value: "c.e"},
			},
		},
		{value: "f"},
		{value: "g"},
	},
}

var emptySource = testSource{}

var longSource = testSource{
	rows: []testRow{
		{value: "_"},
		{value: "a"},
		{value: "b"},
		{value: "c"},
		{value: "d"},
		{value: "e"},
		{value: "f"},
		{value: "g"},
	},
}

func TestNewTable(t *testing.T) {
	t.Run("nodes in table must match source depth-first rows", func(t *testing.T) {
		_, err := NewTable(emptySource, 10, 10, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("nodes in table must match source depth-first rows", func(t *testing.T) {
		table, err := NewTable(source, 10, 10, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		expected := []string{"a", "b", "c", "f", "g"}
		if len(table.rows) != len(expected) {
			t.Fatalf("wrong number of element in table: expected %d but got %d", len(expected),
				len(table.rows))
		}
		for i, row := range table.rows {
			if row.(*testRow).value != expected[i] {
				t.Fatalf("expected %q but got %q", expected[i], row.(*testRow).value)
			}
		}

		expectedMaxWidth := runewidth.StringWidth("VALUE")
		if table.maxWidths["VALUE"] != expectedMaxWidth {
			t.Fatalf("expected table.maxWidths[\"VALUE\"] == %d but got %d",
				expectedMaxWidth, table.maxWidths["VALUE"])
		}
	})
}

func TestTable_Refresh(t *testing.T) {
	t.Run("empty table before refresh, non-empty after", func(t *testing.T) {
		table, err := NewTable(emptySource, 10, 10, time.UTC)
		if err != nil {
			t.Fatal(err)
		}

		table.source = source
		table.Refresh()

		expected := []string{"a", "b", "c", "f", "g"}
		if len(table.rows) != len(expected) {
			t.Fatalf("wrong number of element in table: expected %d but got %d", len(expected),
				len(table.rows))
		}
		for i, row := range table.rows {
			if row.(*testRow).value != expected[i] {
				t.Fatalf("expected %q but got %q", expected[i], row.(*testRow).value)
			}
		}

		expectedMaxWidth := runewidth.StringWidth("VALUE")
		if table.maxWidths["VALUE"] != expectedMaxWidth {
			t.Fatalf("expected table.maxWidths[\"VALUE\"] == %d but got %d",
				expectedMaxWidth, table.maxWidths["VALUE"])
		}
	})

	t.Run("non-empty table before refresh, empty after", func(t *testing.T) {
		table, err := NewTable(source, 10, 10, time.UTC)
		if err != nil {
			t.Fatal(err)
		}

		table.source = emptySource
		table.Refresh()

		if len(table.rows) != 0 {
			t.Fatalf("expected 0 row but got %d", len(table.rows))
		}

		// MaxWidths should be preserved across row deletion
		expectedMaxWidth := runewidth.StringWidth("VALUE")
		if table.maxWidths["VALUE"] != expectedMaxWidth {
			t.Fatalf("expected table.maxWidths[\"VALUE\"] == %d but got %d",
				expectedMaxWidth, table.maxWidths["VALUE"])
		}
	})

	t.Run("if possible, the same row should remain active across calls to refresh", func(t *testing.T) {
		table, err := NewTable(source, 10, 10, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		// Move activeLine to row "c"
		table.activeLine = 2

		table.source = longSource
		table.Refresh()

		// Active line must be moved to row "c"
		expected := 3
		if table.activeLine != expected {
			t.Fatalf("expected table.activeLine == %d but got %d", expected, table.activeLine)
		}
	})
}

func TestTable_SetTraversable(t *testing.T) {
	t.Run("unfolding row 'c' must bring the total number of nodes to 5", func(t *testing.T) {
		table, err := NewTable(source, 10, 10, time.UTC)
		if err != nil {
			t.Fatal(err)
		}

		// Move active line to row "c"
		table.activeLine = 2
		table.SetTraversable(true, false)

		expected := []string{"a", "b", "c", "c.d", "c.e", "f", "g"}
		if len(table.rows) != len(expected) {
			t.Fatalf("wrong number of element in table: expected %d but got %d", len(expected),
				len(table.rows))
		}
		for i, row := range table.rows {
			if row.(*testRow).value != expected[i] {
				t.Fatalf("expected %q but got %q", expected[i], row.(*testRow).value)
			}
		}
	})
}

func TestTable_Scroll(t *testing.T) {
	testCases := []struct {
		name               string
		activeLine         int
		topLine            int
		scrollAmount       int
		expectedTopLine    int
		expectedActiveLine int
	}{
		{
			name:               "move activeLine from 0 to 1",
			activeLine:         0,
			topLine:            0,
			scrollAmount:       1,
			expectedTopLine:    0,
			expectedActiveLine: 1,
		},
		{
			name:               "move activeLine from 0 to 3 and topLine to 1",
			activeLine:         0,
			topLine:            0,
			scrollAmount:       3,
			expectedTopLine:    1,
			expectedActiveLine: 3,
		},
		{
			name:               "move activeLine to 4 and topLine to 2",
			activeLine:         0,
			topLine:            0,
			scrollAmount:       4,
			expectedTopLine:    2,
			expectedActiveLine: 4,
		},
		{
			name:               "move activeLine to 7 and topLine to 5",
			activeLine:         0,
			topLine:            0,
			scrollAmount:       999,
			expectedTopLine:    5,
			expectedActiveLine: 7,
		},
		{
			name:               "move activeLine to 0 and topLine to 0",
			activeLine:         3,
			topLine:            3,
			scrollAmount:       -999,
			expectedTopLine:    0,
			expectedActiveLine: 0,
		},
	}

	for _, testCase := range testCases {
		t.Run("", func(t *testing.T) {
			table, err := NewTable(longSource, 10, 4, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			table.activeLine = testCase.activeLine
			table.topLine = testCase.topLine

			table.Scroll(testCase.scrollAmount)

			if table.topLine != testCase.expectedTopLine {
				t.Fatalf("expected topLine %d but got %d", testCase.expectedTopLine,
					table.topLine)
			}
			if table.activeLine != testCase.expectedActiveLine {
				t.Fatalf("expected topLine %d but got %d", testCase.expectedActiveLine,
					table.activeLine)
			}
		})
	}
}

func TestTable_Resize(t *testing.T) {
	t.Run("zeroed height and width must not cause any error", func(t *testing.T) {
		table, err := NewTable(longSource, 10, 4, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		table.Resize(0, 0)

		for _, amount := range []int{0, 1, 999, -1, -999, 1, 0} {
			table.Scroll(amount)
		}

		table.Refresh()
		if texts := table.Text(); len(texts) > 0 {
			t.Fatal("Text() must return an empty list if height == 0")
		}
	})
}

func TestTable_Text(t *testing.T) {
	t.Run("zeroed height and width must not cause any error", func(t *testing.T) {
		table, err := NewTable(source, 10, 10, time.UTC)
		if err != nil {
			t.Fatal(err)
		}

		for _, height := range []int{0, 1, 5, 10} {
			table.Resize(10, height)
			table.Text()

			//TODO
		}
	})
}

func TestTable_NextMatch(t *testing.T) {
	testCases := []struct {
		name               string
		s                  string
		activeLine         int
		ascending          bool
		expectedMatched    bool
		expectedActiveLine int
	}{
		{
			name:               "t.activeLine must no change if there is no match",
			s:                  "this won't be found",
			ascending:          true,
			activeLine:         0,
			expectedMatched:    false,
			expectedActiveLine: 0,
		},
		{
			name:               "activeLine should be changed to first match (ascending)",
			s:                  "b",
			ascending:          true,
			activeLine:         0,
			expectedMatched:    true,
			expectedActiveLine: 1,
		},
		{
			name:               "activeLine should be changed to first match (descending)",
			s:                  "b",
			ascending:          false,
			activeLine:         0,
			expectedMatched:    true,
			expectedActiveLine: 1,
		},
		{
			name:               "search starting after match should still result in match (ascending)",
			s:                  "b",
			ascending:          true,
			activeLine:         2,
			expectedMatched:    true,
			expectedActiveLine: 1,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			table, err := NewTable(source, 10, 10, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			table.activeLine = testCase.activeLine

			matched := table.NextMatch(testCase.s, testCase.ascending)

			if matched != testCase.expectedMatched {
				t.Fatalf("expected matched == %v but got %v", testCase.expectedMatched, matched)
			}
			if table.activeLine != testCase.expectedActiveLine {
				t.Fatalf("expected table.activeLine == %d but got %d", testCase.expectedActiveLine,
					table.activeLine)
			}
		})
	}

	t.Run("nextMatch on an empty table must return false", func(t *testing.T) {
		table, err := NewTable(emptySource, 10, 10, time.UTC)
		if err != nil {
			t.Fatal(err)
		}

		if table.NextMatch("", true) {
			t.Fail()
		}
	})

}
