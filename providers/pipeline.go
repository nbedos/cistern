package providers

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/cistern/tui"
	"github.com/nbedos/cistern/utils"
)

type State string

const (
	Unknown  State = ""
	Pending  State = "pending"
	Running  State = "running"
	Passed   State = "passed"
	Failed   State = "failed"
	Canceled State = "canceled"
	Manual   State = "manual"
	Skipped  State = "skipped"
)

func (s State) IsActive() bool {
	return s == Pending || s == Running
}

var statePrecedence = map[State]int{
	Unknown:  80,
	Running:  70,
	Pending:  60,
	Canceled: 50,
	Failed:   40,
	Passed:   30,
	Skipped:  20,
	Manual:   10,
}

func (s State) merge(other State) State {
	if statePrecedence[s] > statePrecedence[other] {
		return s
	}
	return other
}

func Aggregate(steps []Step) Step {
	switch len(steps) {
	case 0:
		return Step{}
	case 1:
		return steps[0]
	}

	first, last := steps[0], Aggregate(steps[1:])
	for _, p := range []*Step{&first, &last} {
		if p.AllowFailure && (p.State == Canceled || p.State == Failed) {
			p.State = Passed
		}
	}

	s := Step{
		State:      first.State.merge(last.State),
		StartedAt:  utils.MinNullTime(first.StartedAt, last.StartedAt),
		FinishedAt: utils.MaxNullTime(first.FinishedAt, last.FinishedAt),
		Children:   steps,
	}

	s.CreatedAt = first.CreatedAt
	if last.CreatedAt.Before(s.CreatedAt) {
		s.CreatedAt = last.CreatedAt
	}
	s.Duration = utils.NullSub(s.FinishedAt, s.StartedAt)
	s.UpdatedAt = first.UpdatedAt
	if last.UpdatedAt.After(s.UpdatedAt) {
		s.UpdatedAt = last.UpdatedAt
	}

	return s
}

type Provider struct {
	ID   string
	Name string
}

type Ref struct {
	Name string
	Commit
}

type Commit struct {
	Sha      string
	Author   string
	Date     time.Time
	Message  string
	Branches []string
	Tags     []string
	Head     string
	Statuses []string
}

type GitStyle struct {
	Location *time.Location
	SHA      tui.StyleTransform
	Head     tui.StyleTransform
	Branch   tui.StyleTransform
	Tag      tui.StyleTransform
}

func (c Commit) StyledStrings(conf GitStyle) []tui.StyledString {
	var title tui.StyledString
	commit := tui.NewStyledString(fmt.Sprintf("commit %s", c.Sha), conf.SHA)
	if len(c.Branches) > 0 || len(c.Tags) > 0 {
		refs := make([]tui.StyledString, 0, len(c.Branches)+len(c.Tags))
		for _, tag := range c.Tags {
			refs = append(refs, tui.NewStyledString(fmt.Sprintf("tag: %s", tag), conf.Tag))
		}
		for _, branch := range c.Branches {
			if branch == c.Head {
				var s tui.StyledString
				s.Append("HEAD -> ", conf.Head)
				s.Append(branch, conf.Branch)
				refs = append([]tui.StyledString{s}, refs...)
			} else {
				refs = append(refs, tui.NewStyledString(branch, conf.Branch))
			}
		}

		title = tui.Join([]tui.StyledString{
			commit,
			tui.NewStyledString(" (", conf.SHA),
			tui.Join(refs, tui.NewStyledString(", ", conf.SHA)),
			tui.NewStyledString(")", conf.SHA),
		}, tui.NewStyledString(""))
	} else {
		title = commit
	}

	texts := []tui.StyledString{
		title,
		tui.NewStyledString(fmt.Sprintf("Author: %s", c.Author)),
		tui.NewStyledString(fmt.Sprintf("Date: %s", c.Date.Truncate(time.Second).In(conf.Location).String())),
		tui.NewStyledString(""),
	}
	for _, line := range strings.Split(c.Message, "\n") {
		texts = append(texts, tui.NewStyledString("    "+line))
		break
	}

	return texts
}

type StepType int

const (
	StepPipeline = iota
	StepStage
	StepJob
	StepTask
)

type Log struct {
	Key     string
	Content utils.NullString
}

type Step struct {
	ID           string
	Name         string
	Type         StepType
	State        State
	AllowFailure bool
	CreatedAt    time.Time
	StartedAt    utils.NullTime
	FinishedAt   utils.NullTime
	UpdatedAt    time.Time
	Duration     utils.NullDuration
	WebURL       utils.NullString
	Log          Log
	Children     []Step
}

func (s Step) Diff(other Step) string {
	return cmp.Diff(s, other)
}

func (s Step) Map(f func(Step) Step) Step {
	s = f(s)

	for i, child := range s.Children {
		s.Children[i] = child.Map(f)
	}

	return s
}

