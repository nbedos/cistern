package tui

import (
	"context"
	"errors"
	"github.com/gdamore/tcell"
	"github.com/gdamore/tcell/encoding"
	"github.com/nbedos/citop/providers"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/nbedos/citop/cache"
)

type OutputEvent interface {
	isOutputEvent()
}

type ShowText struct {
	content []text.LocalizedStyledString
}

func (e ShowText) isOutputEvent() {}

type ExecCmd struct {
	cmd    exec.Cmd
	stream cache.Streamer
}

func (e ExecCmd) isOutputEvent() {}

type ExitEvent struct{}

func (e ExitEvent) isOutputEvent() {}

func RunWidgetApp() (err error) {
	// FIXME Discard log until the status bar is implemented in order to hide the "Unsolicited response received on
	//  idle HTTP channel" from GitLab's HTTP client
	log.SetOutput(ioutil.Discard)

	travisToken := os.Getenv("TRAVIS_API_TOKEN")
	if travisToken == "" {
		err = errors.New("environment variable TRAVIS_API_TOKEN is not set")
		return
	}

	gitlabToken := os.Getenv("GITLAB_API_TOKEN")
	if gitlabToken == "" {
		err = errors.New("environment variable GITLAB_API_TOKEN is not set")
		return
	}

	circleciToken := os.Getenv("CIRCLECI_API_TOKEN")
	if circleciToken == "" {
		err = errors.New("environment variable CIRCLECI_API_TOKEN is not set")
		return
	}

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
		text.StatusRunning: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorYellow)
		},
		text.StatusFailed: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorRed)
		},
		text.StatusPassed: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorGreen)
		},
		text.GitRef: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorGray)
		},
		text.Provider: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorGray)
		},
	}

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

	cacheDB := cache.NewCache(CIProviders)

	eventc := make(chan tcell.Event)
	outc := make(chan OutputEvent)
	errc := make(chan error)

	updates := make(chan time.Time)
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	originURL, err := utils.GitOriginURL(cwd)
	if err != nil {
		return err
	}
	originURL = "https://github.com/circleci/circleci-docs"
	source := cacheDB.NewRepositoryBuilds(originURL)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		if err := cacheDB.UpdateFromProviders(ctx, originURL, 1*24*time.Hour, updates); err != nil {
			errc <- err
		}
	}()

	go func() {
		defaultStatus := "j:Down  k:Up  oO:Open  cC:Close  /:Search  b:Browser  ?:Help  q:Quit"
		controller, err := NewTableController(&source, tmpDir, defaultStatus)
		if err != nil {
			errc <- err
			return
		}

		for {
			select {
			case <-updates:
				content, err := controller.Refresh()
				if err != nil {
					errc <- err
				}
				outc <- ShowText{content}

			case event := <-eventc:
				if event == nil {
					return
				}
				if err = controller.Process(ctx, event, outc); err != nil {
					errc <- err
				}
			}
		}
	}()

	encoding.Register()
	screen, err := tcell.NewScreen()
	if err != nil {
		return
	}
	defer func() {
		screen.Fini()
	}()

	if err = screen.Init(); err != nil {
		return
	}

	screen.SetStyle(defaultStyle)
	//screen.EnableMouse()
	screen.Clear()

	poll := func() {
		for {
			event := screen.PollEvent()
			if event == nil {
				break
			}
			eventc <- event
		}
	}

	go poll()

	for {
		select {
		case err := <-errc:
			return err
		case outEvent := <-outc:
			if outEvent == nil {
				return nil
			}
			switch e := outEvent.(type) {
			case ExitEvent:
				cancel()
				return

			case ShowText:
				screen.Clear()
				if err = text.Draw(e.content, screen, defaultStyle, styleSheet); err != nil {
					return
				}
				screen.Show()

			case ExecCmd:
				screen.Fini()

				e.cmd.Stdin = os.Stdin
				// e.cmd.Stderr = os.Stderr FIXME?
				e.cmd.Stdout = os.Stdout
				// FIXME Show return value in status bar

				subCtx, cancel := context.WithCancel(ctx)
				if e.stream != nil {
					go func() {
						e.stream(subCtx)
					}()
				}
				e.cmd.Run()
				cancel()

				screen, err = tcell.NewScreen()
				if err != nil {
					return
				}

				if err = screen.Init(); err != nil {
					return
				}

				screen.SetStyle(defaultStyle)
				//screen.EnableMouse()
				screen.Clear()

				go poll()
			}
		}
	}
}
