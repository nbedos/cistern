package tui

import (
	"sort"
	"strings"
	"sync"

	"github.com/gdamore/tcell"
	"github.com/mattn/go-runewidth"
	"github.com/nbedos/cistern/utils"
)

type Command struct {
	width   int
	height  int
	input   string
	focused bool
	prefix  string
	tooltip *completion
}

func NewCommand(width int, height int, prefix string) Command {
	c := Command{
		prefix: prefix,
	}
	c.tooltip = newCompletion(width, c.height-1, nil)
	c.Resize(width, height)
	c.setInput("")
	return c
}

func (c *Command) Resize(width int, height int) {
	c.width = utils.MaxInt(0, width)
	c.height = utils.MaxInt(0, height)
	tooltipWidth := utils.MaxInt(0, c.width-runewidth.StringWidth(c.prefix)+1)
	c.tooltip.Resize(tooltipWidth, c.height-1)
}

func (c *Command) Focus() {
	c.focused = true
	c.setInput("")
}

func (c Command) Draw(w Window) {
	x := runewidth.StringWidth(c.prefix) - 1
	subWindow := w.Window(x, 0, c.width-x, c.height-1)
	c.tooltip.Draw(subWindow)

	inputLine := StyledString{}
	if c.focused {
		inputLine.Append(c.prefix + c.input)
		// Append cursor
		inputLine.Append(" ", func(s tcell.Style) tcell.Style {
			return s.Reverse(true)
		})
	}
	w.Draw(0, c.height-1, inputLine)
}

func (c *Command) Process(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		c.setInput("")
		c.focused = false
	case tcell.KeyCtrlU:
		c.setInput("")
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		c.deleteLastRune()
	case tcell.KeyTab:
		c.Complete(false)
	case tcell.KeyBacktab:
		c.Complete(true)
	case tcell.KeyRune:
		c.setInput(string(append([]rune(c.input), ev.Rune())))
	case tcell.KeyDown, tcell.KeyCtrlN:
		c.scrollBy(1)
	case tcell.KeyUp, tcell.KeyCtrlP:
		c.scrollBy(-1)
	case tcell.KeyPgDn, tcell.KeyCtrlF:
		amount := utils.MaxInt(0, c.tooltip.pageSize()-1)
		c.scrollBy(amount)
	case tcell.KeyPgUp, tcell.KeyCtrlB:
		amount := -utils.MaxInt(0, c.tooltip.pageSize()-1)
		c.scrollBy(amount)
	}
}

func (c *Command) SetCompletions(suggestions Suggestions) {
	c.tooltip.setSuggestions(suggestions)
}

func (c *Command) deleteLastRune() {
	if len(c.input) > 0 {
		runes := []rune(c.input)
		c.setInput(string(runes[:len(runes)-1]))
	}
}

func (c Command) Input() string {
	return c.input
}

func (c *Command) setInput(s string) {
	c.input = s
	c.tooltip.scrollTo(c.input)
}

func (c *Command) Complete(reverse bool) {
	if c.tooltip.cursorIndex.Valid {
		candidate := c.tooltip.suggestions[c.tooltip.cursorIndex.Int]
		if c.input == candidate.Value {
			if reverse {
				c.scrollBy(-1)
			} else {
				c.scrollBy(1)
			}
			candidate = c.tooltip.suggestions[c.tooltip.cursorIndex.Int]
		}
		c.input = candidate.Value
	}
}

func (c *Command) scrollBy(amount int) {
	c.tooltip.scrollBy(amount)
}

type completion struct {
	mux         *sync.Mutex
	suggestions Suggestions
	width       int
	height      int
	pageIndex   nullInt
	cursorIndex nullInt
}

func newCompletion(width int, height int, suggestions Suggestions) *completion {
	c := completion{
		mux: &sync.Mutex{},
	}
	c.Resize(width, height)
	c.setSuggestions(suggestions)

	return &c
}

type Suggestion struct {
	Value        string
	DisplayValue StyledString
	DisplayInfo  StyledString
}

type Suggestions []Suggestion

func (a Suggestions) Len() int           { return len(a) }
func (a Suggestions) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a Suggestions) Less(i, j int) bool { return a[i].Value < a[j].Value }

func (c *completion) setSuggestions(suggestions Suggestions) {
	c.mux.Lock()
	defer c.mux.Unlock()

	c.suggestions = suggestions
	sort.Sort(c.suggestions)
	c.pageIndex = nullInt{Valid: len(c.suggestions) > 0}
	c.cursorIndex = c.pageIndex
}

