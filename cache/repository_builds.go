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
	sha       string
	accountID string
	buildID   string
	stageID   int
	jobID     int
}

type buildRow struct {
	key         buildRowKey
	type_       string
	state       State
	ref         string
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

func (b buildRow) Tabular() map[string]text.StyledString {
	const nullPlaceholder = "-"

	nullTimeToString := func(t utils.NullTime) text.StyledString {
		s := nullPlaceholder
		if t.Valid {
			t := t.Time.Local().Truncate(time.Second)
			s = t.Format("Jan _2 15:04")
		}
		return text.NewStyledString(s)
	}

	state := text.NewStyledString(string(b.state))
	switch b.state {
	case Failed, Canceled:
		state.Add(text.StatusFailed)
	case Passed:
		state.Add(text.StatusPassed)
	case Pending, Running:
		state.Add(text.StatusRunning)
	}

	name := text.NewStyledString(b.prefix)
	if b.type_ == "P" {
		name.Append(b.provider, text.Provider)
		name.Append(fmt.Sprintf(" (%s)", b.name))
	} else {
		name.Append(b.name)
	}

	return map[string]text.StyledString{
		"COMMIT":   text.NewStyledString(string([]rune(b.key.sha)[:shaLength]), text.CommitSha),
		"REF":      text.NewStyledString(b.ref, text.GitRef),
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
			sha:       b.Commit.Sha,
			accountID: b.Repository.AccountID,
			buildID:   b.ID,
		},
		type_:      "P",
		state:      b.State,
		ref:        ref,
		createdAt:  b.CreatedAt,
		startedAt:  b.StartedAt,
		finishedAt: b.FinishedAt,
		updatedAt:  utils.NullTime{Time: b.UpdatedAt, Valid: true},
		url:        b.WebURL,
		duration:   b.Duration,
		provider:   b.Repository.AccountID,
	}

	// Prefix only numeric IDs with hash
	if _, err := strconv.Atoi(b.ID); err == nil {
		row.name = fmt.Sprintf("#%s", b.ID)
	} else {
		row.name = b.ID
	}

	jobIDs := make([]int, 0, len(b.Jobs))
	for ID := range b.Jobs {
		jobIDs = append(jobIDs, ID)
	}
	sort.Ints(jobIDs)
	for _, jobID := range jobIDs {
		child := buildRowFromJob(b.Repository.AccountID, b.Commit.Sha, ref, b.ID, 0, *b.Jobs[jobID])
		row.children = append(row.children, &child)
	}

	stageIDs := make([]int, 0, len(b.Stages))
	for stageID := range b.Stages {
		stageIDs = append(stageIDs, stageID)
	}
	sort.Ints(stageIDs)
	for _, stageID := range stageIDs {
		child := buildRowFromStage(b.Repository.AccountID, b.Commit.Sha, ref, b.ID, b.WebURL, *b.Stages[stageID])
		row.children = append(row.children, &child)
	}

	return row
}

