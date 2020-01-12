package tui

import (
	"errors"
	"fmt"
	"sort"

	"github.com/gdamore/tcell"
	"github.com/google/go-cmp/cmp"
	"github.com/mattn/go-runewidth"
	"github.com/nbedos/cistern/utils"
)

type nodeID interface{}

type TableNode interface {
	// Unique identifier of this node among its siblings
	NodeID() interface{}
	NodeChildren() []TableNode
	Values(v interface{}) map[ColumnID]StyledString
	InheritedValues() []ColumnID
	Compare(other TableNode, id ColumnID, v interface{}) int
}

func (n *innerTableNode) setPrefix(parent string, isLastChild bool) {
	if parent == "" {
		switch {
		case len(n.children) == 0:
			n.prefix = " "
		case n.traversable:
			n.prefix = "-"
		default:
			n.prefix = "+"
		}
		for i, child := range n.children {
			child.setPrefix(" ", i == len(n.children)-1)
		}
	} else {
		n.prefix = parent
		if isLastChild {
			n.prefix += "└─"
		} else {
			n.prefix += "├─"
		}

		if len(n.children) == 0 || n.traversable {
			n.prefix += "─ "
		} else {
			n.prefix += "+ "
		}

		for i, child := range n.children {
			if childIsLastChild := i == len(n.children)-1; isLastChild {
				child.setPrefix(parent+"    ", childIsLastChild)
			} else {
				child.setPrefix(parent+"│   ", childIsLastChild)
			}
		}
	}
}

type Column struct {
	Header     string
	Position   int
	MaxWidth   int
	Alignment  Alignment
	TreePrefix bool
}

type ColumnID int

type ColumnConfiguration map[ColumnID]Column

func (c ColumnConfiguration) IDs() []ColumnID {
	ids := make([]ColumnID, 0, len(c))
	for id := range c {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(i, j int) bool {
		return c[ids[i]].Position < c[ids[j]].Position
	})

	return ids
}

type nullInt struct {
	Valid bool
	Int   int
}

func (i nullInt) Diff(other nullInt) string {
	return cmp.Diff(i, other, cmp.AllowUnexported(nullInt{}))
}

const maxTreeDepth = 10

type nodePath struct {
	ids [maxTreeDepth]nodeID
	len int
}

func nodePathFromIDs(ids ...nodeID) nodePath {
	return nodePath{}.append(ids...)
}

func (p nodePath) append(ids ...nodeID) nodePath {
	for _, id := range ids {
		if p.len >= len(p.ids) {
			panic(fmt.Sprintf("path length cannot exceed %d", len(p.ids)))
		}

		p.ids[p.len] = id
		p.len++
	}
	return p
}

type innerTableNode struct {
	path        nodePath
	prefix      string
	traversable bool
	values      map[ColumnID]StyledString
	children    []*innerTableNode
}

func (n innerTableNode) depthFirstTraversal(traverseAll bool) []*innerTableNode {
	explored := make([]*innerTableNode, 0)
	toBeExplored := []*innerTableNode{&n}

	for len(toBeExplored) > 0 {
		node := toBeExplored[len(toBeExplored)-1]
		toBeExplored = toBeExplored[:len(toBeExplored)-1]
		if traverseAll || node.traversable {
			for i := len(node.children) - 1; i >= 0; i-- {
				toBeExplored = append(toBeExplored, node.children[i])
			}
		}
		explored = append(explored, node)
	}

	return explored
}

func (t HierarchicalTable) toInnerTableNode(n TableNode, parent innerTableNode, traversable map[nodePath]bool, depth int) innerTableNode {
	path := parent.path.append(n.NodeID())
	s := innerTableNode{
		path:        path,
		values:      n.Values(t.conf.NodeStyle),
		traversable: traversable[path],
	}

	if isTraversable, exists := traversable[path]; exists {
		s.traversable = isTraversable
	} else if depth > 0 {
		s.traversable = true
	}

	for _, c := range n.InheritedValues() {
		s.values[c] = parent.values[c]
	}

	children := n.NodeChildren()
	t.sortSlice(children)
	for _, child := range children {
		innerNode := t.toInnerTableNode(child, s, traversable, depth-1)
		s.children = append(s.children, &innerNode)
	}

	return s
}

