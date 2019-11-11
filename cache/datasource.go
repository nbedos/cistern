package cache

import (
	"context"

	"github.com/nbedos/citop/text"
)

type Streamer func(context.Context) error

type TabularSourceRow interface {
	Tabular() map[string]text.StyledString
	Key() interface{}
	URL() string
}

type HierarchicalTabularDataSource interface {
	Headers() []string
	Alignment() map[string]text.Alignment
	FetchRows()
	Select(key interface{}, nbrBefore int, nbrAfter int) ([]TabularSourceRow, int, error)
	SelectFirst(limit int) ([]TabularSourceRow, error)
	SelectLast(limit int) ([]TabularSourceRow, error)
	SetTraversable(key interface{}, traversable bool, recursive bool) error
	NextMatch(top, bottom, active interface{}, search string, ascending bool) ([]TabularSourceRow, int, error)
	WriteToDisk(ctx context.Context, key interface{}, tmpDir string) (string, Streamer, error)
}
