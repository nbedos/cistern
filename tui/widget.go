package tui

import (
	"github.com/gdamore/tcell"
	"github.com/mattn/go-runewidth"
	"github.com/nbedos/cistern/utils"
)

type Widget interface {
	Resize(width int, height int)
	Draw(w Window)
	Process(k *tcell.EventKey)
}

type Window interface {
	Draw(x, y int, s StyledString)
	Window(x, y, width, height int) Window
}

type subScreen struct {
	style  tcell.Style
	screen tcell.Screen
	x      int
	y      int
	width  int
	height int
}

func (s *subScreen) Draw(i, j int, line StyledString) {
	X, Y := s.x+i, s.y+j
	for _, component := range line.components {
		style := s.style
		if component.Transform != nil {
			style = component.Transform(style)
		}

		for _, r := range component.Content {
			if X >= s.x && X < s.x+s.width && Y >= s.y && Y < s.y+s.height {
				s.screen.SetContent(X, Y, r, nil, style)
				X += runewidth.RuneWidth(r)
			}
		}
	}
}

func (s *subScreen) Window(x, y, width, height int) Window {
	sub := subScreen{
		style:  s.style,
		screen: s.screen,
		x:      utils.Bounded(s.x+x, s.x, s.x+s.width-1),
		y:      utils.Bounded(s.y+y, s.y, s.y+s.height-1),
	}

	X := utils.Bounded(s.x+x+width-1, s.x, s.x+s.width-1)
	Y := utils.Bounded(s.y+y+height-1, s.y, s.y+s.height-1)

	sub.width = utils.MaxInt(0, X-sub.x+1)
	sub.height = utils.MaxInt(0, Y-sub.y+1)

	return &sub
}
