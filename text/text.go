package text

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
	GitRef
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

func (s StyledString) Length() int {
	l := 0
	for _, c := range s.components {
		l += runewidth.StringWidth(c.Content)
	}
	return l
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

func (s *StyledString) Align(alignment Alignment, width int) {
	if paddingWidth := width - s.Length(); paddingWidth > 0 && len(s.components) > 0 {
		switch padding := strings.Repeat(" ", paddingWidth); alignment {
		case Left:
			c := &s.components[len(s.components)-1].Content
			*c = *c + padding
		case Right:
			c := &s.components[0].Content
			*c = padding + *c
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

type LocalizedStyledString struct {
	X int
	Y int
	S StyledString
}

func Draw(texts []LocalizedStyledString, screen tcell.Screen, style tcell.Style, styleSheet StyleSheet) {
	for _, t := range texts {
		t.Draw(screen, style, styleSheet)
	}
}

type StyleSheet = map[Class]func(s tcell.Style) tcell.Style

func (t LocalizedStyledString) Draw(screen tcell.Screen, style tcell.Style, styleSheet StyleSheet) {
	x, y := t.X, t.Y
	for _, component := range t.S.components {
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
