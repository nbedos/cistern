package tui

import (
	"context"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/gdamore/tcell"
	"github.com/gdamore/tcell/encoding"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/providers"
	"github.com/nbedos/citop/text"
)

type ExecCmd struct {
	name   string
	args   []string
	dir    string
	stream cache.Streamer
}

func RunWidgetApp(repositoryURL string, travisToken string, gitlabToken string, circleciToken string) (err error) {
	// FIXME Discard log until the status bar is implemented in order to hide the "Unsolicited response received on
	//  idle HTTP channel" from GitLab's HTTP client
	log.SetOutput(ioutil.Discard)
	encoding.Register()

	tmpDir, err := ioutil.TempDir("", "citop")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

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
	}
	defaultStatus := "j:Down  k:Up  oO:Open  cC:Close  /:Search  b:Browser  ?:Help  q:Quit"

	CIProviders := []cache.Provider{
		providers.NewTravisClient(
			providers.TravisOrgURL,
			providers.TravisPusherHost,
			travisToken,
			"travis",
			50*time.Millisecond),

		providers.NewGitLabClient(
			"gitlab",
			gitlabToken,
			100*time.Millisecond),

		providers.NewCircleCIClient(
			providers.CircleCIURL,
			"circleci",
			circleciToken,
			100*time.Millisecond),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cacheDB := cache.NewCache(CIProviders)
	source := cacheDB.BuildsByCommit(repositoryURL)

	ui, err := NewTUI(defaultStyle, styleSheet)
	if err != nil {
		return err
	}
	defer func() {
		ui.Finish()
	}()

	controller, err := NewTableController(&ui, &source, tmpDir, defaultStatus)
	if err != nil {
		return err
	}

	errc := make(chan error)
	updates := make(chan time.Time)
	go func() {
		errc <- cacheDB.UpdateFromProviders(ctx, repositoryURL, 7*24*time.Hour, updates)
	}()

	go func() {
		errc <- controller.Run(ctx, updates)
	}()

	for i := 0; i < 2; i++ {
		e := <-errc
		if i == 0 {
			cancel()
			err = e
		}
	}

	return err
}

type TUI struct {
	screen       tcell.Screen
	defaultStyle tcell.Style
	styleSheet   text.StyleSheet
	eventc       chan tcell.Event
}

func NewTUI(defaultStyle tcell.Style, styleSheet text.StyleSheet) (TUI, error) {
	ui := TUI{
		defaultStyle: defaultStyle,
		styleSheet:   styleSheet,
		eventc:       make(chan tcell.Event),
	}
	err := ui.init()

	return ui, err
}

func (t *TUI) init() error {
	var err error
	t.screen, err = tcell.NewScreen()
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

type errStream struct{ error }

func (e errStream) Error() string { return e.error.Error() }

type errCmd struct{ error }

func (e errCmd) Error() string { return e.error.Error() }

func (t *TUI) Exec(ctx context.Context, e ExecCmd) error {
	t.Finish()

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, e.name, e.args...)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	errc := make(chan error)
	wg := sync.WaitGroup{}

	if e.stream != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errc <- errStream{error: e.stream(cmdCtx)}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		errc <- errCmd{error: cmd.Run()}
	}()

	go func() {
		wg.Wait()
		close(errc)
	}()

	var err error
	errSet := false
	for e := range errc {
		switch e := e.(type) {
		case errStream:
			if e.error != nil {
				cancel()
				if !errSet {
					errSet = true
					err = e.error
				}
			}
		case errCmd:
			cancel()
			if !errSet {
				errSet = true
				err = e.error
			}
		}
	}
	if err != nil {
		return err
	}

	return t.init()
}
