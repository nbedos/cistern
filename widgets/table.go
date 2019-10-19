package widgets

import (
	"errors"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
	"strings"
)

type Table struct {
	Source     cache.HierarchicalTabularDataSource
	ActiveLine int
	Columns    []string
	Rows       []cache.TabularSourceRow
	height     int
	width      int
	Sep        string
}

// FIXME Remove this. There is no need for custom column names.
type Mapping struct {
	From string
	To   string
}

func NewTable(source cache.HierarchicalTabularDataSource, columns []string, width int, height int, sep string) (Table, error) {
	if width < 0 || height < 0 {
		return Table{}, errors.New("table width and height must be >= 0")
	}

	table := Table{
		Source:  source,
		height:  height,
		width:   width,
		Columns: columns,
		Sep:     sep,
	}

	res, err := table.Source.SelectFirst(table.nbrRows())
	if err != nil {
		return table, err
	}

	table.setRows(res)

	return table, nil
}

func (t Table) nbrRows() int {
	return utils.MaxInt(0, t.height-1)
}

func (t *Table) Refresh() error {
	err := t.Source.FetchRows()
	if err != nil {
		return err
	}

	var rows []cache.TabularSourceRow
	activeLine := t.ActiveLine
	if len(t.Rows) > 0 {
		activeKey := t.Rows[t.ActiveLine].Key()
		rows, activeLine, err = t.Source.Select(activeKey, t.ActiveLine, t.nbrRows()-t.ActiveLine-1)
	} else {
		rows, err = t.Source.SelectFirst(t.nbrRows())
	}

	if err != nil {
		return err
	}
	t.setRows(rows)
	t.setActiveLine(activeLine)

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

	t.setRows(rows)
	t.setActiveLine(activeline)

	return nil
}

func (t Table) Size() (int, int) {
	return t.width, t.height
}

func (t *Table) Resize(width int, height int) error {
	if width < 0 || height < 0 {
		return errors.New("width and height must be >= 0")
	}

	var rows []cache.TabularSourceRow
	var err error
	if len(t.Rows) > 0 {
		// FIXME Keep same row active after resize, ideally also preserve ratio t.ActiveLine / len(t.Rows)
		var activeline int
		key := t.Rows[t.ActiveLine].Key()
		rows, activeline, err = t.Source.Select(key, t.ActiveLine, t.nbrRows()-1-t.ActiveLine)
		if err != nil {
			return err
		}
		t.setActiveLine(activeline)
	} else {
		rows, err = t.Source.SelectFirst(t.nbrRows())
	}
	if err != nil {
		return err
	}

	t.width, t.height = width, height
	t.setRows(rows)

	return nil
}

func (t *Table) Top() error {
	res, err := t.Source.SelectFirst(t.nbrRows())
	if err != nil {
		return err
	}

	t.setRows(res)
	t.setActiveLine(0)
	return nil
}

func (t *Table) Bottom() error {
	res, err := t.Source.SelectLast(t.nbrRows())
	if err != nil {
		return err
	}

	t.setRows(res)
	t.setActiveLine(len(t.Rows) - 1)
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
		t.setRows(rows)
	}
	t.setActiveLine(activeLine)

	return nil
}

func (t *Table) setRows(rows []cache.TabularSourceRow) {
	if len(t.Rows) > t.height {
		t.Rows = rows[:t.height-1]
	} else {
		t.Rows = rows
	}

	t.setActiveLine(t.ActiveLine)
}

func (t *Table) setActiveLine(activeLine int) {
	t.ActiveLine = utils.Bounded(activeLine, 0, len(t.Rows)-1)
}

func (t Table) stringFromColumns(values map[string]string, widths map[string]int) string {
	paddedColumns := make([]string, len(t.Columns))
	for j, name := range t.Columns {
		paddedColumns[j] = align(values[name], widths[name], Left)
	}

	return align(strings.Join(paddedColumns, t.Sep), t.width, Left)
}

func (t *Table) Text() ([]StyledText, error) {
	texts := make([]StyledText, 0, len(t.Rows))
	maxWidths := t.Source.MaxWidths()

	// FIXME meh.
	headers := make(map[string]string)
	for _, header := range t.Columns {
		headers[header] = header
	}

	texts = append(texts, StyledText{
		Content: t.stringFromColumns(headers, maxWidths),
		Class:   TableHeader,
	})

	for i, tableRow := range t.Rows {
		columns := tableRow.Tabular()

		text := StyledText{
			X:       0,
			Y:       i + 1,
			Content: t.stringFromColumns(columns, maxWidths),
		}

		if text.Y == t.ActiveLine+1 {
			text.Class = ActiveRow
		}

		texts = append(texts, text)
	}

	return texts, nil
}
