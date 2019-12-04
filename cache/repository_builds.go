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
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
)

var shaLength = 7

type buildRowKey struct {
	ref       string
	sha       string
	accountID string
	buildID   string
	stageID   int
	jobID     string
}

type buildRow struct {
	key         buildRowKey
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
	children    []*buildRow
	traversable bool
	url         string
}

func (b buildRow) Diff(other buildRow) string {
	options := cmp.AllowUnexported(buildRowKey{}, buildRow{})
	return cmp.Diff(b, other, options)
}

func (b buildRow) Traversable() bool {
	return b.traversable
}

func (b buildRow) Children() []utils.TreeNode {
	children := make([]utils.TreeNode, len(b.children))
	for i := range b.children {
		children[i] = b.children[i]
	}
	return children
}

func (b buildRow) Tabular(loc *time.Location) map[string]text.StyledString {
	const nullPlaceholder = "-"

	nullTimeToString := func(t utils.NullTime) text.StyledString {
		s := nullPlaceholder
		if t.Valid {
			s = t.Time.In(loc).Truncate(time.Second).Format("Jan 2 15:04")
		}
		return text.NewStyledString(s)
	}

	state := text.NewStyledString(string(b.state))
	switch b.state {
	case Failed, Canceled:
		state.Add(text.StatusFailed)
	case Passed:
		state.Add(text.StatusPassed)
	case Running:
		state.Add(text.StatusRunning)
	case Pending, Skipped, Manual:
		state.Add(text.StatusSkipped)
	}

	name := text.NewStyledString(b.prefix)
	if b.type_ == "P" {
		name.Append(b.provider, text.Provider)
	} else {
		name.Append(b.name)
	}

	pipeline := b.key.buildID
	if _, err := strconv.Atoi(b.key.buildID); err == nil {
		pipeline = "#" + pipeline
	}

	refClass := text.GitBranch
	if strings.HasPrefix(b.key.ref, "tag:") {
		refClass = text.GitTag
	}

	return map[string]text.StyledString{
		"REF":      text.NewStyledString(b.key.ref, refClass),
		"PIPELINE": text.NewStyledString(pipeline),
		"TYPE":     text.NewStyledString(b.type_),
		"STATE":    state,
		"NAME":     name,
		"CREATED":  nullTimeToString(b.createdAt),
		"STARTED":  nullTimeToString(b.startedAt),
		"FINISHED": nullTimeToString(b.finishedAt),
		"UPDATED":  nullTimeToString(b.updatedAt),
		"DURATION": text.NewStyledString(b.duration.String()),
	}
}

func (b buildRow) Key() interface{} {
	return b.key
}

func (b buildRow) URL() string {
	return b.url
}

func (b *buildRow) SetTraversable(traversable bool, recursive bool) {
	b.traversable = traversable
	if recursive {
		for _, child := range b.children {
			child.SetTraversable(traversable, recursive)
		}
	}
}

func (b *buildRow) SetPrefix(s string) {
	b.prefix = s
}

func ref(ref string, tag bool) string {
	if tag {
		return fmt.Sprintf("tag: %s", ref)
	}
	return ref
}

func buildRowFromBuild(b Build) buildRow {
	ref := ref(b.Ref, b.IsTag)
	row := buildRow{
		key: buildRowKey{
			ref:       ref,
			sha:       b.Commit.Sha,
			accountID: b.Repository.Provider.ID,
			buildID:   b.ID,
		},
		type_:      "P",
		state:      b.State,
		createdAt:  b.CreatedAt,
		startedAt:  b.StartedAt,
		finishedAt: b.FinishedAt,
		updatedAt:  utils.NullTime{Time: b.UpdatedAt, Valid: true},
		url:        b.WebURL,
		duration:   b.Duration,
		provider:   b.Repository.Provider.Name,
	}

	// Prefix only numeric IDs with hash
	if _, err := strconv.Atoi(b.ID); err == nil {
		row.name = fmt.Sprintf("#%s", b.ID)
	} else {
		row.name = b.ID
	}

	for _, job := range b.Jobs {
		child := buildRowFromJob(b.Repository.Provider, b.Commit.Sha, ref, b.ID, 0, *job)
		row.children = append(row.children, &child)
	}

	stageIDs := make([]int, 0, len(b.Stages))
	for stageID := range b.Stages {
		stageIDs = append(stageIDs, stageID)
	}
	sort.Ints(stageIDs)
	for _, stageID := range stageIDs {
		child := buildRowFromStage(b.Repository.Provider, b.Commit.Sha, ref, b.ID, b.WebURL, *b.Stages[stageID])
		row.children = append(row.children, &child)
	}

	return row
}

