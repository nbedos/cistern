package widgets

import (
	"strings"
)

type Widget interface {
	Resize(width int, height int) error
	Text() ([]StyledText, error)
	Size() (width int, height int)
}

func Clear(widget Widget, class Class) []StyledText {
	xMax, yMax := widget.Size()
	texts := make([]StyledText, 0, yMax)
	for y := 0; y < yMax; y++ {
		texts = append(texts, StyledText{
			Y:       y,
			Content: strings.Repeat(" ", xMax),
			Class:   class,
		})
	}

	return texts
}
