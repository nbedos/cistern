package tui

import (
	"context"
	"io"
	"os"
	"os/exec"

	"github.com/gdamore/tcell"
)



type TUI struct {
	newScreen    func() (tcell.Screen, error)
	screen       tcell.Screen
	defaultStyle tcell.Style
	styleSheet   StyleSheet
	Eventc       chan tcell.Event
}

func NewTUI(newScreen func() (tcell.Screen, error), defaultStyle tcell.Style, styleSheet StyleSheet) (TUI, error) {
	ui := TUI{
		newScreen:    newScreen,
		defaultStyle: defaultStyle,
		styleSheet:   styleSheet,
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
		s.Draw(t.screen, y, t.defaultStyle, t.styleSheet)
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