func buildRowFromStage(accountID string, sha string, ref string, buildID string, webURL string, s Stage) buildRow {
	row := buildRow{
		key: buildRowKey{
			sha:       sha,
			accountID: accountID,
			buildID:   buildID,
			stageID:   s.ID,
		},
		type_:    "S",
		state:    s.State,
		ref:      ref,
		name:     s.Name,
		url:      webURL,
		provider: accountID,
	}

	jobIDs := make([]int, 0, len(s.Jobs))
	// We aggregate jobs by name and only keep the most recent to weed out previous runs of the job.
	// This is mainly for GitLab which keeps jobs after they are restarted.
	jobByName := make(map[string]*Job, len(s.Jobs))
	for ID, job := range s.Jobs {
		jobIDs = append(jobIDs, ID)
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

	if row.startedAt.Valid && row.finishedAt.Valid {
		row.duration = utils.NullDuration{
			Valid:    true,
			Duration: row.finishedAt.Time.Sub(row.startedAt.Time),
		}
	}

	sort.Ints(jobIDs)
	for _, id := range jobIDs {
		child := buildRowFromJob(accountID, sha, ref, buildID, s.ID, *s.Jobs[id])
		row.children = append(row.children, &child)
	}

	return row
}

func buildRowFromJob(accountID string, sha string, ref string, buildID string, stageID int, j Job) buildRow {
	return buildRow{
		key: buildRowKey{
			sha:       sha,
			accountID: accountID,
			buildID:   buildID,
			stageID:   stageID,
			jobID:     j.ID,
		},
		type_:      "J",
		state:      j.State,
		name:       fmt.Sprintf("%s (#%d)", j.Name, j.ID),
		ref:        ref,
		createdAt:  j.CreatedAt,
		startedAt:  j.StartedAt,
		finishedAt: j.FinishedAt,
		updatedAt:  utils.MaxNullTime(j.FinishedAt, j.StartedAt, j.CreatedAt),
		url:        j.WebURL,
		duration:   j.Duration,
		provider:   accountID,
	}
}

func commitRowFromBuilds(builds []Build) buildRow {
	if len(builds) == 0 {
		return buildRow{}
	}

	messageLines := strings.SplitN(builds[0].Commit.Message, "\n", 2)
	row := buildRow{
		key: buildRowKey{
			sha: builds[0].Commit.Sha,
		},
		type_:       "C",
		ref:         ref(builds[0].Ref, builds[0].IsTag),
		name:        messageLines[0],
		children:    make([]*buildRow, 0, len(builds)),
		traversable: false,
		provider:    "",
	}

	latestBuildByProvider := make(map[string]Build)
	for _, build := range builds {
		child := buildRowFromBuild(build)
		row.children = append(row.children, &child)

		latestBuild, exists := latestBuildByProvider[build.Repository.AccountID]
		if !exists || latestBuild.StartedAt.Valid && build.StartedAt.Valid && latestBuild.StartedAt.Time.Before(build.StartedAt.Time) {
			latestBuildByProvider[build.Repository.AccountID] = build
		}
	}

	latestBuilds := make([]Statuser, 0, len(latestBuildByProvider))
	for _, build := range latestBuildByProvider {
		latestBuilds = append(latestBuilds, build)
		row.createdAt = utils.MinNullTime(row.createdAt, build.CreatedAt)
		row.startedAt = utils.MinNullTime(row.startedAt, build.StartedAt)
		row.finishedAt = utils.MaxNullTime(row.finishedAt, build.FinishedAt)
		if !row.updatedAt.Valid || row.updatedAt.Time.Before(build.UpdatedAt) {
			row.updatedAt.Time = build.UpdatedAt
			row.updatedAt.Valid = true
		}
		if !row.duration.Valid || (build.Duration.Valid && build.Duration.Duration > row.duration.Duration) {
			row.duration = build.Duration
		}
	}

	row.state = AggregateStatuses(latestBuilds)

	sort.Slice(row.children, func(i, j int) bool {
		ti := utils.MinNullTime(
			row.children[i].createdAt,
			row.children[i].startedAt,
			row.children[i].updatedAt,
			row.children[i].finishedAt)

		tj := utils.MinNullTime(
			row.children[j].createdAt,
			row.children[j].startedAt,
			row.children[j].updatedAt,
			row.children[j].finishedAt)

		return ti.Time.After(tj.Time) || (ti == tj && row.children[i].name > row.children[j].name)
	})

	return row
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
	return []string{"REF", "COMMIT", "TYPE", "STATE", "CREATED", "DURATION", "NAME"}
}

func (s BuildsByCommit) Alignment() map[string]text.Alignment {
	return map[string]text.Alignment{
		"REF":      text.Left,
		"COMMIT":   text.Left,
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
	type Ref struct {
		sha   string
		ref   string
		isTag bool
	}
	buildsPerRef := make(map[Ref][]Build)
	for _, build := range s.cache.Builds() {
		ref := Ref{
			sha:   build.Commit.Sha,
			ref:   build.Ref,
			isTag: build.IsTag,
		}
		buildsPerRef[ref] = append(buildsPerRef[ref], build)
	}

	rows := make([]HierarchicalTabularSourceRow, 0, len(buildsPerRef))
	for _, builds := range buildsPerRef {
		row := commitRowFromBuilds(builds)
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

		return ti.Time.After(tj.Time)
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

	if buildKey.jobID == 0 {
		return "", ErrNoLogHere
	}

	accountID := buildKey.accountID
	buildID := buildKey.buildID
	stageID := buildKey.stageID
	jobID := buildKey.jobID

	pattern := fmt.Sprintf("job_%d_*.log", jobID)
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
