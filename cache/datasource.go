package cache

import (
	"context"

	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
)

type Streamer func(context.Context) error

type HierarchicalTabularSourceRow interface {
	Tabular() map[string]text.StyledString
	Key() interface{}
	URL() string
	Prefix(indent string, last bool)
	utils.TreeNode
}

type HierarchicalTabularDataSource interface {
	Rows() []HierarchicalTabularSourceRow
	Headers() []string
	Alignment() map[string]text.Alignment
	WriteToDisk(ctx context.Context, key interface{}, tmpDir string) (string, Streamer, error)
}
