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

func (s *TextArea) Resize(width int, height int) {
	s.width = utils.MaxInt(0, width)
	s.height = utils.MaxInt(0, height)
}

func (s TextArea) StyledStrings() []StyledString {
	ss := make([]StyledString, 0)
	for _, line := range s.Content {
		ss = append(ss, line)
		if len(ss) >= s.height {
			break
		}
	}

	for len(ss) < s.height {
		ss = append(ss, StyledString{})
	}

	return ss
}
