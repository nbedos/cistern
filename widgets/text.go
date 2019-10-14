package widgets

import (
	"errors"
	"github.com/gdamore/tcell"
	"github.com/mattn/go-runewidth"
	"strings"
)

type Class int

const (
	// FIXME Maybe use string and names compatible with CSS?
	DefaultClass Class = iota
	TableHeader
	ActiveRow
)

type StyledText struct {
	X       int
	Y       int
	Content string
	Class   Class
}

func Draw(texts []StyledText, screen tcell.Screen, styleSheet map[Class]tcell.Style) error {
	for _, t := range texts {
		if err := t.Draw(screen, styleSheet); err != nil {
			return err
		}
	}

	return nil
}

func (t StyledText) Draw(screen tcell.Screen, styleSheet map[Class]tcell.Style) error {
	style, ok := styleSheet[t.Class]
	if !ok {
		return errors.New("missing style for class")
	}

	x := t.X
	for _, r := range t.Content {
		screen.SetContent(x, t.Y, r, nil, style)
		x += runewidth.RuneWidth(r)
	}

	return nil
}

type Alignment int

const (
	Left Alignment = iota
	Right
)

func align(s string, width int, alignment Alignment) string {
	if paddingWidth := width - runewidth.StringWidth(s); paddingWidth > 0 {
		switch padding := strings.Repeat(" ", paddingWidth); alignment {
		case Left:
			return s + padding
		case Right:
			return padding + s
		}
	}

	return s
}
