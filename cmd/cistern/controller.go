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
	"github.com/nbedos/cistern/providers"
	"github.com/nbedos/cistern/tui"
	"github.com/nbedos/cistern/utils"
)

type inputDestination int

const (
	inputNone inputDestination = iota
	inputSearch
	inputRef
)

type ControllerConfiguration struct {
	tui.TableConfiguration
	providers.GitStyle
}

type Controller struct {
	tui              *tui.TUI
	cache            providers.Cache
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
	style            providers.GitStyle
}

var ErrExit = errors.New("exit")

func NewController(ui *tui.TUI, conf ControllerConfiguration, ref string, c providers.Cache, defaultStatus string, help string) (Controller, error) {
	// Arbitrary values, the correct size will be set when the first RESIZE event is received
	width, height := ui.Size()
	header, err := tui.NewTextArea(width, height)
	if err != nil {
		return Controller{}, err
	}

	table, err := tui.NewHierarchicalTable(conf.TableConfiguration, nil, width, height)
	if err != nil {
		return Controller{}, err
	}


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
		style:         conf.GitStyle,
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
			case providers.ErrUnknownGitReference:
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
	c.header.Write(commit.StyledStrings(c.style)...)
	pipelines := make([]tui.TableNode, 0)
	for _, pipeline := range c.cache.Pipelines(c.ref) {
		pipelines = append(pipelines, pipeline)
	}
	c.table.Replace(pipelines)
	c.resize(c.width, c.height)
}

func (c Controller) text() []tui.StyledString {
	ss := make([]tui.StyledString, 0)
	for _, child := range []tui.Widget{c.header, c.table, c.status} {
		for _, s := range child.StyledStrings() {
			ss = append(ss, s)
		}
	}

	return ss
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
		ids := c.table.Configuration().Columns.IDs()
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
		return providers.ErrNoLogHere
	}

	log, err := c.cache.Log(ctx, key, ids)
	if err != nil {
		if err == providers.ErrNoLogHere {
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

func (c Controller) activeStepPath() (providers.PipelineKey, []string, bool) {
	if stepPath := c.table.ActiveNodePath(); len(stepPath) > 0 {
		key, ok := stepPath[0].(providers.PipelineKey)
		if !ok {
			return providers.PipelineKey{}, nil, false
		}

		stepIDs := make([]string, 0)
		for _, id := range stepPath[1:] {
			s, ok := id.(string)
			if !ok {
				return providers.PipelineKey{}, nil, false
			}
			stepIDs = append(stepIDs, s)
		}

		return key, stepIDs, true
	}

	return providers.PipelineKey{}, nil, false
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
			c.table.VerticalScroll(+1)
		case tcell.KeyUp:
			c.table.VerticalScroll(-1)
		case tcell.KeyLeft:
			c.table.HorizontalScroll(-1)
		case tcell.KeyRight:
			c.table.HorizontalScroll(+1)
		case tcell.KeyPgDn:
			c.table.VerticalScroll(c.table.PageSize())
		case tcell.KeyPgUp:
			c.table.VerticalScroll(-c.table.PageSize())
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
				c.table.VerticalScroll(+1)
			case 'k':
				c.table.VerticalScroll(-1)
			case 'h':
				c.table.HorizontalScroll(-1)
			case 'l':
				c.table.HorizontalScroll(+1)
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

const defaultStatus = "j:Down  k:Up  oO:Open  cC:Close  /:Search  v:Logs  b:Browser  ?:Help  q:Quit"

func RunApplication(ctx context.Context, newScreen func() (tcell.Screen, error), repo string, ref string, conf Configuration) error {
	// FIXME Discard log until the status bar is implemented in order to hide the "Unsolicited response received on
	//  idle HTTP channel" from GitLab's HTTP client
	log.SetOutput(ioutil.Discard)

	tableConfig, err := conf.TableConfig(defaultTableColumns)
	if err != nil {
		return err
	}

	// Keep this before NewTUI since it may use stdin/stderr for password prompt
	cacheDB, err := conf.Providers.ToCache(ctx)
	if err != nil {
		return err
	}

	encoding.Register()
	defaultStyle := tcell.StyleDefault
	if conf.Style.Default != nil {
		transform, err := conf.Style.Default.Parse()
		if err != nil {
			return err
		}
		defaultStyle = transform(defaultStyle)
	}
	ui, err := tui.NewTUI(newScreen, defaultStyle)
	if err != nil {
		return err
	}
	defer func() {
		// If another goroutine panicked this wouldn't run so we'd be left with a garbled screen.
		// The alternative would be to defer a call to recover for every goroutine that we launch
		// in order to have them return an error in case of a panic.
		ui.Finish()
	}()

	controllerConf := ControllerConfiguration{
		TableConfiguration: tableConfig,
		GitStyle:           tableConfig.NodeStyle.(providers.StepStyle).GitStyle,
	}

	controller, err := NewController(&ui, controllerConf, ref, cacheDB, defaultStatus, manualPage)
	if err != nil {
		return err
	}

	return controller.Run(ctx, repo)
}

