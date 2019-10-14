package tui

import (
	"context"
	"errors"
	"github.com/gdamore/tcell"
	"github.com/nbedos/citop/cache"
	"github.com/nbedos/citop/utils"
	"github.com/nbedos/citop/widgets"
	"log"
	"os"
	"os/exec"
	"path"
)

type TableController struct {
	Table     widgets.Table
	Status    widgets.StatusBar
	TempDir   string
	InputMode bool
}

func NewTableController(source cache.HierarchicalTabularDataSource, tempDir string) (TableController, error) {
	// TODO Move this out of here
	headers := []string{"ACCOUNT", "ID", "TYPE", "STATE", "UPDATED", " NAME"}

	// Arbitrary values, the correct size will be set when the first RESIZE event is received
	width, height := 10, 10
	table, err := widgets.NewTable(source, headers, width, height, "   ")
	if err != nil {
		return TableController{}, err
	}

	status, err := widgets.NewStatusBar(width, height, 1)
	if err != nil {
		return TableController{}, err
	}

	return TableController{
		Table:   table,
		Status:  status,
		TempDir: tempDir,
	}, nil
}

func (l *TableController) Refresh() ([]widgets.StyledText, error) {
	if err := l.Table.Refresh(); err != nil {
		return nil, err
	}

	return l.Text()
}

func (l TableController) Text() ([]widgets.StyledText, error) {
	texts := make([]widgets.StyledText, 0)
	yOffset := 0

	for _, child := range []widgets.Widget{&l.Table, &l.Status} {
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

func (l *TableController) Resize(width int, height int) error {
	if width < 0 || height < 0 {
		return errors.New("width and height must be >=0")
	}
	tableHeight := utils.MaxInt(0, height-1)
	statusHeight := height - tableHeight

	if err := l.Table.Resize(width, tableHeight); err != nil {
		return err
	}

	if err := l.Status.Resize(width, statusHeight); err != nil {
		return err
	}

	return nil
}

func (l *TableController) Process(event tcell.Event) (OutputEvent, error) {
	switch ev := event.(type) {
	case *tcell.EventResize:
		sx, sy := ev.Size()
		if err := l.Resize(sx, sy); err != nil {
			log.Fatal(err)
		}

		texts, err := l.Text()
		return ShowText{content: texts}, err

	case *tcell.EventKey:
		switch ev.Key() {
		case tcell.KeyEsc:
			if l.InputMode {
				l.InputMode = false
				l.Status.ShowInput = false
			}
		case tcell.KeyEnter:
			if l.InputMode {
				l.InputMode = false
				l.Status.ShowInput = false
			}
			if l.Status.InputBuffer != "" {
				/*if err := l.Table.Search(l.Status.InputBuffer); err != nil {
					return l, NoEvent{}, err
				}*/
			}
		case tcell.KeyCtrlU:
			if l.InputMode {
				l.Status.InputBuffer = ""
			}

		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if l.InputMode {
				runes := []rune(l.Status.InputBuffer)
				if len(runes) > 0 {
					l.Status.InputBuffer = string(runes[:len(runes)-1])
				}
			}
		case tcell.KeyRune:
			if l.InputMode {
				l.Status.InputBuffer += string(ev.Rune())

				texts, err := l.Text()
				return ShowText{content: texts}, err
			}
			switch keyRune := ev.Rune(); keyRune {
			case 'q':
				return ExitEvent{}, nil
			case '/':
				l.InputMode = true
				l.Status.ShowInput = true
			case 'e', 'v':
				if l.Table.ActiveLine < 0 || l.Table.ActiveLine >= len(l.Table.Rows) {
					return NoEvent{}, nil
				}
				workDirPath := path.Join(l.TempDir, "build_dump")
				if err := os.RemoveAll(workDirPath); err != nil && !os.IsNotExist(err) {
					return NoEvent{}, err
				}

				if err := os.Mkdir(workDirPath, 0750); err != nil {
					return NoEvent{}, err
				}

				ctx := context.Background()
				key := l.Table.Rows[l.Table.ActiveLine].Key()
				paths, err := l.Table.Source.WriteToDirectory(ctx, key, workDirPath)
				if err != nil {
					return NoEvent{}, err
				}
				if len(paths) == 0 {
					// FIXME Display 'No matching (errored) jobs' message in status bar
					return NoEvent{}, nil
				}
				paths = append([]string{"-R"}, paths...)
				cmd := exec.Command("less", paths...)
				cmd.Dir = workDirPath

				return ExecCmd{cmd: *cmd}, nil
			}
		}
	}

	_, err := ProcessDefaultTableEvents(&l.Table, event)
	if err != nil {
		return NoEvent{}, err
	}

	texts, err := l.Text()
	return ShowText{content: texts}, err
}

func ProcessDefaultTableEvents(table *widgets.Table, event tcell.Event) (bool, error) {
	var err error
	processed := true

	switch ev := event.(type) {
	case *tcell.EventKey:
		switch ev.Key() {
		case tcell.KeyDown:
			err = table.Scroll(+1)
		case tcell.KeyUp:
			err = table.Scroll(-1)
		case tcell.KeyPgDn:
			_, height := table.Size()
			err = table.Scroll(utils.MaxInt(1, height-2))
		case tcell.KeyPgUp:
			_, height := table.Size()
			err = table.Scroll(-utils.MaxInt(1, height-2))
		case tcell.KeyHome:
			err = table.Top()
		case tcell.KeyEnd:
			err = table.Bottom()
		case tcell.KeyRune:
			switch ev.Rune() {
			case 'b':
				if table.ActiveLine >= 0 && table.ActiveLine < len(table.Rows) {
					browser := os.Getenv("BROWSER")
					if browser == "" {
						return false, errors.New("environment variable BROWSER not set")
					}
					// FIXME If the URL is missing, 'b' should not be usable and this code path
					//  should never be taken
					if url := table.Rows[table.ActiveLine].URL(); url != "" {
						argv := []string{path.Base(browser), url}
						process, err := os.StartProcess(browser, argv, &os.ProcAttr{})
						if err != nil {
							return false, err
						}

						if err := process.Release(); err != nil {
							return false, err
						}
					}
				}
			case 'j':
				err = table.Scroll(+1)
			case 'k':
				err = table.Scroll(-1)
			case 'r':
				// FIXME still useful?
				err = table.Refresh()
			case 'c':
				err = table.SetFold(false, false)
			case 'C':
				err = table.SetFold(false, true)
			case 'o':
				err = table.SetFold(true, false)
			case 'O':
				err = table.SetFold(true, true)
			default:
				processed = false
			}
		default:
			processed = false
		}
	}

	return processed, err
}