func (n *innerTableNode) Map(f func(n *innerTableNode)) {
	f(n)

	for _, child := range n.children {
		child.Map(f)
	}
}

func (t *HierarchicalTable) lookup(path nodePath) *innerTableNode {
	children := make([]*innerTableNode, 0, len(t.innerNodes))
	for i := range t.innerNodes {
		children = append(children, &t.innerNodes[i])
	}

pathLoop:
	for i := 0; i < path.len; i++ {
		for _, c := range children {
			if c.path.ids[i] == path.ids[i] {
				if c.path == path {
					return c
				}
				children = c.children
				continue pathLoop
			}
		}
		return nil
	}

	return nil
}

type Order struct {
	Valid     bool
	ID        ColumnID
	Ascending bool
}

type TableConfiguration struct {
	Sep                    string
	Cursor                 StyleTransform
	Header                 StyleTransform
	HeaderSuffixAscending  string
	HeaderSuffixDescending string
	Columns                ColumnConfiguration
	DefaultDepth           int
	NodeStyle              interface{}
	Order
}

type HierarchicalTable struct {
	outerNodes []TableNode
	// List of the top-level innerNodes
	innerNodes []innerTableNode
	// Depth first traversal of all the top-level innerNodes. Needs updating if `innerNodes` or `traversable` changes
	rows []*innerTableNode
	// Index in `rows` of the first node of the current page
	pageIndex nullInt
	// Index in `rows` of the node where the cursor is located
	cursorIndex  nullInt
	height       int
	width        int
	conf         TableConfiguration
	columnWidth  map[ColumnID]int
	order        Order
	scrolled     bool
	columnOffset int
}

func NewHierarchicalTable(conf TableConfiguration, nodes []TableNode, width int, height int) (HierarchicalTable, error) {
	if width < 0 || height < 0 {
		return HierarchicalTable{}, errors.New("table width and height must be >= 0")
	}

	table := HierarchicalTable{
		height:      height,
		width:       width,
		conf:        conf,
		columnWidth: make(map[ColumnID]int),
	}

	table.Replace(nodes)
	if conf.Order.Valid {
		table.sortBy(conf.ID, conf.Order.Ascending)
	}

	return table, nil
}

func (t HierarchicalTable) depthFirstTraversal(traverseAll bool) []*innerTableNode {
	explored := make([]*innerTableNode, 0)
	for _, n := range t.innerNodes {
		explored = append(explored, n.depthFirstTraversal(traverseAll)...)
	}

	return explored
}

// Number of rows visible on screen
func (t HierarchicalTable) pageSize() int {
	return utils.MaxInt(0, t.height-1)
}

