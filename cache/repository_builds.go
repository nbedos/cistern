package cache

import (
	"context"
	"errors"
	"fmt"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
	"io/ioutil"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var shaLength = 7

var ErrNoMatchFound = errors.New("no match found")

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
	state       string
	ref         string
	name        string
	provider    string
	prefix      string
	createdAt   utils.NullTime
	startedAt   utils.NullTime
	finishedAt  utils.NullTime
	updatedAt   utils.NullTime
	duration    utils.NullDuration
	children    []buildRow
	traversable bool
	url         string
}

func (b buildRow) Traversable() bool {
	return b.traversable
}

func (b *buildRow) SetPrefix(prefix string) {
	b.prefix = prefix
}

func (b buildRow) Children() []utils.TreeNode {
	children := make([]utils.TreeNode, len(b.children))
	for i := range b.children {
		children[i] = &b.children[i]
	}
	return children
}

func (b buildRow) Tabular() map[string]text.StyledString {
	const nullPlaceholder = "-"

	nullTimeToString := func(t utils.NullTime) string {
		if t.Valid {
			t := t.Time.Local().Truncate(time.Second)
			return t.Format("Jan _2 15:04")
		}
		return nullPlaceholder
	}

	var state text.StyledString
	switch b.state {
	case "failed", "canceled":
		state = text.NewStyledString(b.state, text.StatusFailed)
	case "passed":
		state = text.NewStyledString(b.state, text.StatusPassed)
	case "pending", "running":
		state = text.NewStyledString(b.state, text.StatusRunning)
	default:
		state = text.NewStyledString(b.state)
	}

	var name text.StyledString
	switch b.type_ {
	case "P":
		name = text.NewStyledString(b.prefix)
		name.Append(b.provider, text.Provider)
		name.Append(fmt.Sprintf(" (%s)", b.name))
	default:
		prefix := b.prefix
		if prefix == "" {
			prefix = nullPlaceholder
		}
		name = text.NewStyledString(fmt.Sprintf("%s%s", prefix, b.name))
	}

	return map[string]text.StyledString{
		"COMMIT":   text.NewStyledString(string([]rune(b.key.sha)[:shaLength]), text.CommitSha),
		"REF":      text.NewStyledString(b.ref, text.GitRef),
		"TYPE":     text.NewStyledString(b.type_),
		"STATE":    state,
		"NAME":     name,
		"CREATED":  text.NewStyledString(nullTimeToString(b.createdAt)),
		"STARTED":  text.NewStyledString(nullTimeToString(b.startedAt)),
		"FINISHED": text.NewStyledString(nullTimeToString(b.finishedAt)),
		"UPDATED":  text.NewStyledString(nullTimeToString(b.updatedAt)),
		"DURATION": text.NewStyledString(b.duration.String()),
	}
}

func (b buildRow) Key() interface{} {
	return b.key
}

func (b buildRow) URL() string {
	return b.url
}

type RepositoryBuilds struct {
	cache         Cache
	repositoryURL string
	rows          []buildRow
	treeIndex     map[buildRowKey]*buildRow
	dfsTraversal  []*buildRow
	dfsIndex      map[buildRowKey]int
	dfsUpToDate   bool
}

func (c *Cache) NewRepositoryBuilds(repositoryURL string) RepositoryBuilds {
	return RepositoryBuilds{
		cache:         *c,
		repositoryURL: repositoryURL,
	}
}

func (s *RepositoryBuilds) SetTraversable(key interface{}, traversable bool, recursive bool) error {
	buildKey, ok := key.(buildRowKey)
	if !ok {
		return fmt.Errorf("expected key of concrete type %T but got %v", buildKey, key)
	}
	if row, exists := s.treeIndex[buildKey]; exists {
		row.traversable = traversable
		s.dfsUpToDate = false
		if recursive {
			for _, child := range utils.DepthFirstTraversal(row, true) {
				if child, ok := child.(*buildRow); ok {
					child.traversable = traversable
				}
			}
		}
	}

	return nil
}

