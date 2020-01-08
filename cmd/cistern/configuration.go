package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell"
	"github.com/nbedos/cistern/providers"
	"github.com/nbedos/cistern/tui"
	"github.com/pelletier/go-toml"
)

const ConfDir = "cistern"
const ConfFilename = "cistern.toml"

// Configuration file format
// Warning: go-toml ignores default values on fields of nested structs
// See https://github.com/pelletier/go-toml/issues/274
type Configuration struct {
	Providers providers.Configuration `toml:"providers"`
	Location  string                  `toml:"location" default:"Local"`
	Columns   []string                `toml:"columns"`
	Sort      string                  `toml:"sort"`
	Depth     int                     `toml:"depth" default:"2"`
	Style     struct {
		Theme   string                        `toml:"theme"`
		Default *tui.StyleTransformDefinition `toml:"default"`
		Table   struct {
			Separator  string                        `toml:"separator"`
			Ascending  string                        `toml:"ascending"`
			Descending string                        `toml:"descending"`
			Header     *tui.StyleTransformDefinition `toml:"header"`
			Cursor     *tui.StyleTransformDefinition `toml:"cursor"`
			Provider   *tui.StyleTransformDefinition `toml:"provider"`
			Status     struct {
				Canceled *tui.StyleTransformDefinition `toml:"canceled"`
				Failed   *tui.StyleTransformDefinition `toml:"failed"`
				Manual   *tui.StyleTransformDefinition `toml:"manual"`
				Passed   *tui.StyleTransformDefinition `toml:"passed"`
				Pending  *tui.StyleTransformDefinition `toml:"pending"`
				Running  *tui.StyleTransformDefinition `toml:"running"`
				Skipped  *tui.StyleTransformDefinition `toml:"skipped"`
			} `toml:"status"`
		} `toml:"table"`
		Git struct {
			SHA    *tui.StyleTransformDefinition `toml:"sha"`
			Branch *tui.StyleTransformDefinition `toml:"branch"`
			Tag    *tui.StyleTransformDefinition `toml:"tag"`
			Head   *tui.StyleTransformDefinition `toml:"head"`
		} `toml:"git"`
	} `toml:"style"`
	Man string
}

var monochromeTableConfiguration = tui.TableConfiguration{
	Cursor: func(s tcell.Style) tcell.Style { return s.Reverse(true) },
	Header: func(s tcell.Style) tcell.Style { return s.Reverse(true).Bold(true) },
	NodeStyle: providers.StepStyle{
		GitStyle: providers.GitStyle{
			SHA:    nil,
			Head:   nil,
			Branch: nil,
			Tag:    nil,
		},
		Provider: func(s tcell.Style) tcell.Style { return s.Bold(true) },
		Status: struct {
			Failed   tui.StyleTransform
			Canceled tui.StyleTransform
			Passed   tui.StyleTransform
			Running  tui.StyleTransform
			Pending  tui.StyleTransform
			Skipped  tui.StyleTransform
			Manual   tui.StyleTransform
		}{},
	},
}

