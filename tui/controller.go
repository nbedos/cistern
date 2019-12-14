package tui

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
)

type inputDestination int

const (
	inputNone inputDestination = iota
	inputSearch
	inputRef
)

type Controller struct {
	tui              *TUI
	cache            cache.Cache
	ref              string
	width            int
	height           int
	header           *TextArea
	table            *Table
	tableSearch      string
	status           *StatusBar
	inputDestination inputDestination
	defaultStatus    string
	help             string
	tempDir          string
}

var ErrExit = errors.New("exit")

type SourceFromRef = func(ref string) cache.HierarchicalTabularDataSource

func NewController(tui *TUI, ref string, c cache.Cache, loc *time.Location, tempDir string, defaultStatus string, help string) (Controller, error) {
	// Arbitrary values, the correct size will be set when the first RESIZE event is received
	width, height := 10, 10
	header, err := NewTextArea(width, height)
	if err != nil {
		return Controller{}, err
	}

	table, err := NewTable(c.BuildsOfRef(ref), width, height, loc)
	if err != nil {
		return Controller{}, err
	}

	status, err := NewStatusBar(width, height)
	if err != nil {
		return Controller{}, err
	}
	status.Write(defaultStatus)

	return Controller{
		tui:           tui,
		ref:           ref,
		cache:         c,
		header:        &header,
		table:         &table,
		status:        &status,
		tempDir:       tempDir,
		defaultStatus: defaultStatus,
		help:          help,
	}, nil
}

func (c *Controller) setRef(ref string) error {
	// TODO Preserve traversable state across calls to setRef()
	table, err := NewTable(c.cache.BuildsOfRef(ref), c.table.width, c.table.height, c.table.location)
	if err != nil {
		return err
	}
	c.table, c.ref = &table, ref

	return nil
}

func (c *Controller) MonitorPipelines(ctx context.Context, repositoryURL string, ref string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errc := make(chan error)
	updates := make(chan time.Time)
	go func() {
		errc <- c.cache.MonitorPipelines(ctx, repositoryURL, ref, updates)
	}()

	refSet := false
	for {
		select {
		case <-updates:
			// Update the controller once we receive an update, meaning the reference exists at
			// least locally or remotely
			if !refSet {
				if err := c.setRef(ref); err != nil {
					return err
				}
				refSet = true
			}
			c.refresh()
			c.draw()
		case err := <-errc:
			return err
		}
	}
}

func (c *Controller) Run(ctx context.Context, repositoryURL string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error)
	refc := make(chan string)

	c.refresh()
	c.draw()

	// Trigger pipeline monitoring for git reference c.ref
	go func() {
		select {
		case refc <- c.ref:
		case <-ctx.Done():
		}
	}()

	var refCtx context.Context
	var refCancel = func() {}
	var err error
	for err == nil {
		select {
		case ref := <-refc:
			// Each time a new git reference is received, cancel the last function call
			// and start a new one.
			refCancel()
			refCtx, refCancel = context.WithCancel(ctx)
			go func(ctx context.Context, ref string) {
				errc <- c.MonitorPipelines(refCtx, repositoryURL, ref)
			}(refCtx, ref)

		case e := <-errc:
			switch e {
			case context.Canceled:
				// Do nothing
			case cache.ErrUnknownGitReference:
				c.status.Write(fmt.Sprintf("error: git reference was not found on remote server(s)"))
				c.draw()
			default:
				err = e
			}

		case event := <-c.tui.eventc:
			err = c.process(ctx, event, refc)

		case <-ctx.Done():
			err = ctx.Err()
		}
	}

	if err == ErrExit {
		return nil
	}
	return err
}

func (c *Controller) SetHeader(lines []text.StyledString) {
	c.header.Write(lines...)
}

func (c *Controller) setStatus(s string) {
	c.status.Write(s)
}

func (c *Controller) clearStatus() {
	c.setStatus(c.defaultStatus)
}

func (c *Controller) refresh() {
	commit, _ := c.cache.Commit(c.ref)
	c.header.Write(commit.Strings()...)
	c.table.Refresh()
	c.resize(c.width, c.height)
}

func (c Controller) text() []text.LocalizedStyledString {
	texts := make([]text.LocalizedStyledString, 0)
	yOffset := 0

	for _, child := range []Widget{c.header, c.table, c.status} {
		for _, line := range child.Text() {
			line.Y += yOffset
			texts = append(texts, line)
		}
		_, height := child.Size()
		yOffset += height
	}

	return texts
}

func (c *Controller) NextMatch() {
	if c.tableSearch != "" {
		found := c.table.NextMatch(c.tableSearch, true)
		if !found {
			c.setStatus(fmt.Sprintf("No match found for %#v", c.tableSearch))
		}
	}
}