func buildRowFromStage(provider Provider, sha string, ref string, buildID string, webURL string, s Stage) buildRow {
	row := buildRow{
		key: buildRowKey{
			ref:       ref,
			sha:       sha,
			accountID: provider.ID,
			buildID:   buildID,
			stageID:   s.ID,
		},
		type_:    "S",
		state:    s.State,
		name:     s.Name,
		url:      webURL,
		provider: provider.Name,
	}

	// We aggregate jobs by name and only keep the most recent to weed out previous runs of the job.
	// This is mainly for GitLab which keeps jobs after they are restarted.
	jobByName := make(map[string]*Job, len(s.Jobs))
	for _, job := range s.Jobs {
		namedJob, exists := jobByName[job.Name]
		if !exists || job.CreatedAt.Valid && job.CreatedAt.Time.After(namedJob.CreatedAt.Time) {
			jobByName[job.Name] = job
		}
	}

	for _, job := range jobByName {
		row.createdAt = utils.MinNullTime(row.createdAt, job.CreatedAt)
		row.startedAt = utils.MinNullTime(row.startedAt, job.StartedAt)
		row.finishedAt = utils.MaxNullTime(row.finishedAt, job.FinishedAt)
		row.updatedAt = utils.MaxNullTime(row.updatedAt, job.FinishedAt, job.StartedAt, job.CreatedAt)
	}

	row.duration = utils.NullSub(row.finishedAt, row.startedAt)

	for _, job := range s.Jobs {
		child := buildRowFromJob(provider, sha, ref, buildID, s.ID, *job)
		row.children = append(row.children, &child)
	}

	return row
}

func buildRowFromJob(provider Provider, sha string, ref string, buildID string, stageID int, j Job) buildRow {
	name := j.Name
	if name == "" {
		name = j.ID
	}
	return buildRow{
		key: buildRowKey{
			ref:       ref,
			sha:       sha,
			accountID: provider.ID,
			buildID:   buildID,
			stageID:   stageID,
			jobID:     j.ID,
		},
		type_:      "J",
		state:      j.State,
		name:       name,
		createdAt:  j.CreatedAt,
		startedAt:  j.StartedAt,
		finishedAt: j.FinishedAt,
		updatedAt:  utils.MaxNullTime(j.FinishedAt, j.StartedAt, j.CreatedAt),
		url:        j.WebURL,
		duration:   j.Duration,
		provider:   provider.Name,
	}
}

type BuildsByCommit struct {
	cache Cache
}

func (c *Cache) BuildsByCommit() BuildsByCommit {
	return BuildsByCommit{
		cache: *c,
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
	for _, build := range s.cache.Builds() {
		row := buildRowFromBuild(build)
		rows = append(rows, &row)
	}

	sort.Slice(rows, func(i, j int) bool {
		ri, rj := rows[i].(*buildRow), rows[j].(*buildRow)
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
	buildKey, ok := key.(buildRowKey)
	if !ok {
		return "", fmt.Errorf("key conversion to buildRowKey failed: '%v'", key)
	}

	if buildKey.jobID == "" {
		return "", ErrNoLogHere
	}

	accountID := buildKey.accountID
	buildID := buildKey.buildID
	stageID := buildKey.stageID
	jobID := buildKey.jobID

	pattern := fmt.Sprintf("job_%s_*.log", jobID)
	file, err := ioutil.TempFile(dir, pattern)
	w := utils.NewANSIStripper(file)
	defer w.Close()
	if err != nil {
		return "", err
	}
	logPath := path.Join(dir, filepath.Base(file.Name()))

	err = s.cache.WriteLog(ctx, accountID, buildID, stageID, jobID, w)
	return logPath, err
}