var defaultTableConfiguration = tui.TableConfiguration{
	Cursor: func(s tcell.Style) tcell.Style { return s.Background(tcell.ColorSilver).Foreground(tcell.ColorBlack).Bold(false).Underline(false).Blink(false) },
	Header: func(s tcell.Style) tcell.Style { return s.Bold(true).Reverse(true) },
	NodeStyle: providers.StepStyle{
		GitStyle: providers.GitStyle{
			SHA:    func(s tcell.Style) tcell.Style { return s.Foreground(tcell.ColorOlive) },
			Head:   func(s tcell.Style) tcell.Style { return s.Foreground(tcell.ColorAqua) },
			Branch: func(s tcell.Style) tcell.Style { return s.Foreground(tcell.ColorTeal).Bold(false) },
			Tag:    func(s tcell.Style) tcell.Style { return s.Foreground(tcell.ColorYellow).Bold(false) },
		},
		Provider: func(s tcell.Style) tcell.Style { return s.Bold(true) },
		Status: struct {
			Failed   tui.StyleTransform
			Canceled tui.StyleTransform
			Passed   tui.StyleTransform
			Running  tui.StyleTransform
			Pending  tui.StyleTransform
			Skipped  tui.StyleTransform
			Manual   tui.StyleTransform
		}{
			Failed:   func(s tcell.Style) tcell.Style { return s.Foreground(tcell.ColorMaroon).Bold(false) },
			Canceled: func(s tcell.Style) tcell.Style { return s.Foreground(tcell.ColorMaroon).Bold(false) },
			Passed:   func(s tcell.Style) tcell.Style { return s.Foreground(tcell.ColorGreen).Bold(false) },
			Running:  func(s tcell.Style) tcell.Style { return s.Foreground(tcell.ColorOlive).Bold(false) },
			Pending:  func(s tcell.Style) tcell.Style { return s.Foreground(tcell.ColorGray).Bold(false) },
			Skipped:  func(s tcell.Style) tcell.Style { return s.Foreground(tcell.ColorGray).Bold(false) },
			Manual:   func(s tcell.Style) tcell.Style { return s.Foreground(tcell.ColorGray).Bold(false) },
		},
	},
}

const maxWidth = 999

