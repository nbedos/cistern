package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gdamore/tcell"
)

type TUI struct {
	newScreen    func() (tcell.Screen, error)
	screen       tcell.Screen
	defaultStyle tcell.Style
	Eventc       chan tcell.Event
}

func NewTUI(newScreen func() (tcell.Screen, error), defaultStyle tcell.Style) (TUI, error) {
	ui := TUI{
		newScreen:    newScreen,
		defaultStyle: defaultStyle,
		Eventc:       make(chan tcell.Event),
	}
	err := ui.init()

	return ui, err
}

func (t *TUI) init() error {
	var err error
	t.screen, err = t.newScreen()
	if err != nil {
		return err
	}

	if err = t.screen.Init(); err != nil {
		return err
	}
	t.screen.SetStyle(t.defaultStyle)
	//screen.EnableMouse()
	t.screen.Clear()

	go t.poll()

	return nil
}

func (t TUI) Finish() {
	t.screen.Fini()
}

func (t TUI) Events() <-chan tcell.Event {
	return t.Eventc
}

func (t TUI) Size() (int, int) {
	return t.screen.Size()
}

func (t TUI) poll() {
	// Exits when t.Finish() is called
	for {
		event := t.screen.PollEvent()
		if event == nil {
			break
		}
		t.Eventc <- event
	}
}

func (t TUI) Draw(ss ...StyledString) {
	t.screen.Clear()
	for y, s := range ss {
		s.Draw(t.screen, y, t.defaultStyle)
	}
	t.screen.Show()
}

func (t *TUI) Exec(ctx context.Context, name string, args []string, stdin io.Reader) error {
	var err error
	t.Finish()
	defer func() {
		if e := t.init(); err == nil {
			err = e
		}
	}()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	err = cmd.Run()
	return err
}

type StyleTransform func(s tcell.Style) tcell.Style

func (t StyleTransform) On(other StyleTransform) StyleTransform {
	if other == nil {
		return t
	}
	return func(s tcell.Style) tcell.Style {
		return t(other(s))
	}
}

type StyleTransformDefinition struct {
	Foreground *string `toml:"foreground"`
	Background *string `toml:"background"`
	Bold       *bool   `toml:"bold"`
	Underlined *bool   `toml:"underlined"`
	Reversed   *bool   `toml:"reversed"`
	Dimmed     *bool   `toml:"dimmed"`
	Blink      *bool   `toml:"blink"`
}

func (s StyleTransformDefinition) Parse() (StyleTransform, error) {
	parseColor := func(s string) (tcell.Color, error) {
		s = strings.Trim(strings.ToLower(s), " ")

		if strings.HasPrefix(s, "color") {
			s = strings.TrimPrefix(s, "color")
			if n, err := strconv.Atoi(s); err == nil {
				return tcell.Color(n), nil
			}
		} else if c, ok := tcell.ColorNames[s]; ok {
			return c, nil
		} else if len(s) == 7 && s[0] == '#' {
			if n, err := strconv.ParseInt(s[1:], 16, 32); err == nil {
				return tcell.NewHexColor(int32(n)), nil
			}
		}

		return tcell.Color(0), fmt.Errorf("failed to parse color from string %q", s)
	}

	var err error
	var fg, bg tcell.Color

	if s.Foreground != nil {
		if fg, err = parseColor(*s.Foreground); err != nil {
			return nil, err
		}
	}

	if s.Background != nil {
		if bg, err = parseColor(*s.Foreground); err != nil {
			return nil, err
		}
	}

	return func(style tcell.Style) tcell.Style {
		if s.Foreground != nil {
			style = style.Foreground(fg)
		}
		if s.Background != nil {
			style = style.Background(bg)
		}
		if s.Bold != nil {
			style = style.Bold(*s.Bold)
		}
		if s.Underlined != nil {
			style = style.Underline(*s.Underlined)
		}
		if s.Dimmed != nil {
			style = style.Dim(*s.Dimmed)
		}
		if s.Blink != nil {
			style = style.Blink(*s.Blink)
		}
		if s.Reversed != nil {
			style = style.Reverse(*s.Reversed)
		}
		return style
	}, nil
}


