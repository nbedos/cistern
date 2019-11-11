package tui

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/man"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
	"github.com/nbedos/citop/widgets"
)

type TableController struct {
	tui           *TUI
	table         *widgets.Table
	status        *widgets.StatusBar
	tempDir       string
	inputMode     bool
	defaultStatus string
}

func NewTableController(tui *TUI, source cache.HierarchicalTabularDataSource, tempDir string, defaultStatus string) (TableController, error) {
	// Arbitrary values, the correct size will be set when the first RESIZE event is received
	width, height := 10, 10
	table, err := widgets.NewTable(source, width, height, "  ")
	if err != nil {
		return TableController{}, err
	}

	status, err := widgets.NewStatusBar(width, height)
	if err != nil {
		return TableController{}, err
	}
	status.Write(defaultStatus)

	return TableController{
		tui:           tui,
		table:         &table,
		status:        &status,
		tempDir:       tempDir,
		defaultStatus: defaultStatus,
	}, nil
}

func (c *TableController) Run(ctx context.Context, updates <-chan time.Time) error {
	for exit := false; !exit; {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-updates:
			texts, err := c.refresh()
			if err != nil {
				return err
			}
			c.tui.Draw(texts...)
		case event := <-c.tui.eventc:
			switch err := c.process(ctx, event); err {
			case nil:
				// Do nothing
			case ErrExit:
				exit = true
			default:
				return err
			}
		}
	}

	return nil
}

func (c *TableController) setStatus(s string) {
	c.status.Write(s)
}

func (c *TableController) clearStatus() {
	c.setStatus(c.defaultStatus)
}

func (c *TableController) refresh() ([]text.LocalizedStyledString, error) {
	if err := c.table.Refresh(); err != nil {
		return nil, err
	}

	return c.text(), nil
}

func (c TableController) text() []text.LocalizedStyledString {
	texts := make([]text.LocalizedStyledString, 0)
	yOffset := 0

	for _, child := range []widgets.Widget{c.table, c.status} {
		for _, line := range child.Text() {
			line.Y += yOffset
			texts = append(texts, line)
		}
		_, height := child.Size()
		yOffset += height
	}

	return texts
}

func (c *TableController) resize(width int, height int) error {
	if width < 0 || height < 0 {
		return errors.New("width and height must be >=0")
	}
	tableHeight := utils.MaxInt(0, height-1)
	statusHeight := height - tableHeight

	if err := c.table.Resize(width, tableHeight); err != nil {
		return err
	}

	if err := c.status.Resize(width, statusHeight); err != nil {
		return err
	}

	return nil
}

func (c *TableController) draw() {
	c.tui.Draw(c.text()...)
}

var ErrExit = errors.New("exit")

func (c *TableController) process(ctx context.Context, event tcell.Event) error {
	c.clearStatus()
	switch ev := event.(type) {
	case *tcell.EventResize:
		sx, sy := ev.Size()
		if err := c.resize(sx, sy); err != nil {
			log.Fatal(err)
		}
	case *tcell.EventKey:
		switch ev.Key() {
		case tcell.KeyDown:
			return c.table.Scroll(+1)
		case tcell.KeyUp:
			return c.table.Scroll(-1)
		case tcell.KeyPgDn:
			_, height := c.table.Size()
			return c.table.Scroll(utils.MaxInt(1, height-2))
		case tcell.KeyPgUp:
			_, height := c.table.Size()
			return c.table.Scroll(-utils.MaxInt(1, height-2))
		case tcell.KeyHome:
			return c.table.Top()
		case tcell.KeyEnd:
			return c.table.Bottom()
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
				if err := c.table.Scroll(+1); err != nil {
					return err
				}
			case 'k':
				if err := c.table.Scroll(-1); err != nil {
					return err
				}
			case 'c':
				if err := c.table.SetFold(false, false); err != nil {
					return err
				}
			case 'C', '-':
				if err := c.table.SetFold(false, true); err != nil {
					return err
				}
			case 'o':
				if err := c.table.SetFold(true, false); err != nil {
					return err
				}
			case 'O', '+':
				if err := c.table.SetFold(true, true); err != nil {
					return err
				}
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
				_, err = file.Write([]byte(man.Section1))
				if err != nil {
					return err
				}

				cmd := ExecCmd{
					name:   "less",
					args:   []string{path.Join(c.tempDir, path.Base(file.Name()))},
					dir:    c.tempDir,
					stream: nil,
				}
				if err := c.tui.Exec(ctx, cmd); err != nil {
					return err
				}

			case 'e', 'v':
				c.setStatus("Fetching logs...")
				c.draw()
				defer func() {
					c.clearStatus()
					c.draw()
				}()

				logPath, stream, err := c.table.WriteToDisk(ctx, c.tempDir)
				if err != nil {
					if err == cache.ErrNoLogHere {
						return nil
					}
					return err
				}

				var args []string
				if stream == nil {
					args = []string{"-R"}
				} else {
					args = []string{"+F", "-R"}
				}

				cmd := ExecCmd{
					name:   "less",
					args:   append(args, logPath),
					dir:    c.tempDir,
					stream: stream,
				}

				return c.tui.Exec(ctx, cmd)
			}
		}
	}

	c.draw()
	return nil
}
