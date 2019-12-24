package tui

import (
	"errors"
	"fmt"

	"github.com/nbedos/cistern/text"
	"github.com/nbedos/cistern/utils"
)

type StatusBar struct {
	width        int
	height       int
	outputBuffer []string
	InputBuffer  string
	ShowInput    bool
	inputPrefix  string
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
		inputPrefix:  "/",
	}, nil
}

func (s *StatusBar) Write(status string) {
	s.outputBuffer = append(s.outputBuffer, status)
	if offset := len(s.outputBuffer) - s.height; offset > 0 {
		s.outputBuffer = s.outputBuffer[offset : offset+s.height]
	}
}

func (s StatusBar) Size() (int, int) {
	return s.width, s.height
}

func (s *StatusBar) Resize(width int, height int) {
	s.width = utils.MaxInt(0, width)
	s.height = utils.MaxInt(0, height)
}

func (s StatusBar) Text() []text.LocalizedStyledString {
	if s.ShowInput {
		return []text.LocalizedStyledString{{
			X: 0,
			Y: utils.MaxInt(s.height-1, 0),
			S: text.NewStyledString(fmt.Sprintf("%s%s", s.inputPrefix, s.InputBuffer)),
		}}
	}

	texts := make([]text.LocalizedStyledString, 0)
	startRow := utils.MaxInt(0, len(s.outputBuffer)-s.height)
	for i := startRow; i < len(s.outputBuffer); i++ {
		texts = append(texts, text.LocalizedStyledString{
			X: 0,
			Y: i - startRow,
			S: text.NewStyledString(s.outputBuffer[i]),
		})
	}
	return texts
}
