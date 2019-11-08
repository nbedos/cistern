package tui

import (
	"context"
	"errors"
	"fmt"
	"github.com/gdamore/tcell"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/man"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
	"github.com/nbedos/citop/widgets"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
)

type TableController struct {
	table         *widgets.Table
	status        *widgets.StatusBar
	tempDir       string
	inputMode     bool
	defaultStatus string
}

func NewTableController(source cache.HierarchicalTabularDataSource, tempDir string, defaultStatus string) (TableController, error) {
	// TODO Move this out of here
	headers := []string{"REF", "COMMIT", "TYPE", "STATE", "CREATED", "DURATION", "NAME"}
	// FIXME This should be included in classes
	alignment := map[string]text.Alignment{
		"REF":      text.Left,
		"COMMIT":   text.Left,
		"TYPE":     text.Right,
		"STATE":    text.Left,
		"CREATED":  text.Left,
		"STARTED":  text.Left,
		"UPDATED":  text.Left,
		"DURATION": text.Right,
		"NAME":     text.Left,
	}

	// Arbitrary values, the correct size will be set when the first RESIZE event is received
	width, height := 10, 10
	table, err := widgets.NewTable(source, headers, alignment, width, height, "  ")
	if err != nil {
		return TableController{}, err
	}

	status, err := widgets.NewStatusBar(width, height)
	if err != nil {
		return TableController{}, err
	}
	status.Write(defaultStatus)

	return TableController{
		table:         &table,
		status:        &status,
		tempDir:       tempDir,
		defaultStatus: defaultStatus,
	}, nil
}

func (c *TableController) SetStatus(s string) {
	c.status.Write(s)
}

func (c *TableController) ClearStatus() {
	c.SetStatus(c.defaultStatus)
}

func (c *TableController) Refresh() ([]text.LocalizedStyledString, error) {
	if err := c.table.Refresh(); err != nil {
		return nil, err
	}

	return c.Text()
}

func (c TableController) Text() ([]text.LocalizedStyledString, error) {
	texts := make([]text.LocalizedStyledString, 0)
	yOffset := 0

	for _, child := range []widgets.Widget{c.table, c.status} {
		childTexts, err := child.Text()
		if err != nil {
			return texts, err
		}
		for _, line := range childTexts {
			line.Y += yOffset
			texts = append(texts, line)
		}
		_, height := child.Size()
		yOffset += height
	}

	return texts, nil
}

func (c *TableController) Resize(width int, height int) error {
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

func (c *TableController) SendShowText(ctx context.Context, outc chan OutputEvent) error {
	texts, err := c.Text()
	if err != nil {
		return err
	}
	select {
	case outc <- ShowText{content: texts}:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (c *TableController) Process(ctx context.Context, event tcell.Event, outc chan OutputEvent) error {
	c.ClearStatus()
	switch ev := event.(type) {
	case *tcell.EventResize:
		sx, sy := ev.Size()
		if err := c.Resize(sx, sy); err != nil {
			log.Fatal(err)
		}

		return c.SendShowText(ctx, outc)

	case *tcell.EventKey:
		switch ev.Key() {
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
					c.SetStatus(fmt.Sprintf("No match found for %#v", c.status.InputBuffer))
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
				return c.SendShowText(ctx, outc)
			}
			switch keyRune := ev.Rune(); keyRune {
			case 'q':
				select {
				case outc <- ExitEvent{}:
				case <-ctx.Done():
					return ctx.Err()
				}
				return nil
			case '/':
				c.inputMode = true
				c.status.ShowInput = true
				c.status.InputBuffer = ""
			case '?':
				// FIXME Just write on stdin instead of using a temporary file
				file, err := ioutil.TempFile(c.tempDir, "citop_")
				if err != nil {
					return err
				}
				_, err = file.Write([]byte(man.Section1))
				if err != nil {
					return err
				}

				var cmd *exec.Cmd
				cmd = exec.Command("less", path.Join(c.tempDir, path.Base(file.Name())))
				cmd.Dir = c.tempDir

				select {
				case outc <- ExecCmd{cmd: *cmd, stream: nil}:
				case <-ctx.Done():
					return ctx.Err()
				}
				return nil

			case 'e', 'v':
				c.SetStatus("Fetching logs...")
				if err := c.SendShowText(ctx, outc); err != nil {
					return err
				}
				defer func() {
					c.ClearStatus()
					c.SendShowText(ctx, outc)
				}()

				if c.table.ActiveLine < 0 || c.table.ActiveLine >= len(c.table.Rows) {
					return nil
				}

				key := c.table.Rows[c.table.ActiveLine].Key()
				logPath, stream, err := c.table.Source.WriteToDirectory(ctx, key, c.tempDir)
				if err != nil {
					if err == cache.ErrNoLogHere {
						return nil
					}
					return err
				}

				var cmd *exec.Cmd
				var args []string
				if stream == nil {
					args = []string{"-R"}
				} else {
					args = []string{"+F", "-R"}
				}
				cmd = exec.Command("less", append(args, logPath)...)
				cmd.Dir = c.tempDir

				select {
				case outc <- ExecCmd{cmd: *cmd, stream: stream}:
				case <-ctx.Done():
					return ctx.Err()
				}
				return nil
			}
		}
	}

	if err := ProcessDefaultTableEvents(c.table, event, c.status.InputBuffer); err != nil {
		return err
	}

	return c.SendShowText(ctx, outc)
}

func ProcessDefaultTableEvents(table *widgets.Table, event tcell.Event, search string) error {
	switch ev := event.(type) {
	case *tcell.EventKey:
		switch ev.Key() {
		case tcell.KeyDown:
			return table.Scroll(+1)
		case tcell.KeyUp:
			return table.Scroll(-1)
		case tcell.KeyPgDn:
			_, height := table.Size()
			return table.Scroll(utils.MaxInt(1, height-2))
		case tcell.KeyPgUp:
			_, height := table.Size()
			return table.Scroll(-utils.MaxInt(1, height-2))
		case tcell.KeyHome:
			return table.Top()
		case tcell.KeyEnd:
			return table.Bottom()
		case tcell.KeyRune:
			switch ev.Rune() {
			case 'b':
				if table.ActiveLine >= 0 && table.ActiveLine < len(table.Rows) {
					browser := os.Getenv("BROWSER")
					if browser == "" {
						return errors.New("environment variable BROWSER not set")
					}
					if url := table.Rows[table.ActiveLine].URL(); url != "" {
						argv := []string{path.Base(browser), url}
						process, err := os.StartProcess(browser, argv, &os.ProcAttr{})
						if err != nil {
							return err
						}

						if err := process.Release(); err != nil {
							return err
						}
					}
				}
			case 'j':
				return table.Scroll(+1)
			case 'k':
				return table.Scroll(-1)
			case 'c':
				return table.SetFold(false, false)
			case 'C', '-':
				return table.SetFold(false, true)
			case 'o':
				return table.SetFold(true, false)
			case 'O', '+':
				return table.SetFold(true, true)
			case 'n', 'N':
				// FIXME Return found status
				if search != "" {
					_ = table.NextMatch(search, ev.Rune() == 'n')
				}
			}
		}
	}

	return nil
}
