package cache

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
)

const maxStepIDs = 10

type taskKey struct {
	providerID string
	// We need an array instead of a slice so that taskKey is hashable
	stepIDs [maxStepIDs]utils.NullString
}

type task struct {
	key         taskKey
	ref         GitReference
	type_       string
	state       State
	name        string
	provider    string
	prefix      string
	createdAt   utils.NullTime
	startedAt   utils.NullTime
	finishedAt  utils.NullTime
	updatedAt   utils.NullTime
	duration    utils.NullDuration
	children    []*task
	traversable bool
	url         utils.NullString
}

func (t task) Diff(other task) string {
	options := cmp.AllowUnexported(taskKey{}, task{})
	return cmp.Diff(t, other, options)
}

func (t task) Traversable() bool {
	return t.traversable
}

func (t task) Children() []utils.TreeNode {
	children := make([]utils.TreeNode, len(t.children))
	for i := range t.children {
		children[i] = t.children[i]
	}
	return children
}

func (t task) Tabular(loc *time.Location) map[string]text.StyledString {
	const nullPlaceholder = "-"

	nullTimeToString := func(t utils.NullTime) text.StyledString {
		s := nullPlaceholder
		if t.Valid {
			s = t.Time.In(loc).Truncate(time.Second).Format("Jan 2 15:04")
		}
		return text.NewStyledString(s)
	}

	state := text.NewStyledString(string(t.state))
	switch t.state {
	case Failed, Canceled:
		state.Add(text.StatusFailed)
	case Passed:
		state.Add(text.StatusPassed)
	case Running:
		state.Add(text.StatusRunning)
	case Pending, Skipped, Manual:
		state.Add(text.StatusSkipped)
	}

	name := text.NewStyledString(t.prefix)
	if t.type_ == "P" && t.name == "" {
		name.Append(t.provider, text.Provider)
	} else {
		name.Append(t.name)
	}

	pipeline := ""
	if t.key.stepIDs[0].Valid {
		pipeline = t.key.stepIDs[0].String
		if _, err := strconv.Atoi(pipeline); err == nil {
			pipeline = "#" + pipeline
		}
	}

	refClass := text.GitBranch
	if t.ref.IsTag {
		refClass = text.GitTag
	}

	return map[string]text.StyledString{
		"REF":      text.NewStyledString(t.ref.Ref, refClass),
		"PIPELINE": text.NewStyledString(pipeline),
		"TYPE":     text.NewStyledString(t.type_),
		"STATE":    state,
		"NAME":     name,
		"CREATED":  nullTimeToString(t.createdAt),
		"STARTED":  nullTimeToString(t.startedAt),
		"FINISHED": nullTimeToString(t.finishedAt),
		"UPDATED":  nullTimeToString(t.updatedAt),
		"DURATION": text.NewStyledString(t.duration.String()),
	}
}

func (t task) Key() interface{} {
	return t.key
}

func (t task) URL() utils.NullString {
	return t.url
}

func (t *task) SetTraversable(traversable bool, recursive bool) {
	t.traversable = traversable
	if recursive {
		for _, child := range t.children {
			child.SetTraversable(traversable, recursive)
		}
	}
}

func (t *task) SetPrefix(s string) {
	t.prefix = s
}

func taskFromPipeline(p Pipeline, providerByID map[string]CIProvider) task {
	key := taskKey{
		providerID: p.providerID,
		stepIDs:    [maxStepIDs]utils.NullString{},
	}

	providerName := "unknown"
	if provider, exists := providerByID[p.providerID]; exists {
		providerName = provider.Name()
	}

	return taskFromStep(p.Step, p.GitReference, key, providerName)
}

func taskFromStep(s Step, ref GitReference, key taskKey, provider string) task {
	keySet := false
	for i, ID := range key.stepIDs {
		if !ID.Valid {
			key.stepIDs[i] = utils.NullString{
				String: s.ID,
				Valid:  true,
			}
			keySet = true
			break
		}
	}
	// TODO Get rid off this after changing task.Key() so that it returns a hashable value
	//  while still allowing taskKey.StepIDs to be a slice (non hashable) instead of an array
	//  (hashable, but requires special handling to avoid overflow)
	if !keySet {
		panic("exceeded maximum nesting depth for type task")
	}

	t := task{
		key:        key,
		ref:        ref,
		state:      s.State,
		name:       s.Name,
		provider:   provider,
		createdAt:  s.CreatedAt,
		startedAt:  s.StartedAt,
		finishedAt: s.FinishedAt,
		updatedAt: utils.NullTime{
			Time:  s.UpdatedAt,
			Valid: true,
		},
		duration: s.Duration,
		url:      s.WebURL,
	}

	switch s.Type {
	case StepPipeline:
		t.type_ = "P"
	case StepStage:
		t.type_ = "S"
	case StepJob:
		t.type_ = "J"
	case StepTask:
		t.type_ = "T"
	}

	for _, childStep := range s.Children {
		childTask := taskFromStep(childStep, ref, t.key, provider)
		t.children = append(t.children, &childTask)
	}

	return t
}

type BuildsByCommit struct {
	cache Cache
	ref   string
}

func (c Cache) BuildsOfRef(ref string) HierarchicalTabularDataSource {
	return BuildsByCommit{
		cache: c,
		ref:   ref,
	}
}

func (s BuildsByCommit) Headers() []string {
	return []string{"REF", "PIPELINE", "TYPE", "STATE", "CREATED", "DURATION", "NAME"}
}

func (s BuildsByCommit) Alignment() map[string]text.Alignment {
	return map[string]text.Alignment{
		"REF":      text.Left,
		"PIPELINE": text.Right,
		"TYPE":     text.Right,
		"STATE":    text.Left,
		"CREATED":  text.Left,
		"STARTED":  text.Left,
		"UPDATED":  text.Left,
		"DURATION": text.Right,
		"NAME":     text.Left,
	}
}

func (s BuildsByCommit) Rows() []HierarchicalTabularSourceRow {
	rows := make([]HierarchicalTabularSourceRow, 0)
	for _, p := range s.cache.PipelinesByRef(s.ref) {
		t := taskFromPipeline(p, s.cache.ciProvidersById)
		rows = append(rows, &t)
	}

	sort.Slice(rows, func(i, j int) bool {
		ri, rj := rows[i].(*task), rows[j].(*task)
		ti := utils.MinNullTime(
			ri.createdAt,
			ri.startedAt,
			ri.updatedAt,
			ri.finishedAt)

		tj := utils.MinNullTime(
			rj.createdAt,
			rj.startedAt,
			rj.updatedAt,
			rj.finishedAt)

		return ti.Time.Before(tj.Time)
	})

	return rows
}

var ErrNoLogHere = errors.New("no log is associated to this row")

func (s BuildsByCommit) WriteToDisk(ctx context.Context, key interface{}, dir string) (string, error) {
	// TODO Allow filtering for errored jobs
	stepKey, ok := key.(taskKey)
	if !ok {
		return "", fmt.Errorf("key conversion to taskKey failed: '%v'", key)
	}

	file, err := ioutil.TempFile(dir, "step_*.log")
	w := utils.NewANSIStripper(file)
	defer w.Close()
	if err != nil {
		return "", err
	}

	err = s.cache.WriteLog(ctx, stepKey, w)
	logPath := path.Join(dir, filepath.Base(file.Name()))

	return logPath, err
}
