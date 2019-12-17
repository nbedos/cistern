package tui

import (
	"context"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/gdamore/tcell"
	"github.com/gdamore/tcell/encoding"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/text"
)

var ErrNoProvider = errors.New("list of providers must not be empty")

func RunApplication(ctx context.Context, newScreen func() (tcell.Screen, error), repo string, ref string, CIProviders []cache.CIProvider, SourceProviders []cache.SourceProvider, loc *time.Location, help string) (err error) {
	if len(CIProviders) == 0 || len(SourceProviders) == 0 {
		return ErrNoProvider
	}
	// FIXME Discard log until the status bar is implemented in order to hide the "Unsolicited response received on
	//  idle HTTP channel" from GitLab's HTTP client
	log.SetOutput(ioutil.Discard)
	encoding.Register()

	defaultStyle := tcell.StyleDefault
	styleSheet := text.StyleSheet{
		text.TableHeader: func(s tcell.Style) tcell.Style {
			return s.Bold(true).Reverse(true)
		},
		text.ActiveRow: func(s tcell.Style) tcell.Style {
			return s.Background(tcell.ColorSilver).Foreground(tcell.ColorBlack).Bold(false).Underline(false).Blink(false)
		},
		text.Provider: func(s tcell.Style) tcell.Style {
			return s.Bold(true)
		},
		text.StatusFailed: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorMaroon).Bold(false)
		},
		text.StatusPassed: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorGreen).Bold(false)
		},
		text.StatusRunning: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorOlive).Bold(false)
		},
		text.StatusSkipped: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorGray).Bold(false)
		},
		text.GitSha: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorOlive)
		},
		text.GitBranch: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorTeal).Bold(false)
		},
		text.GitTag: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorYellow).Bold(false)
		},
		text.GitHead: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorAqua)
		},
	}
	defaultStatus := "j:Down  k:Up  oO:Open  cC:Close  /:Search  v:Logs  b:Browser  ?:Help  q:Quit"

	ui, err := NewTUI(newScreen, defaultStyle, styleSheet)
	if err != nil {
		return err
	}
	defer func() {
		// If another goroutine panicked this wouldn't run so we'd be left with a garbled screen.
		// The alternative would be to defer a call to recover for every goroutine that we launch
		// in order to have them return an error in case of a panic.
		ui.Finish()
	}()

	cacheDB := cache.NewCache(CIProviders, SourceProviders)

	controller, err := NewController(&ui, ref, cacheDB, loc, defaultStatus, help)
	if err != nil {
		return err
	}

	return controller.Run(ctx, repo)
}

type TUI struct {
	newScreen    func() (tcell.Screen, error)
	screen       tcell.Screen
	defaultStyle tcell.Style
	styleSheet   text.StyleSheet
	eventc       chan tcell.Event
}

func NewTUI(newScreen func() (tcell.Screen, error), defaultStyle tcell.Style, styleSheet text.StyleSheet) (TUI, error) {
	ui := TUI{
		newScreen:    newScreen,
		defaultStyle: defaultStyle,
		styleSheet:   styleSheet,
		eventc:       make(chan tcell.Event),
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
	return t.eventc
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
		t.eventc <- event
	}
}

func (t TUI) Draw(texts ...text.LocalizedStyledString) {
	t.screen.Clear()
	text.Draw(texts, t.screen, t.defaultStyle, t.styleSheet)
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
