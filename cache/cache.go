package cache

import (
	"context"
	"database/sql"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"github.com/nbedos/citop/utils"
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// FIXME Find a better name
type Requester interface {
	AccountID() string
	Builds(ctx context.Context, repository Repository, duration time.Duration, buildc chan<- Build) error
	Repository(ctx context.Context, repositoryURL string) (Repository, error)
}

type Inserter interface {
	insert(ctx context.Context, transaction *sql.Tx) (sql.Result, error)
}

type State string

const (
	Unknown  State = "?"
	Pending  State = "pending"
	Running  State = "running"
	Passed   State = "passed"
	Failed   State = "failed"
	Canceled State = "canceled"
	Skipped  State = "skipped"
)

type Account struct {
	ID       string
	URL      string
	UserID   string
	Username string
}

type Repository struct {
	AccountID string
	URL       string
	Owner     string
	Name      string
	RemoteID  int
}

func (r Repository) Slug() string {
	return fmt.Sprintf("%s/%s", r.Owner, r.Name)
}

type Commit struct {
	AccountID     string
	ID            string
	RepositoryURL string
	Message       string
	Date          sql.NullTime
}

type Build struct {
	AccountID       string
	ID              int
	RepositoryURL   string
	Commit          Commit
	Ref             string
	IsTag           bool
	RepoBuildNumber string
	State           State
	CreatedAt       sql.NullTime
	StartedAt       sql.NullTime
	FinishedAt      sql.NullTime
	UpdatedAt       time.Time
	Duration        sql.NullInt64
	WebURL          string
	Stages          map[int]*Stage
	Jobs            map[int]*Job
}

type Stage struct {
	AccountID string
	BuildID   int
	ID        int
	Name      string
	State     State
	Jobs      map[int]*Job
}

type JobKey struct {
	AccountID string
	BuildID   int
	StageID   int
	ID        int
}

// FIXME Now that jobs are embedded in Build and Stage we might remove Key and only keep ID
type Job struct {
	Key        JobKey
	State      State
	Name       string
	CreatedAt  sql.NullTime
	StartedAt  sql.NullTime
	FinishedAt sql.NullTime
	Duration   sql.NullInt64
	Log        sql.NullString
}

type Cache struct {
	filePath   string
	db         *sql.DB
	requesters []Requester
	mutex      *sync.Mutex
}

func NewCache(filePath string, removeExisting bool, requesters []Requester) (Cache, error) {
	if removeExisting {
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return Cache{}, err
		}
	}

	// FIXME Properly escape filePath
	// WAL is activated to allow read access during write operations
	uri := fmt.Sprintf("%s?_foreign_keys=on&_journal_mode=WAL", filePath)
	db, err := sql.Open("sqlite3", uri)
	if err != nil {
		return Cache{}, err
	}
	defer func() {
		if err != nil {
			_ = db.Close()
		}
	}()

	var count int
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' ;").Scan(&count)
	if err != nil {
		return Cache{}, err
	}

	if count == 0 {
		if _, err = db.Exec(CurrentSchema); err != nil {
			return Cache{}, err
		}
	}

	c := Cache{filePath, db, requesters, &sync.Mutex{}}

	return c, nil
}

func (c Cache) Close() (err error) {
	return c.db.Close()
}

// Serialize write operations for safe use across multiple goroutines
// FIXME This will break with multiple instances of citop accessing the same
//  cache database ==> Check for sqlite BUSY error
func (c Cache) Save(ctx context.Context, inserters []Inserter) (err error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	for _, i := range inserters {
		if _, err = i.insert(ctx, tx); err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				return fmt.Errorf("insert and rollback failed: %w (%v)", err, rollbackErr)
			}
			return err
		}
	}

	return tx.Commit()
}

func TemporaryCache(ctx context.Context, name string, inserters []Inserter) (Cache, error) {
	dir, err := ioutil.TempDir("", name)
	if err != nil {
		return Cache{}, err
	}
	cache, err := NewCache(path.Join(dir, "cache.db"), true, nil)
	if err != nil {
		return Cache{}, err
	}

	if err = cache.Save(ctx, inserters); err != nil {
		return Cache{}, err
	}

	return cache, nil
}

func (a Account) insert(ctx context.Context, tx *sql.Tx) (sql.Result, error) {
	return tx.ExecContext(
		ctx,
		"INSERT INTO account(id, url, user_id, username) VALUES (:id, :url, :user_id, :username);",
		sql.Named("id", a.ID),
		sql.Named("url", a.URL),
		sql.Named("user_id", a.UserID),
		sql.Named("username", a.Username),
	)
}

