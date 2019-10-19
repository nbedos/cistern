package cache

import "context"

type Streamer func(context.Context) error

type TabularSourceRow interface {
	Tabular() map[string]string
	Key() interface{}
	URL() string
}

type HierarchicalTabularDataSource interface {
	SetTraversable(key interface{}, traversable bool, recursive bool) error
	FetchRows() error
	Select(key interface{}, nbrBefore int, nbrAfter int) ([]TabularSourceRow, int, error)
	SelectFirst(limit int) ([]TabularSourceRow, error)
	SelectLast(limit int) ([]TabularSourceRow, error)
	WriteToDirectory(ctx context.Context, key interface{}, tmpDir string) ([]string, Streamer, error)
	MaxWidths() map[string]int
}
