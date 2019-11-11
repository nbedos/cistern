package widgets

import (
	"context"
	"errors"
	"os"
	"path"

	"github.com/mattn/go-runewidth"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
)

type Table struct {
	Source     cache.HierarchicalTabularDataSource
	ActiveLine int
	Rows       []cache.TabularSourceRow
	height     int
	width      int
	sep        string
	maxWidths  map[string]int
}

func NewTable(source cache.HierarchicalTabularDataSource, width int, height int, sep string) (Table, error) {
	if width < 0 || height < 0 {
		return Table{}, errors.New("table width and height must be >= 0")
	}

	table := Table{
		Source:    source,
		height:    height,
		width:     width,
		sep:       sep,
		maxWidths: make(map[string]int),
	}

	res, err := table.Source.SelectFirst(table.nbrRows())
	if err != nil {
		return table, err
	}

	table.setRows(res, 0)

	return table, nil
}

func (t Table) nbrRows() int {
	return utils.MaxInt(0, t.height-1)
}

func (t *Table) Refresh() error {
	t.Source.FetchRows()

	var err error
	var rows []cache.TabularSourceRow
	activeLine := t.ActiveLine
	if len(t.Rows) > 0 && t.ActiveLine > 0 {
		activeKey := t.Rows[t.ActiveLine].Key()
		rows, activeLine, err = t.Source.Select(activeKey, t.ActiveLine, t.nbrRows()-t.ActiveLine-1)
	} else {
		rows, err = t.Source.SelectFirst(t.nbrRows())
	}

	if err != nil {
		return err
	}

	if t.ActiveLine == 0 {
		activeLine = 0
	}

	t.setRows(rows, activeLine)

	return nil
}

func (t *Table) SetFold(open bool, recursive bool) error {
	if t.ActiveLine < 0 || t.ActiveLine >= len(t.Rows) {
		return nil
	}

	activeKey := t.Rows[t.ActiveLine].Key()
	if err := t.Source.SetTraversable(activeKey, open, recursive); err != nil {
		return err
	}
	rows, activeline, err := t.Source.Select(activeKey, t.ActiveLine, t.nbrRows()-t.ActiveLine-1)
	if err != nil {
		return err
	}

	t.setRows(rows, activeline)

	return nil
}

func (t Table) Size() (int, int) {
	return t.width, t.height
}

func (t *Table) NextMatch(s string, ascending bool) bool {
	if len(t.Rows) == 0 {
		return false
	}

	top := t.Rows[0].Key()
	bottom := t.Rows[len(t.Rows)-1].Key()
	active := t.Rows[t.ActiveLine].Key()
	rows, i, err := t.Source.NextMatch(top, bottom, active, s, ascending)
	if err == cache.ErrNoMatchFound {
		return false
	}
	t.setRows(rows, i)
	return true
}

func (t *Table) Resize(width int, height int) error {
	if width < 0 || height < 0 {
		return errors.New("width and height must be >= 0")
	}

	var rows []cache.TabularSourceRow
	var err error
	var activeline int
	if len(t.Rows) > 0 && t.ActiveLine > 0 {
		key := t.Rows[t.ActiveLine].Key()
		rows, activeline, err = t.Source.Select(key, t.ActiveLine, height-2-t.ActiveLine)
	} else {
		rows, err = t.Source.SelectFirst(t.nbrRows())
	}
	if err != nil {
		return err
	}

	t.width, t.height = width, height
	t.setRows(rows, activeline)

	return nil
}

func (t *Table) Top() error {
	res, err := t.Source.SelectFirst(t.nbrRows())
	if err != nil {
		return err
	}

	t.setRows(res, 0)
	return nil
}

func (t *Table) Bottom() error {
	res, err := t.Source.SelectLast(t.nbrRows())
	if err != nil {
		return err
	}

	t.setRows(res, -1)
	return nil
}