func (t *HierarchicalTable) computeTraversal() {
	// Save current paths of page and cursor
	var pageNodePath nodePath
	if t.pageIndex.Valid {
		pageNodePath = t.rows[t.pageIndex.Int].path
	}

	cursorIndex := t.cursorIndex
	var cursorNodePath nodePath
	if t.cursorIndex.Valid {
		cursorNodePath = t.rows[t.cursorIndex.Int].path
	}

	// Update node prefixes
	for i := range t.innerNodes {
		t.innerNodes[i].setPrefix("", false)
	}

	// Reset page and cursor indexes
	t.pageIndex = nullInt{}
	t.cursorIndex = nullInt{}

	t.rows = t.depthFirstTraversal(false)

	// Adjust value of pageIndex and cursorIndex
	for i, row := range t.rows {
		if row.path == pageNodePath {
			t.pageIndex = nullInt{
				Valid: true,
				Int:   i,
			}
		}

		// Track the row the cursor was on, unless it was on the first row and the user never
		// scrolled. This prevents the cursor from moving around when increasingly more rows are
		// loaded into the table but the user has not interacted with the table yet.
		if row.path == cursorNodePath && (t.scrolled || (cursorIndex.Valid && cursorIndex.Int > 0)) {
			t.cursorIndex = nullInt{
				Valid: true,
				Int:   i,
			}
		}
	}

	if len(t.rows) > 0 {
		// If no matching row was found or if all rows fit on screen, move the top page to the
		// first row
		if !t.pageIndex.Valid || len(t.rows) <= t.pageSize() {
			t.pageIndex = nullInt{
				Valid: true,
				Int:   0,
			}
		}

		// If no matching row was found, move the cursor to the first row of the page
		if !t.cursorIndex.Valid {
			t.cursorIndex = t.pageIndex
		}

		// Show as many rows as possible on screen
		if t.cursorIndex.Int-t.pageIndex.Int+1 < t.pageSize() {
			t.pageIndex.Int = utils.MaxInt(0, t.cursorIndex.Int-t.pageSize()+1)
		}

		// Adjust pageIndex so that the cursor is always on screen
		lowerBound := utils.Bounded(t.cursorIndex.Int-t.pageSize()+1, 0, len(t.rows)-1)
		t.pageIndex.Int = utils.Bounded(t.pageIndex.Int, lowerBound, t.cursorIndex.Int)
	}

	for id, value := range t.headers() {
		t.columnWidth[id] = utils.MaxInt(t.columnWidth[id], value.Length())
	}

	for _, row := range t.rows {
		for _, id := range t.conf.Columns.IDs() {
			w := row.values[id].Length()
			if t.conf.Columns[id].TreePrefix {
				w += runewidth.StringWidth(row.prefix)
			}
			t.columnWidth[id] = utils.MaxInt(t.columnWidth[id], w)
		}
	}
}

func (t *HierarchicalTable) sortSlice(nodes []TableNode) {
	if t.order.Valid {
		indexById := make(map[interface{}]int)
		for i, child := range nodes {
			indexById[child.NodeID()] = i
		}
		sort.Slice(nodes, func(i, j int) bool {
			c := nodes[i].Compare(nodes[j], t.order.ID, t.conf.NodeStyle)
			if c == 0 {
				if t.order.Ascending {
					return indexById[nodes[i].NodeID()] < indexById[nodes[j].NodeID()]
				} else {
					return indexById[nodes[i].NodeID()] > indexById[nodes[j].NodeID()]
				}
			} else {
				return (c < 0 && t.order.Ascending) || (c > 0 && !t.order.Ascending)
			}
		})
	}
}

func (t *HierarchicalTable) Replace(nodes []TableNode) {
	// Defensive copy to preserve the initial sort order
	t.outerNodes = make([]TableNode, len(nodes))
	copy(t.outerNodes, nodes)

	// Save traversable state
	traversable := make(map[nodePath]bool, 0)
	for _, node := range t.depthFirstTraversal(true) {
		traversable[node.path] = node.traversable
	}

	t.sortSlice(nodes)

	// Copy node hierarchy and compute the path of each node along the way
	t.innerNodes = make([]innerTableNode, 0, len(nodes))
	for _, n := range nodes {
		innerNode := t.toInnerTableNode(n, innerTableNode{}, traversable, t.conf.DefaultDepth)
		t.innerNodes = append(t.innerNodes, innerNode)
	}

	t.computeTraversal()
}

func (t *HierarchicalTable) setTraversable(traversable bool, recursive bool) {
	if t.cursorIndex.Valid {
		if n := t.lookup(t.rows[t.cursorIndex.Int].path); n != nil {
			if recursive {
				n.Map(func(node *innerTableNode) {
					node.traversable = traversable
				})
			} else {
				n.traversable = traversable
			}
		}
		t.computeTraversal()
	}
}