func Ref(ref string, tag bool) string {
	if tag {
		return fmt.Sprintf("tag: %s", ref)
	}
	return ref
}

func buildRowFromBuild(b Build) buildRow {
	row := buildRow{
		key: buildRowKey{
			sha:       b.Commit.Sha,
			accountID: b.Repository.AccountID,
			buildID:   b.ID,
		},
		type_:      "P",
		state:      string(b.State),
		ref:        Ref(b.Ref, b.IsTag),
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
		row.children = append(row.children, buildRowFromJob(*b.Jobs[jobID]))
	}

	stageIDs := make([]int, 0, len(b.Stages))
	for stageID := range b.Stages {
		stageIDs = append(stageIDs, stageID)
	}
	sort.Ints(stageIDs)
	for _, stageID := range stageIDs {
		row.children = append(row.children, buildRowFromStage(*b.Stages[stageID]))
	}

	return row
}

func buildRowFromStage(s Stage) buildRow {
	row := buildRow{
		key: buildRowKey{
			sha:       s.Build.Commit.Sha,
			accountID: s.Build.Repository.AccountID,
			buildID:   s.Build.ID,
			stageID:   s.ID,
		},
		type_:    "S",
		state:    string(s.State),
		ref:      Ref(s.Build.Ref, s.Build.IsTag),
		name:     s.Name,
		url:      s.Build.WebURL,
		provider: s.Build.Repository.AccountID,
	}

	jobIDs := make([]int, 0, len(s.Jobs))
	// We agreggate jobs by name and only keep the most recent to weed out previous runs of the job.
	// This is mainly for GitLab which keeps jobs after they are restarted. Maybe we should add
	// date fields to Stage and have providers fill them instead of computing them here.
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
	for i := len(jobIDs) - 1; i >= 0; i-- {
		row.children = append(row.children, buildRowFromJob(*s.Jobs[jobIDs[i]]))
	}

	return row
}

