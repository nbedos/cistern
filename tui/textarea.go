package tui

import (
	"errors"

	"github.com/nbedos/cistern/utils"
)

type TextArea struct {
	width   int
	height  int
	Content []StyledString
	yOffset int
}

func NewTextArea(width, height int) (TextArea, error) {
	if width < 0 || height < 0 {
		return TextArea{}, errors.New("width and height must be >= 0")
	}

	return TextArea{
		width:  width,
		height: height,
	}, nil
}

func (t *TextArea) VerticalScroll(amount int) {
	switch offset := t.yOffset + amount; {
	case offset < 0:
		t.yOffset = 0
	case offset > len(t.Content) - t.height:
		t.yOffset = utils.MaxInt(0, len(t.Content) - t.height)
	default:
		t.yOffset = offset
	}
}

func (t *TextArea) Write(lines ...StyledString) {
	t.Content = lines
}

func (t *TextArea) Resize(width int, height int) {
	t.width = utils.MaxInt(0, width)
	t.height = utils.MaxInt(0, height)
}

func (t TextArea) StyledStrings() []StyledString {
	ss := make([]StyledString, 0)
	for i, line := range t.Content {
		if i >= t.yOffset {
			ss = append(ss, line)
			if len(ss) >= t.height {
				break
			}
		}
	}

	for len(ss) < t.height {
		ss = append(ss, StyledString{})
	}

	return ss
}