func (t *HierarchicalTable) horizontalScroll(amount int) {
	t.columnOffset = utils.Bounded(t.columnOffset+amount, 0, len(t.conf.Columns.IDs())-1)
}

func (t *HierarchicalTable) verticalScroll(amount int) {
	if !t.cursorIndex.Valid || !t.pageIndex.Valid {
		return
	}
	if amount != 0 {
		t.scrolled = true
	}

	t.cursorIndex.Int = utils.Bounded(t.cursorIndex.Int+amount, 0, len(t.rows)-1)

	switch {
	case t.cursorIndex.Int < t.pageIndex.Int:
		// verticalScroll up
		t.pageIndex.Int = t.cursorIndex.Int
	case t.cursorIndex.Int > t.pageIndex.Int+t.pageSize()-1:
		// verticalScroll down
		scrollAmount := t.cursorIndex.Int - (t.pageIndex.Int + t.pageSize() - 1)
		t.pageIndex.Int = utils.Bounded(t.pageIndex.Int+scrollAmount, 0, len(t.rows)-1)
		t.cursorIndex.Int = t.pageIndex.Int + t.pageSize() - 1
	}
}

func (t *HierarchicalTable) ScrollToNextMatch(s string, ascending bool) bool {
	if !t.cursorIndex.Valid {
		return false
	}

	step := 1
	if !ascending {
		step = -1
	}

	start := utils.Modulo(t.cursorIndex.Int+step, len(t.rows))
	next := func(i int) int {
		return utils.Modulo(i+step, len(t.rows))
	}
	for i := start; i != t.cursorIndex.Int; i = next(i) {
		for id := range t.conf.Columns {
			if t.rows[i].values[id].Contains(s) {
				t.verticalScroll(i - t.cursorIndex.Int)
				return true
			}
		}
	}

	return false
}

func (t HierarchicalTable) headers() map[ColumnID]StyledString {
	values := make(map[ColumnID]StyledString)
	for id, column := range t.conf.Columns {
		suffix := ""
		if t.order.Valid && t.order.ID == id {
			if t.order.Ascending {
				suffix = t.conf.HeaderSuffixAscending
			} else {
				suffix = t.conf.HeaderSuffixDescending
			}
		}
		values[id] = NewStyledString(column.Header + suffix)
	}
	return values
}

func (t HierarchicalTable) styledString(values map[ColumnID]StyledString, prefix string, forceAlignLeft bool) StyledString {
	paddedColumns := make([]StyledString, 0)
	for _, id := range t.conf.Columns.IDs() {
		alignment := t.conf.Columns[id].Alignment
		if forceAlignLeft {
			alignment = Left
		}
		v := values[id]
		if t.conf.Columns[id].TreePrefix {
			prefixedValue := NewStyledString(prefix)
			prefixedValue.AppendString(v)
			v = prefixedValue
		}
		w := utils.MinInt(t.columnWidth[id], t.conf.Columns[id].MaxWidth)
		v.Fit(alignment, w)
		paddedColumns = append(paddedColumns, v)
	}
	if len(paddedColumns) > 0 {
		if t.columnOffset >= 0 && t.columnOffset < len(paddedColumns) {
			paddedColumns = paddedColumns[t.columnOffset:]
		} else {
			paddedColumns = nil
		}
	}
	line := Join(paddedColumns, NewStyledString(t.conf.Sep))
	line.Fit(Left, t.width)

	return line
}

func (t *HierarchicalTable) Resize(width int, height int) {
	t.width = utils.MaxInt(0, width)
	t.height = utils.MaxInt(0, height)

	if t.pageSize() > 0 {
		if t.cursorIndex.Valid && t.pageIndex.Valid {
			upperBound := utils.Bounded(t.pageIndex.Int+t.pageSize()-1, 0, len(t.rows)-1)
			t.cursorIndex.Int = utils.Bounded(t.cursorIndex.Int, t.pageIndex.Int, upperBound)
		} else if len(t.rows) > 0 {
			t.pageIndex = nullInt{
				Valid: true,
				Int:   0,
			}
			t.cursorIndex = t.pageIndex
		}
	} else {
		t.cursorIndex = nullInt{}
		t.pageIndex = nullInt{}
	}
}

