package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/mattn/go-runewidth"
	"github.com/nbedos/citop/utils"
	"io"
	"os"
	"path"
	"sort"
)

type buildRowKey struct {
	accountID string
	buildID   int
	stageID   int
	jobID     int
}

type buildRow struct {
	key         buildRowKey
	id          string
	type_       string
	state       string
	name        string
	prefix      string
	startedAt   sql.NullTime
	finishedAt  sql.NullTime
	updatedAt   sql.NullTime
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

func (b buildRow) Tabular() map[string]string {
	const nullPlaceholder = "-"

	nullTimeToString := func(t sql.NullTime) string {
		if t.Valid {
			return t.Time.String()
		}
		return nullPlaceholder
	}

	return map[string]string{
		"ACCOUNT":  b.key.accountID,
		"STATE":    b.state,
		" NAME":    fmt.Sprintf("%v%v", b.prefix, b.name),
		"BUILD":    fmt.Sprintf("%d", b.key.buildID),
		"STAGE":    fmt.Sprintf("%d", b.key.stageID),
		"JOB":      fmt.Sprintf("%d", b.key.jobID),
		"TYPE":     b.type_,
		"ID":       b.id,
		"STARTED":  nullTimeToString(b.startedAt),
		"FINISHED": nullTimeToString(b.finishedAt),
		"UPDATED":  nullTimeToString(b.updatedAt),
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
	maxWidths     map[string]int
	treeIndex     map[buildRowKey]*buildRow
	dfsTraversal  []*buildRow
	dfsIndex      map[buildRowKey]int
	dfsUpToDate   bool
}

func (c *Cache) NewRepositoryBuilds(repositoryURL string) RepositoryBuilds {
	return RepositoryBuilds{
		cache:         *c,
		repositoryURL: repositoryURL,
		maxWidths:     make(map[string]int),
	}
}

func (s RepositoryBuilds) MaxWidths() map[string]int {
	return s.maxWidths
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

func buildRowFromBuild(b Build) buildRow {
	row := buildRow{
		key: buildRowKey{
			accountID: b.AccountID,
			buildID:   b.ID,
		},
		type_:      "P",
		state:      string(b.State),
		name:       "",
		startedAt:  b.StartedAt,
		finishedAt: b.FinishedAt,
		updatedAt:  sql.NullTime{Time: b.UpdatedAt, Valid: true},
		url:        b.WebURL,
	}

	for _, job := range b.Jobs {
		row.children = append(row.children, buildRowFromJob(*job))
	}

	for _, stage := range b.Stages {
		row.children = append(row.children, buildRowFromStage(*stage))
	}

	return row
}

func buildRowFromStage(s Stage) buildRow {
	row := buildRow{
		key: buildRowKey{
			accountID: s.AccountID,
			buildID:   s.BuildID,
			stageID:   s.ID,
		},
		type_:      "S",
		state:      string(s.State),
		name:       s.Name,
		startedAt:  sql.NullTime{},
		finishedAt: sql.NullTime{},
		updatedAt:  sql.NullTime{},
		url:        "",
	}

	for _, job := range s.Jobs {
		row.children = append(row.children, buildRowFromJob(*job))
	}

	return row
}

func buildRowFromJob(j Job) buildRow {
	return buildRow{
		key: buildRowKey{
			accountID: j.Key.AccountID,
			buildID:   j.Key.BuildID,
			stageID:   j.Key.StageID,
			jobID:     j.Key.ID,
		},
		type_:      "J",
		state:      string(j.State),
		name:       j.Name,
		startedAt:  j.StartedAt,
		finishedAt: j.FinishedAt,
		updatedAt:  sql.NullTime{},
		url:        j.WebURL,
	}
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

	rows := make([]buildRow, 0)
	for _, build := range s.cache.Builds() {
		rows = append(rows, buildRowFromBuild(build))
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].updatedAt.Valid && rows[j].updatedAt.Valid && rows[i].updatedAt.Time.After(rows[j].updatedAt.Time)
	})

	maxWidths := make(map[string]int)
	for header := range (buildRow{}).Tabular() {
		maxWidths[header] = runewidth.StringWidth(header)
	}

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

			for header, value := range row.Tabular() {
				maxWidths[header] = utils.MaxInt(maxWidths[header], runewidth.StringWidth(value))
			}
		}
	}

	s.rows = rows
	s.maxWidths = maxWidths
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
			accountID: buildKey.accountID,
			buildID:   buildKey.buildID,
			stageID:   buildKey.stageID,
		},
		{
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

func (s RepositoryBuilds) WriteToDirectory(ctx context.Context, key interface{}, dir string) ([]string, Streamer, error) {
	// FIXME This should probably work with marks and open jobs of all marked rows + active row
	// TODO Allow filtering for errored jobs
	buildKey, ok := key.(buildRowKey)
	if !ok {
		return nil, nil, fmt.Errorf("key conversion to buildRowKey failed: '%v'", key)
	}

	build, exists := s.treeIndex[buildKey]
	if !exists {
		return nil, nil, fmt.Errorf("no row associated to key '%v'", key)
	}

	jobKeys := make([]JobKey, 0)
	for _, row := range utils.DepthFirstTraversal(build, true) {
		if row := row.(*buildRow); row.type_ == "J" {
			jobKeys = append(jobKeys, JobKey{
				AccountID: row.key.accountID,
				BuildID:   row.key.buildID,
				StageID:   row.key.stageID,
				ID:        row.key.jobID,
			})
		}
	}

	jobs := s.cache.FetchJobs(jobKeys)

	paths := make([]string, 0, len(jobs))
	activeJobs := make(map[Job]io.WriteCloser, 0)
	finishedJobs := make(map[Job]io.WriteCloser, 0)
	for _, job := range jobs {
		filename := fmt.Sprintf(
			"%s-%d.%d.%d.log",
			job.Key.AccountID, // FIXME Sanitize account ID
			job.Key.BuildID,
			job.Key.StageID,
			job.Key.ID)
		jobPath := path.Join(dir, filename)
		paths = append(paths, jobPath)
		file, err := os.Create(jobPath)
		if err != nil {
			return nil, nil, err
		}
		if job.State.isActive() {
			activeJobs[job] = file
		} else {
			finishedJobs[job] = file
		}
	}

	if err := WriteLogs(finishedJobs); err != nil {
		return nil, nil, err
	}

	var stream Streamer
	if len(activeJobs) > 0 {
		stream = func(ctx context.Context) error {
			return s.cache.StreamLogs(ctx, activeJobs)
		}
	}

	return paths, stream, nil
}
