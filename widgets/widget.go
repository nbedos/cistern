package widgets

import "github.com/nbedos/citop/text"

type Widget interface {
	Resize(width int, height int) error
	Text() ([]text.LocalizedStyledString, error)
	Size() (width int, height int)
}
