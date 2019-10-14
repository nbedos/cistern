package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/mattn/go-runewidth"
	"github.com/nbedos/citop/utils"
	"sync"
	"time"
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

// meh. Maybe working with build, stage job would be easier.
func (b buildRow) IsParentOf(other buildRow) bool {
	switch b.type_ {
	case "P":
		return b.key.accountID == other.key.accountID &&
			b.key.buildID == other.key.buildID
	case "S":
		return b.key.accountID == other.key.accountID &&
			b.key.buildID == other.key.buildID &&
			b.key.stageID == other.key.stageID
	}

	return false
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
	lastUpdate    time.Time
	updates       chan time.Time
}

func (c *Cache) NewRepositoryBuilds(repositoryURL string, updates chan time.Time) RepositoryBuilds {
	return RepositoryBuilds{
		cache:         *c,
		repositoryURL: repositoryURL,
		updates:       make(chan time.Time),
		maxWidths:     make(map[string]int),
	}
}

func (s *RepositoryBuilds) FetchData(ctx context.Context, updates chan time.Time) error {
	insertersChannel := make(chan []Inserter)
	errc := make(chan error)

	subCtx, cancel := context.WithCancel(ctx)

	wg := sync.WaitGroup{}
	for _, requester := range s.cache.requesters {
		wg.Add(1)
		go func(r Requester) {
			defer wg.Done()

			repo, err := s.cache.FindRepository(subCtx, r.AccountID(), s.repositoryURL)
			if err != nil {
				errc <- err
				return
			}
			// FIXME Dynamically check if repository cached or not
			if err := r.Builds(subCtx, repo, 0, insertersChannel); err != nil {
				errc <- err
				return
			}
		}(requester)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case inserters := <-insertersChannel:
				if err := s.cache.Save(subCtx, inserters); err != nil {
					errc <- err
					return
				}
				// Signal database update without blocking the current goroutine
				go func() {
					select {
					case updates <- time.Now():
					case <-subCtx.Done():
					}
				}()
			case <-subCtx.Done():
				errc <- subCtx.Err()
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(errc)
		close(updates)
	}()

	var err error
	for e := range errc {
		// Cancel all goroutines on first error
		if err == nil && e != nil {
			err = e
			cancel()
		}
	}

	return err
}

func (s RepositoryBuilds) MaxWidths() map[string]int {
	return s.maxWidths
}

func (s *RepositoryBuilds) SetTraversable(key interface{}, traversable bool) {
	if key == nil {
		for i := range s.rows {
			s.rows[i].traversable = traversable
		}
		s.dfsUpToDate = false
	} else {
		if buildKey, ok := key.(buildRowKey); ok {
			if row, exists := s.treeIndex[buildKey]; exists {
				row.traversable = traversable
				s.dfsUpToDate = false
			}
		}
	}
}

func parse(rows *sql.Rows) (builds []buildRow, maxWidths map[string]int, err error) {
	// meh.
	var parentBuild, parentStage *buildRow
	maxWidths = make(map[string]int)

	for key := range (buildRow{}).Tabular() {
		maxWidths[key] = runewidth.StringWidth(key)
	}

	for rows.Next() {
		build := buildRow{}

		var startedAt, finishedAt, updatedAt sql.NullString
		if err = rows.Scan(
			&build.key.accountID,
			&build.id,
			&build.key.buildID,
			&build.key.stageID,
			&build.key.jobID,
			&build.type_,
			&build.state,
			&build.name,
			&startedAt,
			&finishedAt,
			&updatedAt,
			&build.url,
		); err != nil {
			return nil, nil, err
		}

		dates := map[*sql.NullString]*sql.NullTime{
			&startedAt:  &build.startedAt,
			&finishedAt: &build.finishedAt,
			&updatedAt:  &build.updatedAt,
		}
		for s, t := range dates {
			if s.Valid {
				if t.Time, err = time.Parse(time.RFC3339, s.String); err != nil {
					return
				}
				t.Valid = true
			}
		}

		if build.type_ == "P" {
			builds = append(builds, build)
			parentBuild = &builds[len(builds)-1]
		} else {
			if parentBuild == nil || len(builds) == 0 {
				return nil, nil, errors.New("no previous build found for current stage")
			}

			if !parentBuild.IsParentOf(build) {
				return nil, nil, errors.New("current build is not the parent of the current stage")
			}

			switch build.type_ {
			case "S":
				parentBuild.children = append(parentBuild.children, build)
				parentStage = &parentBuild.children[len(parentBuild.children)-1]
			case "J":
				if build.key.stageID == 0 {
					parentBuild.children = append(parentBuild.children, build)
				} else {
					if parentStage == nil {
						return nil, nil, errors.New("no previous stage found for current job")
					}

					if !parentStage.IsParentOf(build) {
						return nil, nil, errors.New("current stage is not the parent of the current job")
					}

					parentStage.children = append(parentStage.children, build)
				}
			}
		}

		for key, value := range build.Tabular() {
			maxWidths[key] = utils.MaxInt(maxWidths[key], runewidth.StringWidth(value))
		}

	}

	return
}