func (t *Table) Scroll(amount int) error {
	if len(t.Rows) == 0 {
		return nil
	}
	activeLine := t.ActiveLine + amount

	if activeLine < 0 {
		activeLine = 0
	} else if activeLine > len(t.Rows)-1 {
		activeLine = len(t.Rows) - 1
	}

	scrollAmount := amount - (activeLine - t.ActiveLine)
	// If we've reached the top or the bottom, fetch new data
	if scrollAmount != 0 {
		var rows []cache.TabularSourceRow
		var err error
		if scrollAmount > 0 {
			rows, _, err = t.Source.Select(t.Rows[0].Key(), 0, t.nbrRows()+scrollAmount-1)
			if err != nil {
				return err
			}
			if len(rows) > t.nbrRows() {
				rows = rows[len(rows)-t.nbrRows():]
			}
		} else if scrollAmount < 0 {
			scrollAmount *= -1
			key := t.Rows[len(t.Rows)-1].Key()
			rows, _, err = t.Source.Select(key, t.nbrRows()+scrollAmount-1, 0)
			if err != nil {
				return err
			}
			if len(rows) > t.nbrRows() {
				rows = rows[:t.nbrRows()]
			}
		}
		t.setRows(rows, activeLine)
	}
	t.setActiveLine(activeLine)

	return nil
}

func (t *Table) setRows(rows []cache.TabularSourceRow, activeline int) {
	if len(t.Rows) > t.height {
		t.Rows = rows[:t.height-1]
	} else {
		t.Rows = rows
	}

	t.setActiveLine(activeline)
	t.computeMaxWidths()
}

func (t *Table) computeMaxWidths() {
	for _, header := range t.Source.Headers() {
		t.maxWidths[header] = utils.MaxInt(t.maxWidths[header], runewidth.StringWidth(header))
	}
	for _, row := range t.Rows {
		for header, value := range row.Tabular() {
			t.maxWidths[header] = utils.MaxInt(t.maxWidths[header], value.Length())
		}
	}
}

func (t *Table) setActiveLine(activeLine int) {
	t.ActiveLine = utils.Bounded(activeLine, 0, len(t.Rows)-1)
}

func (t Table) stringFromColumns(values map[string]text.StyledString, header bool) text.StyledString {
	paddedColumns := make([]text.StyledString, len(t.Source.Headers()))
	for j, name := range t.Source.Headers() {
		alignment := text.Left
		if !header {
			alignment = t.Source.Alignment()[name]
		}
		paddedColumns[j] = values[name]
		paddedColumns[j].Align(alignment, t.maxWidths[name])
	}

	line := text.Join(paddedColumns, t.sep)
	line.Align(text.Left, t.width)

	return line
}

func (t *Table) Text() []text.LocalizedStyledString {
	texts := make([]text.LocalizedStyledString, 0, len(t.Rows))

	headers := make(map[string]text.StyledString)
	for _, header := range t.Source.Headers() {
		headers[header] = text.NewStyledString(header)
	}

	s := t.stringFromColumns(headers, true)
	s.Add(text.TableHeader)
	texts = append(texts, text.LocalizedStyledString{
		X: 0,
		Y: 0,
		S: s,
	})

	for i, row := range t.Rows {
		tx := text.LocalizedStyledString{
			X: 0,
			Y: i + 1,
			S: t.stringFromColumns(row.Tabular(), false),
		}

		if tx.Y == t.ActiveLine+1 {
			tx.S.Add(text.ActiveRow)
		}

		texts = append(texts, tx)
	}

	return texts
}

func (t Table) OpenInBrowser(browser string) error {
	if t.ActiveLine >= 0 && t.ActiveLine < len(t.Rows) {
		if url := t.Rows[t.ActiveLine].URL(); url != "" {
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

func (t *Table) WriteToDisk(ctx context.Context, dir string) (string, cache.Streamer, error) {
	if t.ActiveLine < 0 || t.ActiveLine >= len(t.Rows) {
		return "", nil, errors.New("t.Activeline is out of range")
	}

	key := t.Rows[t.ActiveLine].Key()
	return t.Source.WriteToDisk(ctx, key, dir)
}
