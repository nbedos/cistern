package tui

import (
	"context"
	"errors"
	"os"
	"path"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
)

type Table struct {
	source     cache.HierarchicalTabularDataSource
	nodes      []cache.HierarchicalTabularSourceRow
	rows       []cache.HierarchicalTabularSourceRow
	topLine    int
	activeLine int
	height     int
	width      int
	sep        string
	maxWidths  map[string]int
	location   *time.Location
}

func NewTable(source cache.HierarchicalTabularDataSource, width int, height int, loc *time.Location) (Table, error) {
	if width < 0 || height < 0 {
		return Table{}, errors.New("table width and height must be >= 0")
	}

	table := Table{
		source:    source,
		height:    height,
		width:     width,
		maxWidths: make(map[string]int),
		sep:       "  ", // FIXME Move this out of here
		location:  loc,
	}

	table.Refresh()

	return table, nil
}

func (t Table) NbrRows() int {
	return utils.MaxInt(0, t.height-1)
}

func (t *Table) computeMaxWidths() {
	for _, header := range t.source.Headers() {
		t.maxWidths[header] = utils.MaxInt(t.maxWidths[header], runewidth.StringWidth(header))
	}
	for _, row := range t.rows {
		for header, value := range row.Tabular(t.location) {
			t.maxWidths[header] = utils.MaxInt(t.maxWidths[header], value.Length())
		}
	}
}

func (t *Table) Refresh() {
	// Save traversable state of current nodes
	traversables := make(map[interface{}]struct{})
	for i := range t.nodes {
		rowTraversal := utils.DepthFirstTraversal(t.nodes[i], true)
		for j := range rowTraversal {
			if row := rowTraversal[j].(cache.HierarchicalTabularSourceRow); row.Traversable() {
				traversables[row.Key()] = struct{}{}
			}
		}
	}

	// Fetch all nodes from DataSource and restore traversable state
	nodes := t.source.Rows()
	t.nodes = make([]cache.HierarchicalTabularSourceRow, 0, len(nodes))
	for _, node := range nodes {
		for _, childRow := range utils.DepthFirstTraversal(node, true) {
			childRow := childRow.(cache.HierarchicalTabularSourceRow)
			_, exists := traversables[childRow.Key()]
			childRow.SetTraversable(exists, false)
		}
		t.nodes = append(t.nodes, node)
	}

	// Traverse all nodes in depth-first order to build t.rows
	var activeKey interface{} = nil
	if t.activeLine >= 0 && t.activeLine < len(t.rows) {
		activeKey = t.rows[t.activeLine].Key()
	}
	t.rows = make([]cache.HierarchicalTabularSourceRow, 0, len(t.nodes))
	for _, node := range t.nodes {
		cache.Prefix(node, "", true)
		for _, childRow := range utils.DepthFirstTraversal(node, false) {
			t.rows = append(t.rows, childRow.(cache.HierarchicalTabularSourceRow))
			// change t.activeline so that the same row stays active, except if t.activeLine == 0
			if t.activeLine != 0 && activeKey != nil && t.rows[len(t.rows)-1].Key() == activeKey {
				t.activeLine = len(t.rows) - 1
			}
		}
	}

	if len(t.rows) == 0 {
		t.topLine = 0
		t.activeLine = 0
	} else {
		t.topLine = utils.Bounded(t.topLine, 0, len(t.rows)-1)
		if t.NbrRows() == 0 {
			t.activeLine = t.topLine
		} else {
			t.activeLine = utils.Bounded(t.activeLine, t.topLine, t.topLine+t.NbrRows())
		}
	}

	t.computeMaxWidths()
}

func (t *Table) SetTraversable(open bool, recursive bool) {
	if t.activeLine >= 0 && t.activeLine < len(t.rows) {
		t.rows[t.activeLine].SetTraversable(open, recursive)
		t.Refresh() // meh. That's simpler but not needed
	}
}

