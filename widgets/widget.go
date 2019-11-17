package widgets

import "github.com/nbedos/citop/text"

type Widget interface {
	Resize(width int, height int)
	Text() []text.LocalizedStyledString
	Size() (width int, height int)
}
