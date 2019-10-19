package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"github.com/nbedos/citop/utils"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// FIXME Find a better name
type Provider interface {
	AccountID() string
	Builds(ctx context.Context, repository Repository, duration time.Duration, buildc chan<- Build) error
	Repository(ctx context.Context, repositoryURL string) (Repository, error)
	StreamLogs(ctx context.Context, writerByJobId map[int]io.WriteCloser) error
}

type Inserter interface {
	insert(ctx context.Context, transaction *sql.Tx) (sql.Result, error)
}

type State string

func (s State) isActive() bool {
	return s == Pending || s == Running
}

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
	RemoteID        int // FIXME Not in DB
}

func (b *Build) SetJobState(key JobKey, state State) error {
	if key.AccountID != b.AccountID || key.BuildID != b.ID {
		return errors.New("account id or build id mismatch between key and receiver")
	}

	var jobs map[int]*Job
	if key.StageID == 0 {
		jobs = b.Jobs
	} else {
		stage, exists := b.Stages[key.StageID]
		if !exists {
			return errors.New("stage not found")
		}
		jobs = stage.Jobs
	}

	job, exists := jobs[key.ID]
	if !exists {
		return errors.New("job not found")
	}
	job.State = state

	return nil
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

// Return jobKey from string of dot separated numbers
func NewJobKey(s string, accountID string) (JobKey, error) {
	// 	"1.2"   => jobKey{BuildID: 1, ID: 2}
	// 	"1.2.3" => jobKey{BuildID: 1, StageID: 2, ID: 3}
	key := JobKey{AccountID: accountID}

	n, err := fmt.Sscanf(s, "%d.%d.%d", &key.BuildID, &key.StageID, &key.ID)
	if err != nil || n != 3 {
		n, err = fmt.Sscanf(s, "%d.%d", &key.BuildID, &key.ID)
		if err != nil {
			return key, err
		}
		if n != 2 {
			return key, fmt.Errorf("failed parsing jobKey from '%s'", s)
		}
		key.StageID = 0
	}

	return key, nil
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
	WebURL     string
	RemoteID   int
}

type Cache struct {
	filePath   string
	db         *sql.DB
	requesters map[string]Provider
	mutex      *sync.Mutex
}

func NewCache(filePath string, removeExisting bool, requesters []Provider) (Cache, error) {
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

	requesterByAccountID := make(map[string]Provider, len(requesters))
	for _, requester := range requesters {
		requesterByAccountID[requester.AccountID()] = requester
	}

	c := Cache{filePath, db, requesterByAccountID, &sync.Mutex{}}

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
                                  finished_at, log, duration, web_url, remote_id)
            VALUES (:account_id, :build_id, :id, :state, :name, :created_at, :started_at,
                    :finished_at, :log, :duration, :web_url, :remote_id);`,
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
			sql.Named("web_url", j.WebURL),
			sql.Named("remote_id", j.RemoteID),
		)
	}

	return tx.ExecContext(
		ctx,
		`INSERT INTO job(account_id, build_id, stage_id, id, state, name, created_at, started_at,
                finished_at, log, duration, web_url, remote_id)
		 VALUES (:account_id, :build_id, :stage_id, :id, :state, :name, :created_at, :started_at,
		         :finished_at, :log, :duration, :web_url, :remote_id);`,
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
		sql.Named("web_url", j.WebURL),
		sql.Named("remote_id", j.RemoteID),
	)
}

func (c Cache) FetchJobs(ctx context.Context, jobsKeys []JobKey) ([]Job, error) {
	if len(jobsKeys) == 0 {
		return nil, nil
	}

	// FIXME Dates attributes are missing
	queryFmt := `SELECT account_id, build_id, stage_id, id, state, name, remote_id, log
 		         FROM job
		         WHERE (account_id, build_id, stage_id, id) IN ( VALUES %s )

				 UNION ALL

				 SELECT account_id, build_id, 0, id, state, name, remote_id, log
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
			&job.RemoteID,
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

func WriteLogs(writerByJob map[Job]io.WriteCloser) error {
	wg := sync.WaitGroup{}
	errc := make(chan error)
	for job, writer := range writerByJob {
		if job.Log.Valid {
			wg.Add(1)
			go func(log string, writer io.WriteCloser) {
				var err error
				defer wg.Done()
				defer func() {
					// FIXME errClose is not reported if err != nil
					if errClose := writer.Close(); errClose != nil && err == nil {
						err = errClose
					}
					if err != nil {
						errc <- err
					}
				}()
				if !strings.HasSuffix(log, "\n") {
					log = log + "\n"
				}
				preprocessedLog := utils.PostProcess(log)
				if _, err = writer.Write([]byte(preprocessedLog)); err != nil {
					return
				}
			}(job.Log.String, writer)
		}
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	var err error
	for e := range errc {
		// Return first error only. meh. FIXME
		if err == nil {
			err = e
		}
	}

	return err
}