var defaultTableColumns = map[tui.ColumnID]tui.Column{
	providers.ColumnRef: {
		Header:    "REF",
		Position:  1,
		MaxWidth:  maxWidth,
		Alignment: tui.Left,
		Less: func(nodes []tui.TableNode, asc bool, v interface{}) func(i, j int) bool {
			return func(i, j int) bool {
				pi := nodes[i].(providers.Pipeline)
				pj := nodes[j].(providers.Pipeline)
				refi := pi.Values(v)[providers.ColumnRef]
				refj := pj.Values(v)[providers.ColumnRef]

				if asc {
					return refi.String() < refj.String() || (refi.String() == refj.String() && pi.Less(pj))
				} else {
					return refj.String() > refi.String() || (refi.String() == refj.String() && pi.Less(pj))
				}
			}
		},
	},
	providers.ColumnPipeline: {
		Position:  2,
		Header:    "PIPELINE",
		MaxWidth:  maxWidth,
		Alignment: tui.Right,
		Less: func(nodes []tui.TableNode, asc bool, v interface{}) func(i, j int) bool {
			return func(i, j int) bool {
				pi := nodes[i].(providers.Pipeline)
				pj := nodes[j].(providers.Pipeline)
				IDi := pi.Values(v)[providers.ColumnPipeline]
				IDj := pj.Values(v)[providers.ColumnPipeline]

				if asc {
					return IDi.String() < IDj.String() || (IDi.String() == IDj.String() && pi.Less(pj))
				} else {
					return IDi.String() > IDj.String() || (IDi.String() == IDj.String() && pi.Less(pj))
				}
			}
		},
	},
	providers.ColumnType: {
		Position:  3,
		Header:    "TYPE",
		MaxWidth:  maxWidth,
		Alignment: tui.Left,
		// The following sorting function has no interesting effect on the table since all Pipelines
		// have the same type. We just include it for consistency.
		Less: func(nodes []tui.TableNode, asc bool, v interface{}) func(i, j int) bool {
			return func(i, j int) bool {
				ni := nodes[i].(providers.Pipeline)
				nj := nodes[j].(providers.Pipeline)

				if asc {
					return ni.Type < nj.Type || (ni.Type == nj.Type && ni.Less(nj))
				} else {
					return ni.Type > nj.Type || (ni.Type == nj.Type && ni.Less(nj))
				}
			}
		},
	},
	providers.ColumnState: {
		Position:  4,
		Header:    "STATE",
		MaxWidth:  maxWidth,
		Alignment: tui.Left,
		Less: func(nodes []tui.TableNode, asc bool, v interface{}) func(i, j int) bool {
			return func(i, j int) bool {
				ni := nodes[i].(providers.Pipeline)
				nj := nodes[j].(providers.Pipeline)

				if asc {
					return ni.State < nj.State || (ni.State == nj.State && ni.Less(nj))
				} else {
					return ni.State > nj.State || (ni.State == nj.State && ni.Less(nj))
				}
			}
		},
	},
	providers.ColumnAllowedFailure: {
		Position:  5,
		Header:    "XFAIL",
		MaxWidth:  maxWidth,
		Alignment: tui.Left,
		Less: func(nodes []tui.TableNode, asc bool, v interface{}) func(i, j int) bool {
			return func(i, j int) bool {
				pi := nodes[i].(providers.Pipeline)
				pj := nodes[j].(providers.Pipeline)
				faili := pi.Values(v)[providers.ColumnAllowedFailure]
				failj := pj.Values(v)[providers.ColumnAllowedFailure]

				if asc {
					return faili.String() < failj.String() || (faili.String() == failj.String() && pi.Less(pj))
				} else {
					return faili.String() > failj.String() || (faili.String() == failj.String() && pi.Less(pj))
				}
			}
		},
	},
	providers.ColumnCreated: {
		Position:  6,
		Header:    "CREATED",
		MaxWidth:  maxWidth,
		Alignment: tui.Left,
		Less: func(nodes []tui.TableNode, asc bool, v interface{}) func(i, j int) bool {
			return func(i, j int) bool {
				ni := nodes[i].(providers.Pipeline)
				nj := nodes[j].(providers.Pipeline)

				if asc {
					return ni.CreatedAt.Before(nj.CreatedAt) || (ni.CreatedAt.Equal(nj.CreatedAt) && ni.Less(nj))
				} else {
					return ni.CreatedAt.After(nj.CreatedAt) || (ni.CreatedAt.Equal(nj.CreatedAt) && ni.Less(nj))
				}
			}
		},
	},
	providers.ColumnStarted: {
		Position:  7,
		Header:    "STARTED",
		MaxWidth:  maxWidth,
		Alignment: tui.Left,
		Less: func(nodes []tui.TableNode, asc bool, v interface{}) func(i, j int) bool {
			return func(i, j int) bool {
				pi := nodes[i].(providers.Pipeline)
				pj := nodes[j].(providers.Pipeline)
				ti := pi.StartedAt.Time
				tj := pj.StartedAt.Time

				// Assume that Null values are attributed to events that will occur in the
				// future, so give them a maximal value
				if !pi.StartedAt.Valid {
					ti = time.Unix(1<<62, 0)
				}

				if !pj.StartedAt.Valid {
					tj = time.Unix(1<<62, 0)
				}

				if asc {
					return ti.Before(tj) || (ti.Equal(tj) && pi.Less(pj))
				} else {
					return ti.After(tj) || (ti.Equal(tj) && pi.Less(pj))
				}
			}
		},
	},
	providers.ColumnFinished: {
		Position:  8,
		Header:    "FINISHED",
		MaxWidth:  maxWidth,
		Alignment: tui.Left,
		Less: func(nodes []tui.TableNode, asc bool, v interface{}) func(i, j int) bool {
			return func(i, j int) bool {
				pi := nodes[i].(providers.Pipeline)
				pj := nodes[j].(providers.Pipeline)
				ti := pi.FinishedAt.Time
				tj := pj.FinishedAt.Time

				if !pi.FinishedAt.Valid {
					ti = time.Unix(1<<62, 0)
				}

				if !pi.FinishedAt.Valid {
					tj = time.Unix(1<<62, 0)
				}

				if asc {
					return ti.Before(tj) || (ti.Equal(tj) && pi.Less(pj))
				} else {
					return ti.After(tj) || (ti.Equal(tj) && pi.Less(pj))
				}
			}
		},
	},
	providers.ColumnDuration: {
		Position:  10,
		Header:    "DURATION",
		MaxWidth:  maxWidth,
		Alignment: tui.Right,
		Less: func(nodes []tui.TableNode, asc bool, v interface{}) func(i, j int) bool {
			return func(i, j int) bool {
				ni := nodes[i].(providers.Pipeline)
				nj := nodes[j].(providers.Pipeline)

				if !ni.Duration.Valid {
					ni.Duration.Duration = 1<<63 - 1
				}
				if !nj.Duration.Valid {
					nj.Duration.Duration = 1<<63 - 1
				}

				if asc {
					return ni.Duration.Duration < nj.Duration.Duration || (ni.Duration.Duration == nj.Duration.Duration && ni.Less(nj))
				} else {
					return ni.Duration.Duration > nj.Duration.Duration || (ni.Duration.Duration == nj.Duration.Duration && ni.Less(nj))
				}
			}
		},
	},
	providers.ColumnName: {
		Position:   11,
		Header:     "NAME",
		MaxWidth:   maxWidth,
		Alignment:  tui.Left,
		TreePrefix: true,
		Less: func(nodes []tui.TableNode, asc bool, v interface{}) func(i, j int) bool {
			return func(i, j int) bool {
				pi := nodes[i].(providers.Pipeline)
				pj := nodes[j].(providers.Pipeline)
				namei := pi.Values(v)[providers.ColumnName]
				namej := pj.Values(v)[providers.ColumnName]

				if asc {
					return namei.String() < namej.String() || (namei.String() == namej.String() && pi.Less(pj))
				} else {
					return namei.String() > namej.String() || (namei.String() == namej.String() && pi.Less(pj))
				}
			}
		},
	},
	providers.ColumnWebURL: {
		Position:  12,
		Header:    "URL",
		MaxWidth:  maxWidth,
		Alignment: tui.Left,
		Less: func(nodes []tui.TableNode, asc bool, v interface{}) func(i, j int) bool {
			return func(i, j int) bool {
				pi := nodes[i].(providers.Pipeline)
				pj := nodes[j].(providers.Pipeline)
				urli := pi.Values(v)[providers.ColumnWebURL]
				urlj := pj.Values(v)[providers.ColumnWebURL]

				if asc {
					return urli.String() < urlj.String() || (urli.String() == urlj.String() && pi.Less(pj))
				} else {
					return urli.String() > urlj.String() || (urli.String() == urlj.String() && pi.Less(pj))
				}
			}
		},
	},
}

