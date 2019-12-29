package tui

import (
	"errors"

	"github.com/nbedos/cistern/utils"
)

type TextArea struct {
	width   int
	height  int
	Content []StyledString
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

func (s *TextArea) Write(lines ...StyledString) {
	s.Content = lines
}

func (s TextArea) Size() (int, int) {
	return s.width, s.height
}

func (s *TextArea) Resize(width int, height int) {
	s.width = utils.MaxInt(0, width)
	s.height = utils.MaxInt(0, height)
}

func (s TextArea) Text() []LocalizedStyledString {
	texts := make([]LocalizedStyledString, 0)
	for i, line := range s.Content {
		texts = append(texts, LocalizedStyledString{
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
