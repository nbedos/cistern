package tui

type Widget interface {
	Resize(width int, height int)
	StyledStrings() []StyledString
}
