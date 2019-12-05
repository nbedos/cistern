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

type Controller struct {
	tui           *TUI
	header        *TextArea
	table         *Table
	status        *StatusBar
	tempDir       string
	inputMode     bool
	defaultStatus string
	help          string
}

var ErrExit = errors.New("exit")

func NewController(tui *TUI, source cache.HierarchicalTabularDataSource, loc *time.Location, tempDir string, defaultStatus string, help string) (Controller, error) {
	// Arbitrary values, the correct size will be set when the first RESIZE event is received
	width, height := 10, 10
	header, err := NewTextArea(width, height)
	if err != nil {
		return Controller{}, err
	}

	table, err := NewTable(source, width, height, loc)
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
		header:        &header,
		table:         &table,
		status:        &status,
		tempDir:       tempDir,
		defaultStatus: defaultStatus,
		help:          help,
	}, nil
}

func (c *Controller) Run(ctx context.Context, updates <-chan time.Time) error {
	var err error
	for err == nil {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-updates:
			c.refresh()
			c.draw()
		case event := <-c.tui.eventc:
			err = c.process(ctx, event)
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
	c.table.Refresh()
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

func (c *Controller) resize(width int, height int) {
	width = utils.MaxInt(width, 0)
	height = utils.MaxInt(height, 0)
	headerHeight := utils.MinInt(utils.MinInt(len(c.header.content)+2, 9), height)
	tableHeight := utils.MaxInt(0, height-headerHeight-1)
	statusHeight := height - headerHeight - tableHeight

	c.header.Resize(width, headerHeight)
	c.table.Resize(width, tableHeight)
	c.status.Resize(width, statusHeight)
}

func (c *Controller) draw() {
	c.tui.Draw(c.text()...)
}

func (c *Controller) process(ctx context.Context, event tcell.Event) error {
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
			if c.inputMode {
				c.inputMode = false
				c.status.ShowInput = false
			}
		case tcell.KeyEnter:
			if c.inputMode {
				c.inputMode = false
				c.status.ShowInput = false
			}
			if c.status.InputBuffer != "" {
				found := c.table.NextMatch(c.status.InputBuffer, true)
				if !found {
					c.setStatus(fmt.Sprintf("No match found for %#v", c.status.InputBuffer))
				}
			}
		case tcell.KeyCtrlU:
			if c.inputMode {
				c.status.InputBuffer = ""
			}
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if c.inputMode {
				runes := []rune(c.status.InputBuffer)
				if len(runes) > 0 {
					c.status.InputBuffer = string(runes[:len(runes)-1])
				}
			}
		case tcell.KeyRune:
			if c.inputMode {
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
				c.inputMode = true
				c.status.ShowInput = true
				c.status.InputBuffer = ""
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