func (s *RepositoryBuilds) FetchRows() (err error) {
	query := `
		SELECT
			build.account_id,
			printf('#%d', build.id),
			build.id,
			0,
			0,
			'P',
			build.state,
		    build.account_id,
			build.started_at,
			build.finished_at,
		    build.updated_at,
		    build.web_url
		FROM build

		UNION ALL

		SELECT
			build.account_id,
			printf('#%d.%d', build.id, stage.id),
			build.id,
			stage.id,
			0,
			'S',
			stage.state,
			stage.name,
			NULL,
			NULL,
			NULL,
		    ''
		FROM build
		JOIN stage ON
			build.account_id = stage.account_id
			AND build.id = stage.build_id
		
		UNION ALL

		SELECT
			build.account_id,
			printf('#%d.%d.%d', build.id, stage.id ,job.id),
			build.id,
			stage.id,
			job.id,
			'J',
			job.state,
			job.name,
			job.started_at,
			job.finished_at,
		    NULL,
		    ''
		FROM build
		JOIN stage ON
			build.account_id = stage.account_id
			AND build.id = stage.build_id
		JOIN job ON
			stage.account_id = job.account_id
			AND stage.build_id = job.build_id
			AND stage.id = job.stage_id
			
		UNION ALL

		SELECT
			build.account_id,
			printf('#%d.0.%d', build.id, build_job.id), -- FIXME SECURELY remove the stage_id segment
			build.id,
			0,
			build_job.id,
			'J',
			build_job.state,
			build_job.name,
			build_job.started_at,
			build_job.finished_at,
		    NULL,
		    ''
		FROM build
		JOIN build_job ON
			build.account_id = build_job.account_id
			AND build.id = build_job.build_id
			
	ORDER BY 3 DESC, 4 ASC, 5 ASC;`

	ctx := context.Background()
	sqlRows, err := s.cache.db.QueryContext(ctx, query)
	if err != nil {
		return
	}
	defer func() {
		if errClose := sqlRows.Close(); err != nil {
			err = errClose
		}
	}()

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

	rows, maxWidths, err := parse(sqlRows)
	if err != nil {
		return
	}
	s.rows, s.maxWidths = rows, maxWidths

	s.treeIndex = make(map[buildRowKey]*buildRow)
	for i := range s.rows {
		traversal := utils.DepthFirstTraversal(&s.rows[i], true)
		for j := range traversal {
			row := traversal[j].(*buildRow)
			s.treeIndex[row.key] = row
			// Restore traversable state of node
			if value, exists := traversables[row.key]; exists {
				row.traversable = value
			}
		}
	}
	s.dfsUpToDate = false
	s.lastUpdate = time.Now()

	return
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

func (s RepositoryBuilds) WriteToDirectory(ctx context.Context, key interface{}, dir string) ([]string, error) {
	// FIXME This should probably work with marks and open jobs of all marked rows + active row
	// TODO Allow filtering for errored jobs
	buildKey, ok := key.(buildRowKey)
	if !ok {
		return nil, fmt.Errorf("key conversion to buildRowKey failed: '%v'", key)
	}

	build, exists := s.treeIndex[buildKey]
	if !exists {
		return nil, fmt.Errorf("no row associated to key '%v'", key)
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

	jobs, err := s.cache.FetchJobs(ctx, jobKeys)
	if err != nil {
		return nil, err
	}

	return WriteLogs(jobs, dir)
}
