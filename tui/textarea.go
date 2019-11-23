package tui

import (
	"errors"

	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
)

type TextArea struct {
	width   int
	height  int
	content []text.StyledString
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

func (s *TextArea) Write(lines ...text.StyledString) {
	s.content = lines
}

func (s TextArea) Size() (int, int) {
	return s.width, s.height
}

func (s *TextArea) Resize(width int, height int) {
	s.width = utils.MaxInt(0, width)
	s.height = utils.MaxInt(0, height)
}

func (s TextArea) Text() []text.LocalizedStyledString {
	texts := make([]text.LocalizedStyledString, 0)
	for i, line := range s.content {
		texts = append(texts, text.LocalizedStyledString{
			X: 0,
			Y: i,
			S: line,
		})
		if len(texts) >= s.height {
			break
		}
	}

	return texts
}