func (c *completion) scrollTo(s string) {
	c.mux.Lock()
	defer c.mux.Unlock()
	n := sort.Search(len(c.suggestions), func(i int) bool {
		return c.suggestions[i].Value >= s
	})
	if n >= len(c.suggestions) || !strings.HasPrefix(c.suggestions[n].Value, s) {
		c.cursorIndex = nullInt{}
		return
	}

	c.pageIndex = nullInt{
		Valid: true,
		Int:   n,
	}
	c.cursorIndex = c.pageIndex

	c.pageIndex.Int = utils.Bounded(utils.MinInt(c.pageIndex.Int, len(c.suggestions)-c.pageSize()), 0, len(c.suggestions)-1)
}

func (c *completion) pageSize() int {
	return utils.MaxInt(0, c.height-2)
}

func (c *completion) scrollBy(amount int) {
	if !c.cursorIndex.Valid || !c.pageIndex.Valid {
		return
	}

	c.cursorIndex.Int = utils.Bounded(c.cursorIndex.Int+amount, 0, len(c.suggestions)-1)
	if c.cursorIndex.Int < c.pageIndex.Int {
		c.pageIndex.Int = c.cursorIndex.Int
	} else if delta := c.cursorIndex.Int - (c.pageIndex.Int + c.pageSize() - 1); delta > 0 {
		c.pageIndex.Int = utils.Bounded(c.pageIndex.Int+delta, 0, len(c.suggestions)-1)
	}

	c.pageIndex.Int = utils.Bounded(utils.MinInt(c.pageIndex.Int, len(c.suggestions)-c.pageSize()), 0, len(c.suggestions)-1)
}

func (c *completion) Resize(width int, height int) {
	c.width = utils.MaxInt(0, width)
	c.height = utils.MaxInt(0, height)
}

func (c *completion) Draw(w Window) {
	if !c.pageIndex.Valid {
		return
	}

	c.mux.Lock()
	defer c.mux.Unlock()

	width := 0
	suggestions := make([]StyledString, 0)

	if c.cursorIndex.Valid {
		valueWidth, infoWidth := 0, 0
		for i := c.pageIndex.Int; i < len(c.suggestions) && i-c.pageIndex.Int+1 < c.pageSize(); i++ {
			valueWidth = utils.MaxInt(valueWidth, c.suggestions[i].DisplayValue.Length())
			infoWidth = utils.MaxInt(infoWidth, c.suggestions[i].DisplayInfo.Length()+1)
		}

		sep := "  "
		valueWidth = utils.MaxInt(valueWidth, 20)
		infoWidth = utils.MaxInt(infoWidth, 25)
		width = valueWidth + runewidth.StringWidth(sep) + infoWidth

		for i := c.pageIndex.Int; i < len(c.suggestions) && len(suggestions) < c.pageSize(); i++ {
			suggestion := StyledString{}
			suggestion.AppendString(c.suggestions[i].DisplayValue)
			suggestion.Fit(Left, valueWidth)
			suggestion.Append(sep)
			suggestion.AppendString(c.suggestions[i].DisplayInfo)
			suggestion.Append(" ")
			suggestion.Fit(Left, width)
			if i == c.cursorIndex.Int {
				suggestion.Apply(func(s tcell.Style) tcell.Style {
					return s.Reverse(true).Foreground(tcell.ColorDefault).Background(tcell.ColorBlack)
				})
			}
			suggestions = append(suggestions, suggestion)
		}
	} else {
		suggestion := NewStyledString("<no match found>")
		width = suggestion.Length()
		suggestions = append(suggestions, suggestion)
	}

	innerWidth := utils.MaxInt(0, utils.MinInt(width, c.width-3))
	lines := []StyledString{NewStyledString("┌" + strings.Repeat("─", innerWidth) + "┐")}
	for _, s := range suggestions {
		s.Fit(Left, innerWidth)
		line := NewStyledString("│")
		line.AppendString(s)
		line.Append("│")
		lines = append(lines, line)
	}
	lines = append(lines, NewStyledString("└"+strings.Repeat("─", innerWidth)+"┘"))

	if len(lines) > 2 {
		for i, line := range lines {
			w.Draw(0, c.height-len(lines)+i, line)
		}
	}
}
