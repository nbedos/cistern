package cache

import (
	"context"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
)

type HierarchicalTabularSourceRow interface {
	Tabular(*time.Location) map[string]text.StyledString
	Key() interface{}
	URL() string
	SetPrefix(s string)
	utils.TreeNode
}

type HierarchicalTabularDataSource interface {
	Rows() []HierarchicalTabularSourceRow
	Headers() []string
	Alignment() map[string]text.Alignment
	WriteToDisk(ctx context.Context, key interface{}, tmpDir string) (string, error)
}

func Prefix(row HierarchicalTabularSourceRow, indent string, last bool) {
	var prefix string
	// Special behavior for the root node which is prefixed by "+" if its children are hidden
	if indent == "" {
		switch {
		case len(row.Children()) == 0:
			prefix = " "
		case row.Traversable():
			prefix = "-"
		default:
			prefix = "+"
		}
	} else {
		if last {
			prefix = "└─"
		} else {
			prefix = "├─"
		}

		if len(row.Children()) == 0 || row.Traversable() {
			prefix += "─ "
		} else {
			prefix += "+ "
		}
	}

	row.SetPrefix(indent + prefix)

	if row.Traversable() {
		for i, child := range row.Children() {
			child := child.(HierarchicalTabularSourceRow)
			var childIndent string
			if last {
				childIndent = " "
			} else {
				childIndent = "│"
			}

			paddingLength := runewidth.StringWidth(prefix) - runewidth.StringWidth(childIndent)
			childIndent += strings.Repeat(" ", paddingLength)
			Prefix(child, indent+childIndent, i == len(row.Children())-1)
		}
	}
}