func (t HierarchicalTable) Draw(w Window) {
	if t.height > 0 {
		s := t.styledString(t.headers(), "", true)
		s.Apply(t.conf.Header)
		w.Draw(0, 0, s)
	}

	if t.pageIndex.Valid && t.cursorIndex.Valid {
		for i, row := range t.rows[t.pageIndex.Int:utils.MinInt(t.pageIndex.Int+t.pageSize(), len(t.rows))] {
			s := t.styledString(row.values, row.prefix, false)
			if t.cursorIndex.Int == i+t.pageIndex.Int {
				s.Apply(t.conf.Cursor)
			}
			w.Draw(0, i+1, s)
		}
	}
}

func (t *HierarchicalTable) ActiveNodePath() []interface{} {
	if !t.cursorIndex.Valid {
		return nil
	}

	path := t.rows[t.cursorIndex.Int].path
	slicedPath := make([]interface{}, 0)
	for _, id := range path.ids[:path.len] {
		slicedPath = append(slicedPath, id)
	}

	return slicedPath
}

func (t *HierarchicalTable) sortBy(id ColumnID, ascending bool) {
	t.order.Valid = true
	t.order.ID = id
	t.order.Ascending = ascending
	t.Replace(t.outerNodes)
}

func (t *HierarchicalTable) sortByNextColumn(reverse bool) {
	if order := t.order; order.Valid {
		ids := t.conf.Columns.IDs()
		for i, id := range ids {
			if id == order.ID {
				j := i + 1
				if reverse {
					j = i - 1
				}
				nextID := ids[utils.Modulo(j, len(ids))]
				t.sortBy(nextID, order.Ascending)
				return
			}
		}
	}
}

func (t *HierarchicalTable) reverseSortOrder() {
	if order := t.order; order.Valid {
		t.sortBy(order.ID, !order.Ascending)
	}
}

func (t *HierarchicalTable) Process(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyDown, tcell.KeyCtrlN:
		t.verticalScroll(+1)
	case tcell.KeyUp, tcell.KeyCtrlP:
		t.verticalScroll(-1)
	case tcell.KeyLeft:
		t.horizontalScroll(-1)
	case tcell.KeyRight:
		t.horizontalScroll(+1)
	case tcell.KeyCtrlD:
		t.verticalScroll(t.pageSize() / 2)
	case tcell.KeyPgDn, tcell.KeyCtrlF:
		t.verticalScroll(t.pageSize())
	case tcell.KeyCtrlU:
		t.verticalScroll(-t.pageSize() / 2)
	case tcell.KeyPgUp, tcell.KeyCtrlB:
		t.verticalScroll(-t.pageSize())
	case tcell.KeyHome:
		t.verticalScroll(-len(t.rows))
	case tcell.KeyEnd:
		t.verticalScroll(len(t.rows))
	case tcell.KeyRune:
		switch keyRune := ev.Rune(); keyRune {
		case 'j':
			t.verticalScroll(+1)
		case 'k':
			t.verticalScroll(-1)
		case 'h':
			t.horizontalScroll(-1)
		case 'l':
			t.horizontalScroll(+1)
		case 'c':
			t.setTraversable(false, false)
		case 'C', '-':
			t.setTraversable(false, true)
		case 'o':
			t.setTraversable(true, false)
		case 'O', '+':
			t.setTraversable(true, true)
		case '>':
			t.sortByNextColumn(false)
		case '<':
			t.sortByNextColumn(true)
		case '!':
			t.reverseSortOrder()
		}
	}
}