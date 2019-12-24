package tui

import "github.com/nbedos/cistern/text"

type Widget interface {
	Resize(width int, height int)
	Text() []text.LocalizedStyledString
	Size() (width int, height int)
}
