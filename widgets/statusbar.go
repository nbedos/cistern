package widgets

import (
	"errors"
	"fmt"
	"github.com/nbedos/citop/utils"
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

func (s *StatusBar) Write(p []byte) (int, error) {
	s.outputBuffer = append(s.outputBuffer, string(p))
	if len(s.outputBuffer) > s.height {
		s.outputBuffer = s.outputBuffer[:s.height]
	}

	return len(p), nil
}

func (s StatusBar) Size() (int, int) {
	return s.width, s.height
}

func (s *StatusBar) Resize(width int, height int) error {
	if width < 0 || height < 0 {
		return errors.New("width and height must be >= 0")
	}

	s.width, s.height = width, height
	return nil
}

func (s StatusBar) Text() ([]StyledText, error) {
	if s.ShowInput {
		return []StyledText{{
			X:       0,
			Y:       utils.MaxInt(s.height-1, 0),
			Content: fmt.Sprintf("%s%s", s.inputPrefix, s.InputBuffer),
			Class:   DefaultClass,
		}}, nil
	}

	texts := make([]StyledText, 0)
	startRow := utils.MaxInt(0, len(s.outputBuffer)-s.height)
	for i := startRow; i < len(s.outputBuffer); i++ {
		texts = append(texts, StyledText{
			X:       0,
			Y:       i - startRow,
			Content: s.outputBuffer[i],
			Class:   DefaultClass,
		})
	}

	return texts, nil
}
