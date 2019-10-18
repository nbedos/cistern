package tui

import (
	"context"
	"errors"
	"github.com/gdamore/tcell"
	"github.com/gdamore/tcell/encoding"
	"github.com/nbedos/citop/providers"
	"github.com/nbedos/citop/widgets"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"time"

	"github.com/nbedos/citop/cache"
)

type OutputEvent interface {
	isOutputEvent()
}

type NoEvent struct{}

func (e NoEvent) isOutputEvent() {}

type ShowText struct {
	content []widgets.StyledText
}

func (e ShowText) isOutputEvent() {}

type ExecCmd struct {
	cmd exec.Cmd
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
	styleSheet := map[widgets.Class]tcell.Style{
		widgets.TableHeader:  defaultStyle.Bold(true),
		widgets.ActiveRow:    defaultStyle.Reverse(true),
		widgets.DefaultClass: defaultStyle,
	}

	requesters := []cache.Requester{
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

	// FIXME Remove this last reference to Inserter outside of Cache
	inserters := []cache.Inserter{
		cache.Account{
			ID:       "travis",
			URL:      "http://travis.example.com/v3",
			UserID:   "F54E34EA",
			Username: "username",
		},
		cache.Account{
			ID:       "gitlab",
			URL:      "http://gitlab.example.com/v3",
			UserID:   "F54E34EA",
			Username: "username",
		},
		cache.Account{
			ID:       "circleci",
			URL:      "http://circleci.example.com/v3",
			UserID:   "F54E34EA",
			Username: "username",
		},
	}

	cachePath := path.Join(tmpDir, "cache.db")
	cacheDb, err := cache.NewCache(cachePath, true, requesters)
	if err != nil {
		return err
	}
	defer func() {
		if errClose := cacheDb.Close(); err == nil {
			err = errClose
		}
	}()

	if err := cacheDb.Save(context.Background(), inserters); err != nil {
		return err
	}

	eventc := make(chan tcell.Event)
	outc := make(chan OutputEvent)
	errc := make(chan error)

	updates := make(chan time.Time)
	source := cacheDb.NewRepositoryBuilds("https://github.com/nbedos/citop", updates)

	ctx := context.Background()

	go func() {
		if err := source.FetchData(ctx, updates); err != nil {
			errc <- err
		}
	}()

	go func() {
		var controller TableController

		controller, err = NewTableController(&source, tmpDir)
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
				outEv, err := controller.Process(event)
				if err != nil {
					errc <- err
				}

				if _, isNoEvent := outEv.(NoEvent); !isNoEvent {
					outc <- outEv
				}

				if _, isExit := outEv.(ExitEvent); isExit {
					close(outc)
					break
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
			case ShowText:
				screen.Clear()
				if err = widgets.Draw(e.content, screen, styleSheet); err != nil {
					return
				}
				screen.Show()

			case ExecCmd:
				screen.Fini()

				e.cmd.Stdin = os.Stdin
				// e.cmd.Stderr = os.Stderr FIXME?
				e.cmd.Stdout = os.Stdout
				// FIXME Show return value in status bar
				e.cmd.Run()

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