func (c Configuration) TableConfig(allColumns map[tui.ColumnID]tui.Column) (tui.TableConfiguration, error) {
	var tconf tui.TableConfiguration

	switch c.Style.Theme {
	case "", "default":
		tconf = defaultTableConfiguration
	case "monochrome":
		tconf = monochromeTableConfiguration
	default:
		return tconf, fmt.Errorf("invalid theme: %q (expected \"default\" or \"monochrome\")", c.Style.Theme)
	}

	tconf.Sep = c.Style.Table.Separator
	if tconf.Sep == "" {
		tconf.Sep = "  "
	}

	var err error
	if c.Style.Table.Cursor != nil {
		tconf.Cursor, err = c.Style.Table.Cursor.Parse()
		if err != nil {
			return tconf, err
		}
	}

	if c.Style.Table.Header != nil {
		tconf.Header, err = c.Style.Table.Header.Parse()
		if err != nil {
			return tconf, err
		}
	}

	stepStyle := tconf.NodeStyle.(providers.StepStyle)

	stepStyle.GitStyle.Location, err = time.LoadLocation(c.Location)
	if err != nil {
		return tconf, err
	}

	transforms := map[*tui.StyleTransformDefinition]*tui.StyleTransform{
		c.Style.Git.SHA:               &stepStyle.GitStyle.SHA,
		c.Style.Git.Branch:            &stepStyle.GitStyle.Branch,
		c.Style.Git.Tag:               &stepStyle.GitStyle.Tag,
		c.Style.Git.Head:              &stepStyle.GitStyle.Head,
		c.Style.Table.Provider:        &stepStyle.Provider,
		c.Style.Table.Status.Failed:   &stepStyle.Status.Failed,
		c.Style.Table.Status.Canceled: &stepStyle.Status.Canceled,
		c.Style.Table.Status.Passed:   &stepStyle.Status.Passed,
		c.Style.Table.Status.Running:  &stepStyle.Status.Running,
		c.Style.Table.Status.Pending:  &stepStyle.Status.Pending,
		c.Style.Table.Status.Skipped:  &stepStyle.Status.Skipped,
		c.Style.Table.Status.Manual:   &stepStyle.Status.Manual,
	}

	for source, target := range transforms {
		if source != nil {
			if *target, err = source.Parse(); err != nil {
				return tconf, err
			}
		}
	}

	tconf.NodeStyle = stepStyle

	if len(c.Columns) == 0 {
		c.Columns = []string{"ref", "pipeline", "type", "state", "started", "duration", "name", "url"}
	}
	tconf.Columns = make(map[tui.ColumnID]tui.Column)
loop:
	for position, name := range c.Columns {
		for id, column := range allColumns {
			if strings.ToLower(column.Header) == strings.ToLower(name) {
				column.Position = position
				tconf.Columns[id] = column
				continue loop
			}
		}
		return tconf, fmt.Errorf("invalid column name: %q", name)
	}

	sort := c.Sort
	if sort == "" {
		sort = c.Columns[0]
	}
	asc := true
	if strings.HasPrefix(sort, "+") {
		sort = strings.TrimPrefix(c.Sort, "+")
	} else if strings.HasPrefix(c.Sort, "-") {
		sort = strings.TrimPrefix(c.Sort, "-")
		asc = false
	}

	tconf.Order.Valid = false
	for id, column := range tconf.Columns {
		if strings.ToLower(column.Header) == strings.ToLower(sort) {
			tconf.Order = tui.Order{
				Valid:     true,
				ID:        id,
				Ascending: asc,
			}
			break
		}
	}
	if !tconf.Order.Valid {
		return tconf, fmt.Errorf("invalid sorting column: %q", c.Sort)
	}

	tconf.DefaultDepth = c.Depth

	tconf.HeaderSuffixAscending = c.Style.Table.Ascending
	if tconf.HeaderSuffixAscending == "" {
		tconf.HeaderSuffixAscending = "▲"
	}
	tconf.HeaderSuffixDescending = c.Style.Table.Descending
	if tconf.HeaderSuffixDescending == "" {
		tconf.HeaderSuffixDescending = "▼"
	}

	return tconf, nil
}

const defaultConfiguration = `
[[providers.github]]

[[providers.gitlab]]

[[providers.travis]]
url = "org"
token = ""

[[providers.travis]]
url = "com"
token = ""

[[providers.appveyor]]

[[providers.circleci]]

[[providers.azure]]

`

var ErrMissingConf = errors.New("missing configuration file")

func ConfigFromPaths(paths ...string) (Configuration, error) {
	var c Configuration

	for _, p := range paths {
		c = Configuration{}
		bs, err := ioutil.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				// No config file at this location, try the next one
				continue
			}
			return c, err
		}
		tree, err := toml.LoadBytes(bs)
		if err != nil {
			return c, err
		}
		err = tree.Unmarshal(&c)
		return c, err
	}

	tree, err := toml.LoadBytes([]byte(defaultConfiguration))
	if err != nil {
		return c, err
	}
	if err := tree.Unmarshal(&c); err != nil {
		return c, err
	}

	return c, ErrMissingConf
}
