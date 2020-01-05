package tui

import (
	"errors"
	"fmt"

	"github.com/nbedos/cistern/utils"
)

type StatusBar struct {
	width        int
	height       int
	outputBuffer []string
	InputBuffer  string
	ShowInput    bool
	InputPrefix  string
}

func NewStatusBar(width, height int) (StatusBar, error) {
	if width < 0 || height < 0 {
		return StatusBar{}, errors.New("width and height must be >= 0")
	}

	return StatusBar{
		width:        width,
		height:       height,
		outputBuffer: make([]string, 0),
		InputBuffer:  "",
		InputPrefix:  "/",
	}, nil
}

func (s *StatusBar) Write(status string) {
	s.outputBuffer = append(s.outputBuffer, status)
	if offset := len(s.outputBuffer) - s.height; offset > 0 {
		s.outputBuffer = s.outputBuffer[offset : offset+s.height]
	}
}

func (s *StatusBar) Resize(width int, height int) {
	s.width = utils.MaxInt(0, width)
	s.height = utils.MaxInt(0, height)
}

func (s StatusBar) StyledStrings() []StyledString {
	lines := make([]StyledString, 0)

	for i := 0; i < s.height; i++ {
		var line StyledString
		if s.ShowInput {
			if i == s.height-1 {
				line = NewStyledString(fmt.Sprintf("%s%s", s.InputPrefix, s.InputBuffer))
			}
		} else {
			if bufferIndex := i - (s.height - len(s.outputBuffer)); bufferIndex >= 0 {
				line = NewStyledString(s.outputBuffer[bufferIndex])
			}
		}
		lines = append(lines, line)
	}

	return lines
}