func (r Repository) insert(ctx context.Context, tx *sql.Tx) (sql.Result, error) {
	return tx.ExecContext(
		ctx,
		`INSERT INTO vcs_repository(account_id, url, owner, name, remote_id)
		 VALUES (:account_id, :url, :owner, :name, :remote_id)
		 ON CONFLICT DO NOTHING;`,
		sql.Named("account_id", r.AccountID),
		sql.Named("url", r.URL),
		sql.Named("owner", r.Owner),
		sql.Named("name", r.Name),
		sql.Named("remote_id", r.RemoteID),
	)
}

func (c Commit) insert(ctx context.Context, tx *sql.Tx) (sql.Result, error) {
	return tx.ExecContext(
		ctx,
		`INSERT OR REPLACE INTO vcs_commit(account_id, repository_url, id, message, date)
		 VALUES (:account_id, :repository_url, :id, :message, :date)
		 ON CONFLICT DO NOTHING ;`,
		sql.Named("account_id", c.AccountID),
		sql.Named("repository_url", c.RepositoryURL),
		sql.Named("id", c.ID),
		sql.Named("message", c.Message),
		sql.Named("date", utils.NullStringFromNullTime(c.Date)),
	)
}

func (b Build) insert(ctx context.Context, tx *sql.Tx) (res sql.Result, err error) {
	if res, err := b.Commit.insert(ctx, tx); err != nil {
		return res, err
	}

	// Should we delete an existing build only if it's older than the one to insert?
	res, err = tx.ExecContext(
		ctx,
		`DELETE FROM build WHERE account_id = :account_id AND id = :id;
		INSERT INTO build(
			account_id,
			id,
			repository_url,
			commit_id,
			ref,
			is_tag,
			repo_build_number,
			state,
			created_at,
			started_at,
			finished_at,
		    updated_at,
		    duration,
            web_url
		 ) VALUES (
			:account_id,
			:id,
			:repository_url,
			:commit_id,
			:ref,
			:is_tag,
			:repo_build_number,
			:state,
			:created_at,
			:started_at,
			:finished_at,
		    :updated_at,
		    :duration,
		    :web_url
	    );`,
		sql.Named("account_id", b.AccountID),
		sql.Named("id", b.ID),
		sql.Named("account_id", b.AccountID),
		sql.Named("id", b.ID),
		sql.Named("repository_url", b.RepositoryURL),
		sql.Named("commit_id", b.Commit.ID),
		sql.Named("ref", b.Ref),
		sql.Named("is_tag", b.IsTag),
		sql.Named("repo_build_number", b.RepoBuildNumber),
		sql.Named("state", b.State),
		sql.Named("created_at", utils.NullStringFromNullTime(b.CreatedAt)),
		sql.Named("started_at", utils.NullStringFromNullTime(b.StartedAt)),
		sql.Named("finished_at", utils.NullStringFromNullTime(b.FinishedAt)),
		sql.Named("updated_at", b.UpdatedAt.Format(time.RFC3339)),
		sql.Named("duration", b.Duration),
		sql.Named("web_url", b.WebURL),
	)
	if err != nil {
		return res, err
	}

	for _, stage := range b.Stages {
		if res, err = stage.insert(ctx, tx); err != nil {
			return res, err
		}
	}

	for _, job := range b.Jobs {
		if res, err = job.insert(ctx, tx); err != nil {
			return res, err
		}
	}

	return res, err
}

func (s Stage) insert(ctx context.Context, tx *sql.Tx) (sql.Result, error) {
	res, err := tx.ExecContext(
		ctx,
		`INSERT INTO stage(account_id, build_id, id, name, state)
		 VALUES (:account_id, :build_id, :id, :name, :state);`,
		sql.Named("account_id", s.AccountID),
		sql.Named("build_id", s.BuildID),
		sql.Named("id", s.ID),
		sql.Named("name", s.Name),
		sql.Named("state", s.State),
	)
	if err != nil {
		return res, err
	}

	for _, job := range s.Jobs {
		if res, err = job.insert(ctx, tx); err != nil {
			return res, err
		}
	}

	return res, err
}