// Return step identified by stepIDs
func (s Step) getStep(stepIDs []string) (Step, bool) {
	step := s
	for _, id := range stepIDs {
		exists := false
		for _, childStep := range step.Children {
			if childStep.ID == id {
				exists = true
				step = childStep
				break
			}
		}
		if !exists {
			return Step{}, false
		}
	}

	return step, true
}

type StepStatusChanges struct {
	Started [][]string
	Passed  [][]string
	Failed  [][]string
}

func (s Step) statusDiff(before Step, prefix []string) map[StepType]*StepStatusChanges {
	c := map[StepType]*StepStatusChanges{
		StepPipeline: {},
		StepStage:    {},
		StepJob:      {},
		StepTask:     {},
	}
	prefix = append(prefix, s.ID)
	if s.State != before.State {
		switch s.State {
		case Running:
			c[s.Type].Started = append(c[s.Type].Started, prefix)
		case Passed:
			c[s.Type].Passed = append(c[s.Type].Passed, prefix)
		case Failed, Canceled:
			if s.AllowFailure {
				c[s.Type].Passed = append(c[s.Type].Passed, prefix)
			} else {
				c[s.Type].Failed = append(c[s.Type].Failed, prefix)
			}
		}
	}

	for _, child := range s.Children {
		beforeChild, exists := before.getStep([]string{child.ID})
		if !exists {
			beforeChild = Step{}
		}
		for stepType, childChanges := range child.statusDiff(beforeChild, prefix) {
			c[stepType].Started = append(c[stepType].Started, childChanges.Started...)
			c[stepType].Passed = append(c[stepType].Passed, childChanges.Passed...)
			c[stepType].Failed = append(c[stepType].Failed, childChanges.Failed...)
		}
	}

	return c
}

const (
	ColumnRef tui.ColumnID = iota
	ColumnPipeline
	ColumnType
	ColumnState
	ColumnCreated
	ColumnStarted
	ColumnFinished
	ColumnUpdated
	ColumnDuration
	ColumnName
	ColumnWebURL
	ColumnAllowedFailure
)

func (s Step) NodeID() interface{} {
	return s.ID
}

func (s Step) NodeChildren() []tui.TableNode {
	children := make([]tui.TableNode, 0)
	for _, child := range s.Children {
		children = append(children, child)
	}

	return children
}

func (s Step) InheritedValues() []tui.ColumnID {
	return []tui.ColumnID{ColumnRef, ColumnPipeline}
}

type StepStyle struct {
	GitStyle
	Provider tui.StyleTransform
	Status   struct {
		Failed   tui.StyleTransform
		Canceled tui.StyleTransform
		Passed   tui.StyleTransform
		Running  tui.StyleTransform
		Pending  tui.StyleTransform
		Skipped  tui.StyleTransform
		Manual   tui.StyleTransform
	}
}

func (s Step) Values(v interface{}) map[tui.ColumnID]tui.StyledString {
	conf := v.(StepStyle)

	timeToString := func(t time.Time) string {
		return t.In(conf.Location).Truncate(time.Second).Format("Jan 2 15:04")
	}

	nullTimeToString := func(t utils.NullTime) tui.StyledString {
		s := "-"
		if t.Valid {
			s = timeToString(t.Time)
		}
		return tui.NewStyledString(s)
	}

	var typeChar string
	switch s.Type {
	case StepPipeline:
		typeChar = "P"
	case StepStage:
		typeChar = "S"
	case StepJob:
		typeChar = "J"
	case StepTask:
		typeChar = "T"
	}

	state := tui.NewStyledString(string(s.State))
	switch s.State {
	case Failed:
		state.Apply(conf.Status.Failed)
	case Canceled:
		state.Apply(conf.Status.Canceled)
	case Passed:
		state.Apply(conf.Status.Passed)
	case Running:
		state.Apply(conf.Status.Running)
	case Pending:
		state.Apply(conf.Status.Pending)
	case Skipped:
		state.Apply(conf.Status.Skipped)
	case Manual:
		state.Apply(conf.Status.Manual)
	}

	webURL := "-"
	if s.WebURL.Valid {
		webURL = s.WebURL.String
	}

	allowedFailure := "no"
	if s.AllowFailure {
		allowedFailure = "yes"
	}
	return map[tui.ColumnID]tui.StyledString{
		ColumnType:           tui.NewStyledString(typeChar),
		ColumnState:          state,
		ColumnAllowedFailure: tui.NewStyledString(allowedFailure),
		ColumnCreated:        tui.NewStyledString(timeToString(s.CreatedAt)),
		ColumnStarted:        nullTimeToString(s.StartedAt),
		ColumnFinished:       nullTimeToString(s.FinishedAt),
		ColumnUpdated:        tui.NewStyledString(timeToString(s.UpdatedAt)),
		ColumnDuration:       tui.NewStyledString(s.Duration.String()),
		ColumnName:           tui.NewStyledString(s.Name),
		ColumnWebURL:         tui.NewStyledString(webURL),
	}
}