func (c *Controller) resize(width int, height int) {
	width = utils.MaxInt(width, 0)
	height = utils.MaxInt(height, 0)
	headerHeight := utils.MinInt(utils.MinInt(len(c.header.content)+2, 9), height)
	tableHeight := utils.MaxInt(0, height-headerHeight-1)
	statusHeight := height - headerHeight - tableHeight

	c.header.Resize(width, headerHeight)
	c.table.Resize(width, tableHeight)
	c.status.Resize(width, statusHeight)
	c.width, c.height = width, height
}

func (c *Controller) draw() {
	c.tui.Draw(c.text()...)
}

func (c *Controller) process(ctx context.Context, event tcell.Event, refc chan<- string) error {
	c.clearStatus()
	switch ev := event.(type) {
	case *tcell.EventResize:
		sx, sy := ev.Size()
		c.resize(sx, sy)
	case *tcell.EventKey:
		switch ev.Key() {
		case tcell.KeyDown:
			c.table.Scroll(+1)
		case tcell.KeyUp:
			c.table.Scroll(-1)
		case tcell.KeyPgDn:
			c.table.Scroll(c.table.NbrRows())
		case tcell.KeyPgUp:
			c.table.Scroll(-c.table.NbrRows())
		case tcell.KeyHome:
			c.table.Top()
		case tcell.KeyEnd:
			c.table.Bottom()
		case tcell.KeyEsc:
			if c.inputDestination != inputNone {
				c.inputDestination = inputNone
				c.status.ShowInput = false
			}
		case tcell.KeyEnter:
			if c.inputDestination != inputNone {
				switch c.inputDestination {
				case inputSearch:
					c.tableSearch = c.status.InputBuffer
					c.NextMatch()
				case inputRef:
					if ref := c.status.InputBuffer; ref != "" && ref != c.ref {
						go func() {
							select {
							case refc <- ref:
							case <-ctx.Done():
							}
						}()
					}
				}
				c.inputDestination = inputNone
				c.status.ShowInput = false
			} else {
				c.NextMatch()
			}
		case tcell.KeyCtrlU:
			if c.inputDestination != inputNone {
				c.status.InputBuffer = ""
			}
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if c.inputDestination != inputNone {
				runes := []rune(c.status.InputBuffer)
				if len(runes) > 0 {
					c.status.InputBuffer = string(runes[:len(runes)-1])
				}
			}
		case tcell.KeyRune:
			if c.inputDestination != inputNone {
				c.status.InputBuffer += string(ev.Rune())
				break
			}
			switch keyRune := ev.Rune(); keyRune {
			case 'b':
				browser := os.Getenv("BROWSER")
				if browser == "" {
					return errors.New("BROWSER environment variable not set")
				}
				if err := c.table.OpenInBrowser(browser); err != nil {
					return err
				}
			case 'j':
				c.table.Scroll(+1)
			case 'k':
				c.table.Scroll(-1)
			case 'c':
				c.table.SetTraversable(false, false)
			case 'C', '-':
				c.table.SetTraversable(false, true)
			case 'o':
				c.table.SetTraversable(true, false)
			case 'O', '+':
				c.table.SetTraversable(true, true)
			case 'n', 'N':
				if c.status.InputBuffer != "" {
					_ = c.table.NextMatch(c.status.InputBuffer, ev.Rune() == 'n')
				}
			case 'q':
				return ErrExit
			case '/':
				c.inputDestination = inputSearch
				c.status.ShowInput = true
				c.status.inputPrefix = "/"
				c.status.InputBuffer = ""
			case 'r':
				c.inputDestination = inputRef
				c.status.ShowInput = true
				c.status.InputBuffer = ""
				c.status.inputPrefix = "ref: "
			case 'u':
				// TODO Fix controller.setRef to preserve traversable state
				go func() {
					select {
					case refc <- c.ref:
					case <-ctx.Done():
					}
				}()
			case '?':
				file, err := ioutil.TempFile(c.tempDir, "citop_")
				if err != nil {
					return err
				}
				_, err = file.Write([]byte(c.help))
				if err != nil {
					return err
				}

				cmd := ExecCmd{
					name: "man",
					args: []string{"-l", path.Join(c.tempDir, path.Base(file.Name()))},
				}
				if err := c.tui.Exec(ctx, cmd); err != nil {
					return err
				}

			case 'v':
				c.setStatus("Fetching logs...")
				c.draw()
				defer func() {
					c.clearStatus()
					c.draw()
				}()

				logPath, err := c.table.WriteToDisk(ctx, c.tempDir)
				if err != nil {
					if err == cache.ErrNoLogHere {
						return nil
					}
					return err
				}

				cmd := ExecCmd{
					name: "less",
					args: []string{"-R", logPath},
				}

				return c.tui.Exec(ctx, cmd)
			}
		}
	}

	c.draw()
	return nil
}