func (t *Table) Scroll(amount int) {
	activeLine := utils.Bounded(t.activeLine+amount, 0, len(t.rows)-1)
	switch {
	case activeLine < t.topLine:
		t.topLine = activeLine
		t.activeLine = activeLine
	case activeLine > t.topLine+t.NbrRows()-1:
		scrollAmount := activeLine - (t.topLine + t.NbrRows() - 1)
		t.topLine = utils.Bounded(t.topLine+scrollAmount, 0, len(t.rows)-1)
		t.activeLine = t.topLine + t.NbrRows() - 1
	default:
		t.activeLine = activeLine
	}
}

func (t *Table) Top() {
	t.Scroll(-len(t.rows))
}

func (t *Table) Bottom() {
	t.Scroll(len(t.rows))
}

func (t *Table) NextMatch(s string, ascending bool) bool {
	if len(t.rows) == 0 {
		return false
	}

	step := 1
	if !ascending {
		step = -1
	}
	start := utils.Modulo(t.activeLine+step, len(t.rows))
	next := func(i int) int {
		return utils.Modulo(i+step, len(t.rows))
	}
	for i := start; i != t.activeLine; i = next(i) {
		row := t.rows[i]
		for _, styledString := range row.Tabular(t.location) {
			if styledString.Contains(s) {
				t.Scroll(i - t.activeLine)
				return true
			}
		}
	}

	return false
}

func (t Table) stringFromColumns(values map[string]text.StyledString, header bool) text.StyledString {
	paddedColumns := make([]text.StyledString, len(t.source.Headers()))
	for j, name := range t.source.Headers() {
		alignment := text.Left
		if !header {
			alignment = t.source.Alignment()[name]
		}
		paddedColumns[j] = values[name]
		paddedColumns[j].Align(alignment, t.maxWidths[name])
	}

	line := text.Join(paddedColumns, text.NewStyledString(t.sep))
	line.Align(text.Left, t.width)

	return line
}

func (t Table) Size() (int, int) {
	return t.width, t.height
}

func (t *Table) Resize(width int, height int) {
	width = utils.MaxInt(width, 0)
	height = utils.MaxInt(height, 0)

	if height == 0 {
		t.activeLine = 0
	} else {
		t.activeLine = utils.Bounded(t.activeLine, t.topLine, t.topLine+height-1)
	}
	t.width, t.height = width, height
}

func (t *Table) Text() []text.LocalizedStyledString {
	texts := make([]text.LocalizedStyledString, 0, len(t.rows))

	if t.height > 0 {
		headers := make(map[string]text.StyledString)
		for _, header := range t.source.Headers() {
			headers[header] = text.NewStyledString(header)
		}

		s := t.stringFromColumns(headers, true)
		s.Add(text.TableHeader)
		texts = append(texts, text.LocalizedStyledString{
			X: 0,
			Y: 0,
			S: s,
		})
	}

	for i := 0; i < t.NbrRows() && t.topLine+i < len(t.rows); i++ {
		row := t.rows[t.topLine+i]
		s := text.LocalizedStyledString{
			X: 0,
			Y: i + 1,
			S: t.stringFromColumns(row.Tabular(t.location), false),
		}

		if t.topLine+i == t.activeLine {
			s.S.Add(text.ActiveRow)
		}

		texts = append(texts, s)
	}

	return texts
}

func (t Table) OpenInBrowser(browser string) error {
	if t.activeLine >= 0 && t.activeLine < len(t.rows) {
		if url := t.rows[t.activeLine].URL(); url != "" {
			argv := []string{path.Base(browser), url}
			process, err := os.StartProcess(browser, argv, &os.ProcAttr{})
			if err != nil {
				return err
			}

			if err := process.Release(); err != nil {
				return err
			}
		}
	}

	return nil
}

func (t *Table) WriteToDisk(ctx context.Context, dir string) (string, error) {
	if t.activeLine >= 0 && t.activeLine < len(t.rows) {

	}
	key := t.rows[t.activeLine].Key()
	return t.source.WriteToDisk(ctx, key, dir)
}
