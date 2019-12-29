package tui

type Widget interface {
	Resize(width int, height int)
	Text() []LocalizedStyledString
	Size() (width int, height int)
}