func (s Step) Compare(t tui.TableNode, id tui.ColumnID, i interface{}) int {
	other := t.(Step)
	switch id {
	case ColumnType, ColumnState, ColumnAllowedFailure, ColumnName, ColumnWebURL:
		lhs, rhs := s.Values(i)[id].String(), other.Values(i)[id].String()
		if lhs < rhs {
			return -1
		} else if lhs == rhs {
			return 0
		} else {
			return 1
		}

	case ColumnCreated:
		if s.CreatedAt.Before(other.CreatedAt) {
			return -1
		} else if s.CreatedAt.Equal(other.CreatedAt) {
			return 0
		} else {
			return 1
		}

	case ColumnStarted, ColumnFinished:
		var v, vOther utils.NullTime
		if id == ColumnStarted {
			v = s.StartedAt
			vOther = other.StartedAt
		} else {
			v = s.FinishedAt
			vOther = other.FinishedAt
		}

		// Assume that Null values are attributed to events that will occur in the
		// future, so give them a maximal value
		if !v.Valid {
			v.Time = time.Unix(1<<62, 0)
		}

		if !vOther.Valid {
			vOther.Time = time.Unix(1<<62, 0)
		}

		if v.Time.Before(vOther.Time) {
			return -1
		} else if v.Time.Equal(vOther.Time) {
			return 0
		} else {
			return 1
		}

	case ColumnDuration:
		v := s.Duration
		vOther := other.Duration

		if !v.Valid {
			v.Duration = 1<<63 - 1
		}
		if !vOther.Valid {
			vOther.Duration = 1<<63 - 1
		}

		if v.Duration < vOther.Duration {
			return -1
		} else if v.Duration == vOther.Duration {
			return 0
		} else {
			return 1
		}

	default:
		return 0
	}
}

type GitReference struct {
	SHA   string
	Ref   string
	IsTag bool
}

type Pipeline struct {
	Number       string
	providerID   string
	ProviderHost string
	ProviderName string
	GitReference
	Step
}

func (p Pipeline) Diff(other Pipeline) string {
	return cmp.Diff(p, other, cmp.AllowUnexported(Pipeline{}, Step{}))
}

type Pipelines []Pipeline

func (ps Pipelines) Diff(others Pipelines) string {
	return cmp.Diff(ps, others, cmp.AllowUnexported(Pipeline{}, Step{}))
}

type PipelineKey struct {
	ProviderHost string
	ID           string
}

func (p Pipeline) Key() PipelineKey {
	return PipelineKey{
		ProviderHost: p.ProviderHost,
		ID:           p.Step.ID,
	}
}

func (p Pipeline) NodeID() interface{} {
	return p.Key()
}

func (p Pipeline) InheritedValues() []tui.ColumnID {
	return nil
}

func (p Pipeline) Values(v interface{}) map[tui.ColumnID]tui.StyledString {
	conf := v.(StepStyle)

	values := p.Step.Values(conf)

	number := p.Number
	if number == "" {
		number = p.ID
	}
	if _, err := strconv.Atoi(number); err == nil {
		number = "#" + number
	}
	values[ColumnPipeline] = tui.NewStyledString(number)

	name := tui.NewStyledString(p.ProviderName, conf.Provider)
	if p.Name != "" {
		name.Append(fmt.Sprintf(": %s", p.Name))
	}
	values[ColumnName] = name

	if p.IsTag {
		values[ColumnRef] = tui.NewStyledString(p.Ref, conf.Tag)
	} else {
		values[ColumnRef] = tui.NewStyledString(p.Ref, conf.Branch)
	}

	return values
}

func (p Pipeline) Compare(other tui.TableNode, id tui.ColumnID, i interface{}) int {
	switch q := other.(Pipeline); id {
	case ColumnRef, ColumnPipeline, ColumnName:
		lhs, rhs := p.Values(i)[id].String(), other.Values(i)[id].String()
		if lhs < rhs {
			return -1
		} else if lhs == rhs {
			return 0
		} else {
			return 1
		}
	default:
		return p.Step.Compare(q.Step, id, i)
	}
}

type PipelineChanges struct {
	Valid bool
	PipelineKey
	Changes map[StepType]*StepStatusChanges
}

func (p Pipeline) StatusDiff(before Pipeline) PipelineChanges {
	return PipelineChanges{
		Valid:       true,
		PipelineKey: p.Key(),
		Changes:     p.Step.statusDiff(before.Step, nil),
	}
}
