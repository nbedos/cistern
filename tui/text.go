package tui

import (
	"bytes"
	"strings"

	"github.com/gdamore/tcell"
	"github.com/mattn/go-runewidth"
)

type Class int

const (
	DefaultClass Class = iota
	TableHeader
	ActiveRow
	GitSha
	GitTag
	GitBranch
	GitHead
	StatusPassed
	StatusRunning
	StatusFailed
	StatusSkipped
	Provider
)

type elementaryString struct {
	Content string
	Classes []Class
}

type StyledString struct {
	components []elementaryString
}

func (s StyledString) String() string {
	buf := bytes.Buffer{}
	for _, c := range s.components {
		buf.WriteString(c.Content)
	}
	return buf.String()
}

func (s *StyledString) Add(c Class) {
	for i := range s.components {
		s.components[i].Classes = append(s.components[i].Classes, c)
	}
}

func (s *StyledString) Append(content string, classes ...Class) {
	s.components = append(s.components, elementaryString{
		Content: content,
		Classes: classes,
	})
}

func (s *StyledString) AppendString(other StyledString) {
	s.components = append(s.components, other.components...)
}

func (s StyledString) Length() int {
	l := 0
	for _, c := range s.components {
		l += runewidth.StringWidth(c.Content)
	}
	return l
}

func (s *StyledString) Truncate(width int) {
	for i, c := range s.components {
		runes := []rune(c.Content)
		for j, r := range runes {
			width -= runewidth.RuneWidth(r)
			if width < 0 {
				s.components[i].Content = string(runes[:j])
				s.components = s.components[:i+1]
				return
			}
		}
	}
}

func (s *StyledString) TruncateLeft(width int) {
	for i := len(s.components) - 1; i >= 0; i-- {
		c := s.components[i]
		runes := []rune(c.Content)
		for j := len(runes) - 1; j >= 0; j-- {
			r := runes[j]
			width -= runewidth.RuneWidth(r)
			if width < 0 {
				s.components[i].Content = string(runes[j+1:])
				s.components = s.components[i:]
				return
			}
		}
	}
}

func Join(ss []StyledString, sep StyledString) StyledString {
	joined := StyledString{
		components: make([]elementaryString, 0),
	}
	for i, s := range ss {
		for _, c := range s.components {
			joined.components = append(joined.components, c)
		}

		if i < len(ss)-1 {
			for _, c := range sep.components {
				joined.components = append(joined.components, c)
			}
		}
	}

	return joined
}

func (s *StyledString) Fit(alignment Alignment, width int) {
	if s.Length() > width {
		switch alignment {
		case Left:
			s.Truncate(width)
		case Right:
			s.TruncateLeft(width)
		}
		// Truncation may leave us with a string slightly shorter than 'width' characters
		// so do not return yet and continue with padding
		// (for example truncating "abcX" to a length of 4 where X is a rune with a width > 1
		// would return "abc" which must be padded to either "abc " or " abc")
	}

	if paddingWidth := width - s.Length(); paddingWidth > 0 {
		if len(s.components) > 0 {
			padding := strings.Repeat(" ", paddingWidth)
			switch alignment {
			case Left:
				c := &s.components[len(s.components)-1].Content
				*c = *c + padding
			case Right:
				c := &s.components[0].Content
				*c = padding + *c
			}
		}
	}
}

func (s StyledString) Contains(value string) bool {
	b := bytes.NewBufferString("")
	for _, c := range s.components {
		b.WriteString(c.Content)
	}
	return strings.Contains(b.String(), value)
}

func NewStyledString(content string, classes ...Class) StyledString {
	return StyledString{
		components: []elementaryString{
			{
				Content: content,
				Classes: classes,
			},
		},
	}
}

type StyleSheet = map[Class]func(s tcell.Style) tcell.Style

func (s StyledString) Draw(screen tcell.Screen, y int, style tcell.Style, styleSheet StyleSheet) {
	x := 0
	for _, component := range s.components {
		s := style
		for _, c := range component.Classes {
			f, exists := styleSheet[c]
			if exists && f != nil {
				s = f(s)
			}
		}

		for _, r := range component.Content {
			screen.SetContent(x, y, r, nil, s)
			x += runewidth.RuneWidth(r)
		}
	}
}

type Alignment int

const (
	Left Alignment = iota
	Right
)