func (j Job) insert(ctx context.Context, tx *sql.Tx) (sql.Result, error) {
	if j.Key.StageID == 0 {
		return tx.ExecContext(
			ctx,
			`INSERT INTO build_job(account_id, build_id, id, state, name, created_at, started_at,
                                  finished_at, log, duration)
            VALUES (:account_id, :build_id, :id, :state, :name, :created_at, :started_at,
                    :finished_at, :log, :duration);`,
			sql.Named("account_id", j.Key.AccountID),
			sql.Named("build_id", j.Key.BuildID),
			sql.Named("id", j.Key.ID),
			sql.Named("state", j.State),
			sql.Named("name", j.Name),
			sql.Named("created_at", utils.NullStringFromNullTime(j.CreatedAt)),
			sql.Named("started_at", utils.NullStringFromNullTime(j.StartedAt)),
			sql.Named("finished_at", utils.NullStringFromNullTime(j.FinishedAt)),
			sql.Named("log", j.Log),
			sql.Named("duration", j.Duration),
		)
	}

	return tx.ExecContext(
		ctx,
		`INSERT INTO job(account_id, build_id, stage_id, id, state, name, created_at, started_at,
                finished_at, log, duration)
		 VALUES (:account_id, :build_id, :stage_id, :id, :state, :name, :created_at, :started_at,
		         :finished_at, :log, :duration);`,
		sql.Named("account_id", j.Key.AccountID),
		sql.Named("build_id", j.Key.BuildID),
		sql.Named("stage_id", j.Key.StageID),
		sql.Named("id", j.Key.ID),
		sql.Named("state", j.State),
		sql.Named("name", j.Name),
		sql.Named("created_at", utils.NullStringFromNullTime(j.CreatedAt)),
		sql.Named("started_at", utils.NullStringFromNullTime(j.StartedAt)),
		sql.Named("finished_at", utils.NullStringFromNullTime(j.FinishedAt)),
		sql.Named("log", j.Log),
		sql.Named("duration", j.Duration),
	)
}

func (c Cache) FetchJobs(ctx context.Context, jobsKeys []JobKey) ([]Job, error) {
	if len(jobsKeys) == 0 {
		return nil, nil
	}

	// FIXME Dates attributes are missing
	queryFmt := `SELECT account_id, build_id, stage_id, id, state, name, log
 		         FROM job
		         WHERE (account_id, build_id, stage_id, id) IN ( VALUES %s )

				 UNION ALL

				 SELECT account_id, build_id, 0, id, state, name, log
 		         FROM build_job
		         WHERE (account_id, build_id, 0, id) IN ( VALUES %s )`

	inClause := make([]string, 0, len(jobsKeys))
	parameters := make([]interface{}, 0, len(jobsKeys)*4)

	for _, key := range jobsKeys {
		inClause = append(inClause, "(?, ?, ?, ?)")
		for _, parameter := range []interface{}{key.AccountID, key.BuildID, key.StageID, key.ID} {
			parameters = append(parameters, parameter)
		}
	}
	query := fmt.Sprintf(queryFmt, strings.Join(inClause, " , "), strings.Join(inClause, " , "))

	parameters = append(parameters, parameters...)
	rows, err := c.db.QueryContext(ctx, query, parameters...)
	if err != nil {
		return nil, err
	}

	jobsByKey := make(map[JobKey]Job, len(jobsKeys))
	for rows.Next() {
		job := Job{}
		if err = rows.Scan(
			&job.Key.AccountID,
			&job.Key.BuildID,
			&job.Key.StageID,
			&job.Key.ID,
			&job.State,
			&job.Name,
			&job.Log); err != nil {
			return nil, err
		}

		jobsByKey[job.Key] = job
	}

	orderedJobs := make([]Job, 0, len(jobsKeys))
	for _, key := range jobsKeys {
		if job, exists := jobsByKey[key]; exists {
			orderedJobs = append(orderedJobs, job)
		}
	}

	return orderedJobs, nil
}

var deleteEraseInLine = regexp.MustCompile(".*\x1b\\[0K")
var deleteUntilCarriageReturn = regexp.MustCompile(`.*\r([^\r\n])`)

// Is this specific to Travis?
func preprocess(log string) string {
	tmp := deleteEraseInLine.ReplaceAllString(log, "")
	return deleteUntilCarriageReturn.ReplaceAllString(tmp, "$1")
}

func WriteLogs(jobs []Job, dir string) (paths []string, err error) {
	paths = make([]string, 0)
	wg := sync.WaitGroup{}
	errc := make(chan error)

	defer func() {
		if err != nil {
			for _, name := range paths {
				// Ignore error since we're already failing. Not ideal.
				_ = os.Remove(name)
			}
			paths = nil
		}
	}()

	for _, job := range jobs {
		if job.Log.Valid {
			// FIXME Sanitize file name
			relativeJobPath := fmt.Sprintf("job_%s-%d_%d_%d.log", job.Key.AccountID, job.Key.BuildID,
				job.Key.StageID, job.Key.ID)
			wg.Add(1)
			go func(log string, logPath string) {
				defer wg.Done()
				fullPath := path.Join(dir, logPath)
				preprocessedLog := []byte(preprocess(log))
				errc <- ioutil.WriteFile(fullPath, preprocessedLog, 0440)
			}(job.Log.String, relativeJobPath)
			paths = append(paths, relativeJobPath)
		}
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	for e := range errc {
		// Return first error only. meh. FIXME
		if err != nil {
			err = e
		}
	}

	return
}