func buildRowFromJob(j Job) buildRow {
	stageID := 0
	if j.Stage != nil {
		stageID = j.Stage.ID
	}
	return buildRow{
		key: buildRowKey{
			sha:       j.Build.Commit.Sha,
			accountID: j.Build.Repository.AccountID,
			buildID:   j.Build.ID,
			stageID:   stageID,
			jobID:     j.ID,
		},
		type_:      "J",
		state:      string(j.State),
		name:       fmt.Sprintf("%s (#%d)", j.Name, j.ID),
		ref:        Ref(j.Build.Ref, j.Build.IsTag),
		createdAt:  j.CreatedAt,
		startedAt:  j.StartedAt,
		finishedAt: j.FinishedAt,
		updatedAt:  utils.MaxNullTime(j.FinishedAt, j.StartedAt, j.CreatedAt),
		url:        j.WebURL,
		duration:   j.Duration,
		provider:   j.Build.Repository.AccountID,
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
		ref:         Ref(builds[0].Ref, builds[0].IsTag),
		name:        messageLines[0],
		children:    make([]buildRow, 0, len(builds)),
		traversable: false,
		provider:    "",
	}

	latestBuildByProvider := make(map[string]Build)
	for _, build := range builds {
		row.children = append(row.children, buildRowFromBuild(build))

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

	row.state = string(AggregateStatuses(latestBuilds))

	sort.Slice(row.children, func(i, j int) bool {
		ti := utils.MinNullTime(
			row.children[i].createdAt,
			row.children[i].startedAt,
			row.children[i].updatedAt,
			row.children[i].finishedAt)

		tj := utils.MinNullTime(
			row.children[i].createdAt,
			row.children[j].startedAt,
			row.children[j].updatedAt,
			row.children[j].finishedAt)

		return ti.Time.After(tj.Time) || (ti == tj && row.children[i].name > row.children[j].name)
	})

	return row
}

func (s *RepositoryBuilds) FetchRows() {
	// Save traversable state of current nodes
	traversables := make(map[buildRowKey]bool)
	for i := range s.rows {
		rowTraversal := utils.DepthFirstTraversal(&s.rows[i], true)
		for j := range rowTraversal {
			if row := rowTraversal[j].(*buildRow); row.traversable {
				traversables[row.key] = true
			}
		}
	}
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

	rows := make([]buildRow, 0, len(buildsPerRef))
	for _, builds := range buildsPerRef {
		rows = append(rows, commitRowFromBuilds(builds))
	}

	sort.Slice(rows, func(i, j int) bool {
		ti := utils.MinNullTime(
			rows[i].createdAt,
			rows[i].startedAt,
			rows[i].updatedAt,
			rows[i].finishedAt)

		tj := utils.MinNullTime(
			rows[i].createdAt,
			rows[j].startedAt,
			rows[j].updatedAt,
			rows[j].finishedAt)

		return ti.Time.After(tj.Time)
	})

	treeIndex := make(map[buildRowKey]*buildRow)
	for i := range rows {
		traversal := utils.DepthFirstTraversal(&rows[i], true)
		for j := range traversal {
			row := traversal[j].(*buildRow)
			treeIndex[row.key] = row
			// Restore traversable state of node
			if value, exists := traversables[row.key]; exists {
				row.traversable = value
			}
		}
	}

	s.rows = rows
	s.treeIndex = treeIndex
	s.dfsUpToDate = false
}

func (s *RepositoryBuilds) prefixAndIndex() {
	s.dfsTraversal = make([]*buildRow, 0)
	s.dfsIndex = make(map[buildRowKey]int)

	for i := range s.rows {
		row := &s.rows[i]
		utils.DepthFirstTraversalPrefixing(row)

		tmpRows := utils.DepthFirstTraversal(row, false)
		for j := range tmpRows {
			tmpRow := tmpRows[j].(*buildRow)
			s.dfsTraversal = append(s.dfsTraversal, tmpRow)
			s.dfsIndex[tmpRow.key] = len(s.dfsTraversal) - 1
		}
	}

	s.dfsUpToDate = true
}

func (s *RepositoryBuilds) NextMatch(top, bottom, active interface{}, search string, ascending bool) ([]TabularSourceRow, int, error) {
	activeKey, ok := active.(buildRowKey)
	if !ok {
		return nil, 0, fmt.Errorf("casting key %v to buildRowKey failed", active)
	}
	topKey, ok := top.(buildRowKey)
	if !ok {
		return nil, 0, fmt.Errorf("casting key %v to buildRowKey failed", top)
	}
	bottomKey, ok := bottom.(buildRowKey)
	if !ok {
		return nil, 0, fmt.Errorf("casting key %v to buildRowKey failed", bottom)
	}

	var next func(int) int
	var start int
	if ascending {
		start = utils.Modulo(s.dfsIndex[activeKey]+1, len(s.dfsTraversal))
		next = func(i int) int {
			return utils.Modulo(i+1, len(s.dfsTraversal))
		}
	} else {
		start = utils.Modulo(s.dfsIndex[activeKey]-1, len(s.dfsTraversal))
		next = func(i int) int {
			return utils.Modulo(i-1, len(s.dfsTraversal))
		}
	}

	for i := start; i != s.dfsIndex[activeKey]; i = next(i) {
		row := s.dfsTraversal[i]
		for _, styledString := range row.Tabular() {
			if styledString.Contains(search) {
				nbrRows := s.dfsIndex[bottomKey] - s.dfsIndex[topKey] + 1
				var maxIndex, minIndex int
				if i > s.dfsIndex[activeKey] {
					maxIndex = utils.MaxInt(s.dfsIndex[bottomKey], i)
					minIndex = utils.MaxInt(s.dfsIndex[topKey], maxIndex-nbrRows+1)
				} else {
					minIndex = utils.MinInt(s.dfsIndex[topKey], i)
					maxIndex = utils.MinInt(s.dfsIndex[bottomKey], minIndex+nbrRows-1)
				}

				return s.Select(row.key, i-minIndex, maxIndex-i)
			}
		}
	}

	return nil, 0, ErrNoMatchFound
}

func (s *RepositoryBuilds) SelectFirst(limit int) ([]TabularSourceRow, error) {
	if !s.dfsUpToDate {
		s.prefixAndIndex()
	}
	if len(s.dfsTraversal) == 0 {
		return nil, nil
	}

	rows, _, err := s.Select(s.dfsTraversal[0].Key(), 0, limit-1)
	return rows, err
}

func (s *RepositoryBuilds) SelectLast(limit int) ([]TabularSourceRow, error) {
	if !s.dfsUpToDate {
		s.prefixAndIndex()
	}
	if len(s.dfsTraversal) == 0 {
		return nil, nil
	}

	rows, _, err := s.Select(s.dfsTraversal[len(s.dfsTraversal)-1].Key(), limit-1, 0)
	return rows, err
}

func (s *RepositoryBuilds) Select(key interface{}, nbrBefore int, nbrAfter int) ([]TabularSourceRow, int, error) {
	buildKey, ok := key.(buildRowKey)
	if !ok {
		return nil, 0, errors.New("casting key to buildRowKey failed")
	}

	if !s.dfsUpToDate {
		s.prefixAndIndex()
	}

	if len(s.dfsTraversal) == 0 {
		return nil, 0, nil
	}

	// Also list parents as candidates since buildKey might refer to a row that is now hidden
	keys := []buildRowKey{
		buildKey,
		{
			sha:       buildKey.sha,
			accountID: buildKey.accountID,
			buildID:   buildKey.buildID,
			stageID:   buildKey.stageID,
		},
		{
			sha:       buildKey.sha,
			accountID: buildKey.accountID,
			buildID:   buildKey.buildID,
		},
	}

	var keyIndex int
	exists := false
	for _, key := range keys {
		if keyIndex, exists = s.dfsIndex[key]; exists {
			break
		}
	}

	if !exists {
		return nil, 0, fmt.Errorf("key '%v' not found", buildKey)
	}

	lower := utils.Bounded(keyIndex-nbrBefore, 0, len(s.dfsTraversal))
	upper := utils.Bounded(lower+nbrBefore+nbrAfter+1, 0, len(s.dfsTraversal))
	lower = utils.Bounded(upper-(nbrBefore+nbrAfter+1), 0, len(s.dfsTraversal))

	selectedRows := make([]TabularSourceRow, upper-lower)
	var index int
	for i, row := range s.dfsTraversal[lower:upper] {
		if row == s.dfsTraversal[keyIndex] {
			index = i
		}
		selectedRows[i] = *row
	}

	return selectedRows, index, nil
}

var ErrNoLogHere = errors.New("no log is associated to this row")

func (s RepositoryBuilds) WriteToDirectory(ctx context.Context, key interface{}, dir string) (string, Streamer, error) {
	// TODO Allow filtering for errored jobs
	buildKey, ok := key.(buildRowKey)
	if !ok {
		return "", nil, fmt.Errorf("key conversion to buildRowKey failed: '%v'", key)
	}

	row, exists := s.treeIndex[buildKey]
	if !exists {
		return "", nil, fmt.Errorf("no row associated to key '%v'", key)
	}

	if row.type_ != "J" {
		return "", nil, ErrNoLogHere
	}

	jobKey := JobKey{
		AccountID: row.key.accountID,
		BuildID:   row.key.buildID,
		StageID:   row.key.stageID,
		ID:        row.key.jobID,
	}

	pattern := fmt.Sprintf("job_%d_*.log", jobKey.ID)
	file, err := ioutil.TempFile(dir, pattern)
	if err != nil {
		return "", nil, err
	}
	logPath := path.Join(dir, filepath.Base(file.Name()))

	switch err := s.cache.WriteLog(ctx, jobKey, file); err {
	case ErrIncompleteLog:
		stream := func(ctx context.Context) error {
			defer file.Close()
			w := utils.NewANSIStripper(file)
			return s.cache.StreamLog(ctx, jobKey, w)
		}
		return logPath, stream, nil
	case nil:
		return logPath, nil, file.Close()
	default:
		file.Close()
		return logPath, nil, err
	}
}
