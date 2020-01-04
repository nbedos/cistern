package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/gdamore/tcell"
	"github.com/gdamore/tcell/encoding"
	"github.com/nbedos/cistern/cache"
	"github.com/nbedos/cistern/tui"
	"github.com/nbedos/cistern/utils"
)

var ErrNoProvider = errors.New("list of providers must not be empty")

type inputDestination int

const (
	inputNone inputDestination = iota
	inputSearch
	inputRef
)

type Controller struct {
	tui              *tui.TUI
	cache            cache.Cache
	ref              string
	width            int
	height           int
	header           *tui.TextArea
	table            *tui.HierarchicalTable
	tableSearch      string
	status           *tui.StatusBar
	inputDestination inputDestination
	defaultStatus    string
	help             string
}

var ErrExit = errors.New("exit")

func NewController(ui *tui.TUI, conf tui.ColumnConfiguration, ref string, c cache.Cache, loc *time.Location, defaultStatus string, help string) (Controller, error) {
	// Arbitrary values, the correct size will be set when the first RESIZE event is received
	width, height := ui.Size()
	header, err := tui.NewTextArea(width, height)
	if err != nil {
		return Controller{}, err
	}

	table, err := tui.NewHierarchicalTable(conf, nil, width, height, loc)
	if err != nil {
		return Controller{}, err
	}
	table.SortBy(cache.ColumnStarted, false)

	status, err := tui.NewStatusBar(width, height)
	if err != nil {
		return Controller{}, err
	}
	status.Write(defaultStatus)

	return Controller{
		tui:           ui,
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

func (c *Controller) setRef(ref string) {
	pipelines := make([]tui.TableNode, 0)
	for _, pipeline := range c.cache.Pipelines(c.ref) {
		pipelines = append(pipelines, pipeline)
	}
	c.table.Replace(pipelines)
	c.ref = ref
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
			c.setRef(tmpRef)
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

		case event := <-c.tui.Eventc:
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

func (c *Controller) SetHeader(lines []tui.StyledString) {
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
	pipelines := make([]tui.TableNode, 0)
	for _, pipeline := range c.cache.Pipelines(c.ref) {
		pipelines = append(pipelines, pipeline)
	}
	c.table.Replace(pipelines)
	c.resize(c.width, c.height)
}

func (c Controller) text() []tui.LocalizedStyledString {
	texts := make([]tui.LocalizedStyledString, 0)
	yOffset := 0

	for _, child := range []tui.Widget{c.header, c.table, c.status} {
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
		found := c.table.ScrollToNextMatch(c.tableSearch, true)
		if !found {
			c.writeStatus(fmt.Sprintf("No match found for %#v", c.tableSearch))
		}
	}
}

func (c *Controller) ReverseSortOrder() {
	if order := c.table.Order(); order.Valid {
		c.table.SortBy(order.ID, !order.Ascending)
	}
}

func (c *Controller) SortByNextColumn(reverse bool) {
	if order := c.table.Order(); order.Valid {
		ids := c.table.Configuration().ColumnIDs()
		for i, id := range ids {
			if id == order.ID {
				j := i + 1
				if reverse {
					j = i - 1
				}
				nextID := ids[utils.Modulo(j, len(ids))]
				c.table.SortBy(nextID, order.Ascending)
				return
			}
		}
	}
}

func (c *Controller) resize(width int, height int) {
	width = utils.MaxInt(width, 0)
	height = utils.MaxInt(height, 0)
	headerHeight := utils.MinInt(utils.MinInt(len(c.header.Content)+2, 9), height)
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
	key, ids, exists := c.activeStepPath()
	if !exists {
		return cache.ErrNoLogHere
	}

	log, err := c.cache.Log(ctx, key, ids)
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

func (c Controller) activeStepPath() (cache.PipelineKey, []string, bool) {
	if stepPath := c.table.ActiveNodePath(); len(stepPath) > 0 {
		key, ok := stepPath[0].(cache.PipelineKey)
		if !ok {
			return cache.PipelineKey{}, nil, false
		}

		stepIDs := make([]string, 0)
		for _, id := range stepPath[1:] {
			s, ok := id.(string)
			if !ok {
				return cache.PipelineKey{}, nil, false
			}
			stepIDs = append(stepIDs, s)
		}

		return key, stepIDs, true
	}

	return cache.PipelineKey{}, nil, false
}

func (c Controller) openActiveRowInBrowser() error {
	if key, ids, exists := c.activeStepPath(); exists {
		step, exists := c.cache.Step(key, ids)
		if exists && step.WebURL.Valid {
			// TODO Move this to configuration file
			browser := os.Getenv("BROWSER")
			if browser == "" {
				return errors.New(fmt.Sprintf("BROWSER environment variable not set. You can instead open %s in your browser.", step.WebURL.String))
			}

			return utils.StartAndRelease(browser, []string{step.WebURL.String})
		}
	}

	return nil
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
			c.table.Scroll(c.table.PageSize())
		case tcell.KeyPgUp:
			c.table.Scroll(-c.table.PageSize())
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
				if err := c.openActiveRowInBrowser(); err != nil {
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
					_ = c.table.ScrollToNextMatch(c.status.InputBuffer, ev.Rune() == 'n')
				}
			case 'q':
				return ErrExit
			case '/':
				c.inputDestination = inputSearch
				c.status.ShowInput = true
				c.status.InputPrefix = "/"
				c.status.InputBuffer = ""
			case 'r':
				c.inputDestination = inputRef
				c.status.ShowInput = true
				c.status.InputBuffer = ""
				c.status.InputPrefix = "ref: "
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
			case '>':
				c.SortByNextColumn(false)
			case '<':
				c.SortByNextColumn(true)
			case '!':
				c.ReverseSortOrder()
			}
		}
	}

	c.draw()
	return nil
}

func RunApplication(ctx context.Context, newScreen func() (tcell.Screen, error), repo string, ref string, CIProviders []cache.CIProvider, SourceProviders []cache.SourceProvider, loc *time.Location, help string) (err error) {
	if len(CIProviders) == 0 || len(SourceProviders) == 0 {
		return ErrNoProvider
	}
	// FIXME Discard log until the status bar is implemented in order to hide the "Unsolicited response received on
	//  idle HTTP channel" from GitLab's HTTP client
	log.SetOutput(ioutil.Discard)
	encoding.Register()

	defaultStyle := tcell.StyleDefault
	styleSheet := tui.StyleSheet{
		tui.TableHeader: func(s tcell.Style) tcell.Style {
			return s.Bold(true).Reverse(true)
		},
		tui.ActiveRow: func(s tcell.Style) tcell.Style {
			return s.Background(tcell.ColorSilver).Foreground(tcell.ColorBlack).Bold(false).Underline(false).Blink(false)
		},
		tui.Provider: func(s tcell.Style) tcell.Style {
			return s.Bold(true)
		},
		tui.StatusFailed: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorMaroon).Bold(false)
		},
		tui.StatusPassed: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorGreen).Bold(false)
		},
		tui.StatusRunning: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorOlive).Bold(false)
		},
		tui.StatusSkipped: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorGray).Bold(false)
		},
		tui.GitSha: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorOlive)
		},
		tui.GitBranch: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorTeal).Bold(false)
		},
		tui.GitTag: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorYellow).Bold(false)
		},
		tui.GitHead: func(s tcell.Style) tcell.Style {
			return s.Foreground(tcell.ColorAqua)
		},
	}
	defaultStatus := "j:Down  k:Up  oO:Open  cC:Close  /:Search  v:Logs  b:Browser  ?:Help  q:Quit"

	const maxWidth = 999

	tableConfig := map[tui.ColumnID]tui.Column{
		cache.ColumnRef: {
			Header:    "REF",
			Order:     1,
			MaxWidth:  maxWidth,
			Alignment: tui.Left,
			Less: func(nodes []tui.TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					refi := nodes[i].(cache.Pipeline).Values(loc)[cache.ColumnRef]
					refj := nodes[j].(cache.Pipeline).Values(loc)[cache.ColumnRef]

					if asc {
						return refi.String() < refj.String()
					} else {
						return refj.String() > refi.String()
					}
				}
			},
		},
		cache.ColumnPipeline: {
			Order:     2,
			Header:    "PIPELINE",
			MaxWidth:  maxWidth,
			Alignment: tui.Right,
			Less: func(nodes []tui.TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					IDi := nodes[i].(cache.Pipeline).Values(loc)[cache.ColumnPipeline]
					IDj := nodes[j].(cache.Pipeline).Values(loc)[cache.ColumnPipeline]

					if asc {
						return IDi.String() < IDj.String()
					} else {
						return IDi.String() > IDj.String()
					}
				}
			},
		},
		cache.ColumnType: {
			Order:     3,
			Header:    "TYPE",
			MaxWidth:  maxWidth,
			Alignment: tui.Left,
			// The following sorting function has no effect on the table since all Pipelines
			// have the same type. We just include it for consistency.
			Less: func(nodes []tui.TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					ni := nodes[i].(cache.Pipeline)
					nj := nodes[j].(cache.Pipeline)

					if asc {
						return ni.Type < nj.Type
					} else {
						return ni.Type > nj.Type
					}
				}
			},
		},
		cache.ColumnState: {
			Order:     4,
			Header:    "STATE",
			MaxWidth:  maxWidth,
			Alignment: tui.Left,
			Less: func(nodes []tui.TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					ni := nodes[i].(cache.Pipeline)
					nj := nodes[j].(cache.Pipeline)

					if asc {
						return ni.State < nj.State
					} else {
						return ni.State > nj.State
					}
				}
			},
		},
		cache.ColumnAllowedFailure: {
			Order:     5,
			Header:    "XFAIL",
			MaxWidth:  maxWidth,
			Alignment: tui.Left,
			Less: func(nodes []tui.TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					faili := nodes[i].(cache.Pipeline).Values(loc)[cache.ColumnAllowedFailure]
					failj := nodes[j].(cache.Pipeline).Values(loc)[cache.ColumnAllowedFailure]

					if asc {
						return faili.String() < failj.String()
					} else {
						return faili.String() > failj.String()
					}
				}
			},
		},
		cache.ColumnCreated: {
			Order:     6,
			Header:    "CREATED",
			MaxWidth:  maxWidth,
			Alignment: tui.Left,
			Less: func(nodes []tui.TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					ni := nodes[i].(cache.Pipeline)
					nj := nodes[j].(cache.Pipeline)

					if asc {
						return ni.CreatedAt.Before(nj.CreatedAt)
					} else {
						return ni.CreatedAt.After(nj.CreatedAt)
					}
				}
			},
		},
		cache.ColumnStarted: {
			Order:     7,
			Header:    "STARTED",
			MaxWidth:  maxWidth,
			Alignment: tui.Left,
			Less: func(nodes []tui.TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					ti := nodes[i].(cache.Pipeline).StartedAt.Time
					tj := nodes[j].(cache.Pipeline).StartedAt.Time

					// Assume that Null values are attributed to events that will occur in the
					// future, so give them a maximal value
					if !nodes[i].(cache.Pipeline).StartedAt.Valid {
						ti = time.Unix(1<<62, 0)
					}

					if !nodes[j].(cache.Pipeline).StartedAt.Valid {
						tj = time.Unix(1<<62, 0)
					}

					if asc {
						return ti.Before(tj)
					} else {
						return ti.After(tj)
					}
				}
			},
		},
		cache.ColumnFinished: {
			Order:     8,
			Header:    "FINISHED",
			MaxWidth:  maxWidth,
			Alignment: tui.Left,
			Less: func(nodes []tui.TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					ti := nodes[i].(cache.Pipeline).FinishedAt.Time
					tj := nodes[j].(cache.Pipeline).FinishedAt.Time

					if !nodes[i].(cache.Pipeline).FinishedAt.Valid {
						ti = time.Unix(1<<62, 0)
					}

					if !nodes[j].(cache.Pipeline).FinishedAt.Valid {
						tj = time.Unix(1<<62, 0)
					}

					if asc {
						return ti.Before(tj)
					} else {
						return ti.After(tj)
					}
				}
			},
		},
		cache.ColumnDuration: {
			Order:     10,
			Header:    "DURATION",
			MaxWidth:  maxWidth,
			Alignment: tui.Right,
			Less: func(nodes []tui.TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					ni := nodes[i].(cache.Pipeline)
					nj := nodes[j].(cache.Pipeline)

					if !ni.Duration.Valid {
						if !nj.Duration.Valid {
							return false
						}
						return asc
					}

					if asc {
						return ni.Duration.Duration < nj.Duration.Duration
					} else {
						return ni.Duration.Duration > nj.Duration.Duration
					}
				}
			},
		},
		cache.ColumnName: {
			Order:      11,
			Header:     "NAME",
			MaxWidth:   maxWidth,
			Alignment:  tui.Left,
			TreePrefix: true,
			Less: func(nodes []tui.TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					namei := nodes[i].(cache.Pipeline).Values(loc)[cache.ColumnName]
					namej := nodes[j].(cache.Pipeline).Values(loc)[cache.ColumnName]

					if asc {
						return namei.String() < namej.String()
					} else {
						return namei.String() > namej.String()
					}
				}
			},
		},
		cache.ColumnWebURL: {
			Order:     12,
			Header:    "URL",
			MaxWidth:  maxWidth,
			Alignment: tui.Left,
			Less: func(nodes []tui.TableNode, asc bool) func(i, j int) bool {
				return func(i, j int) bool {
					urli := nodes[i].(cache.Pipeline).Values(loc)[cache.ColumnWebURL]
					urlj := nodes[j].(cache.Pipeline).Values(loc)[cache.ColumnWebURL]

					if asc {
						return urli.String() < urlj.String()
					} else {
						return urli.String() > urlj.String()
					}
				}
			},
		},
	}

	ui, err := tui.NewTUI(newScreen, defaultStyle, styleSheet)
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

	controller, err := NewController(&ui, tableConfig, ref, cacheDB, loc, defaultStatus, help)
	if err != nil {
		return err
	}

	return controller.Run(ctx, repo)
}
