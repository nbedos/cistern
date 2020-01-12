package tui

import (
	"errors"

	"github.com/gdamore/tcell"
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

func (t *TextArea) verticalScroll(amount int) {
	switch offset := t.yOffset + amount; {
	case offset < 0:
		t.yOffset = 0
	case offset > len(t.Content)-t.height:
		t.yOffset = utils.MaxInt(0, len(t.Content)-t.height)
	default:
		t.yOffset = offset
	}
}

func (t *TextArea) WriteContent(lines ...StyledString) {
	t.Content = lines
}

func (t *TextArea) Resize(width int, height int) {
	t.width = utils.MaxInt(0, width)
	t.height = utils.MaxInt(0, height)
}

func (t TextArea) Draw(w Window) {
	for i, line := range t.Content {
		if i >= t.yOffset {
			w.Draw(0, i-t.yOffset, line)
		}
	}
}

func (t *TextArea) Process(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyDown, tcell.KeyCtrlN:
		t.verticalScroll(+1)
	case tcell.KeyUp, tcell.KeyCtrlP:
		t.verticalScroll(-1)
	case tcell.KeyCtrlD:
		t.verticalScroll(t.height / 2)
	case tcell.KeyPgDn, tcell.KeyCtrlF:
		t.verticalScroll(t.height)
	case tcell.KeyCtrlU:
		t.verticalScroll(-t.height / 2)
	case tcell.KeyPgUp, tcell.KeyCtrlB:
		t.verticalScroll(-t.height)
	case tcell.KeyRune:
		switch ev.Rune() {
		case ' ':
			t.verticalScroll(t.height)
		case 'k':
			t.verticalScroll(-1)
		case 'j':
			t.verticalScroll(+1)
		}
	}
}