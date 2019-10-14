package cache

import "context"

type TabularSourceRow interface {
	Tabular() map[string]string
	Key() interface{}
	URL() string
}

type HierarchicalTabularDataSource interface {
	SetTraversable(key interface{}, traversable bool)
	FetchRows() error
	Select(key interface{}, nbrBefore int, nbrAfter int) ([]TabularSourceRow, int, error)
	SelectFirst(limit int) ([]TabularSourceRow, error)
	SelectLast(limit int) ([]TabularSourceRow, error)
	WriteToDirectory(ctx context.Context, key interface{}, tmpDir string) ([]string, error)
	MaxWidths() map[string]int
}
