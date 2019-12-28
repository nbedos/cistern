package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/cistern/cache"
	"github.com/nbedos/cistern/text"
	"github.com/nbedos/cistern/utils"
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
}

var ErrExit = errors.New("exit")

type SourceFromRef = func(ref string) cache.HierarchicalTabularDataSource

func NewController(tui *TUI, ref string, c cache.Cache, loc *time.Location, defaultStatus string, help string) (Controller, error) {
	// Arbitrary values, the correct size will be set when the first RESIZE event is received
	width, height := tui.Size()
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
		width:         width,
		height:        height,
		header:        &header,
		table:         &table,
		status:        &status,
		defaultStatus: defaultStatus,
		help:          help,
	}, nil
}

func (c *Controller) setRef(ref string) error {
	if ref != c.ref {
		// TODO Preserve traversable state across calls to setRef()
		table, err := NewTable(c.cache.BuildsOfRef(ref), c.table.width, c.table.height, c.table.location)
		if err != nil {
			return err
		}
		c.table, c.ref = &table, ref
	}

	return nil
}

func (c *Controller) Run(ctx context.Context, repositoryURL string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error)
	refc := make(chan string)
	updates := make(chan time.Time)

	c.refresh()
	c.draw()

	// Start pipeline monitoring
	go func() {
		select {
		case refc <- c.ref:
		case <-ctx.Done():
		}
	}()

	var tmpRef string
	var mux = &sync.Mutex{}
	var refCtx context.Context
	var refCancel = func() {}
	var err error
	for err == nil {
		select {
		case ref := <-refc:
			// Each time a new git reference is received, cancel the last function call
			// and start a new one.
			mux.Lock()
			tmpRef = ref
			mux.Unlock()
			refCancel()
			refCtx, refCancel = context.WithCancel(ctx)
			go func(ctx context.Context, ref string) {
				errc <- c.cache.MonitorPipelines(ctx, repositoryURL, ref, updates)
			}(refCtx, ref)

		case <-updates:
			// Update the controller once we receive an update, meaning the reference exists at
			// least locally or remotely
			mux.Lock()
			if err := c.setRef(tmpRef); err != nil {
				return err
			}
			mux.Unlock()
			c.refresh()
			c.draw()

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

func (c *Controller) writeStatus(s string) {
	c.status.Write(s)
}

func (c *Controller) writeDefaultStatus() {
	c.writeStatus(c.defaultStatus)
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

func (c *Controller) nextMatch() {
	if c.tableSearch != "" {
		found := c.table.NextMatch(c.tableSearch, true)
		if !found {
			c.writeStatus(fmt.Sprintf("No match found for %#v", c.tableSearch))
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

// Turn `aaa\rbbb\rccc\r\n` into `ccc\r\n`
// This is mostly for Travis logs that contain metadata hidden by carriage returns
var deleteUntilCarriageReturn = regexp.MustCompile(`.*\r([^\r\n])`)

// https://stackoverflow.com/questions/14693701/how-can-i-remove-the-ansi-escape-sequences-from-a-string-in-python
var deleteANSIEscapeSequence = regexp.MustCompile(`\x1b[@-_][0-?]*[ -/]*[@-~]`)

func (c *Controller) viewLog(ctx context.Context) error {
	c.writeStatus("Fetching logs...")
	c.draw()
	defer func() {
		c.writeDefaultStatus()
		c.draw()
	}()

	log, err := c.table.ActiveRowLog(ctx)
	if err != nil {
		if err == cache.ErrNoLogHere {
			return nil
		}
		return err
	}

	stdin := bytes.Buffer{}
	log = deleteANSIEscapeSequence.ReplaceAllString(log, "")
	log = deleteUntilCarriageReturn.ReplaceAllString(log, "$1")
	stdin.WriteString(log)

	// FIXME Do not make this choice here, move this to the configuration
	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less"
	}

	return c.tui.Exec(ctx, pager, nil, &stdin)
}

func (c *Controller) viewHelp(ctx context.Context) error {
	// TODO Allow user configuration of this command
	// There is no standard way to make 'man' read from stdin
	// so instead we write the man page to disk and invoke
	// man with the '-l' option.
	file, err := ioutil.TempFile("", "cistern_*.man.1")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	if _, err := file.WriteString(c.help); err != nil {
		return err
	}

	return c.tui.Exec(ctx, "man", []string{"-l", file.Name()}, nil)
}

func (c Controller) openWebBrowser(url string) error {
	// FIXME Move this to the configuration
	browser := os.Getenv("BROWSER")
	if browser == "" {
		return errors.New(fmt.Sprintf("BROWSER environment variable not set. You can instead open %s in your browser.", url))
	}

	return utils.StartAndRelease(browser, []string{url})
}

func (c *Controller) draw() {
	c.tui.Draw(c.text()...)
}

func (c *Controller) process(ctx context.Context, event tcell.Event, refc chan<- string) error {
	c.writeDefaultStatus()
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
					c.nextMatch()
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
				c.nextMatch()
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
				if u := c.table.ActiveRowURL(); u.Valid {
					if err := c.openWebBrowser(u.String); err != nil {
						return err
					}
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
				if err := c.viewHelp(ctx); err != nil {
					return err
				}

			case 'v':
				if err := c.viewLog(ctx); err != nil {
					return err
				}
			}
		}
	}

	c.draw()
	return nil
}
