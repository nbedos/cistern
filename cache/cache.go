package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/nbedos/citop/utils"
	"io"
	"strings"
	"sync"
	"time"
)

// FIXME Find a better name
type Provider interface {
	AccountID() string
	Builds(ctx context.Context, repositoryURL string, duration time.Duration, buildc chan<- Build) error
	StreamLogs(ctx context.Context, writerByJobID map[int]io.WriteCloser) error
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

type buildKey struct {
	AccountID string
	BuildID   int
}

type Cache struct {
	builds    map[buildKey]*Build
	mutex     *sync.Mutex
	providers map[string]Provider
}

func NewCache(providers []Provider) Cache {
	providersByAccountID := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		providersByAccountID[provider.AccountID()] = provider
	}

	return Cache{
		builds:    make(map[buildKey]*Build),
		mutex:     &sync.Mutex{},
		providers: providersByAccountID,
	}
}

func (c *Cache) Save(build *Build) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.builds[buildKey{
		AccountID: build.AccountID,
		BuildID:   build.ID,
	}] = build
}

func (c Cache) Builds() []Build {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	builds := make([]Build, 0, len(c.builds))
	for _, build := range c.builds {
		builds = append(builds, *build)
	}

	return builds
}

func (c *Cache) UpdateFromProviders(ctx context.Context, repositoryURL string, updates chan time.Time) error {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	wg := sync.WaitGroup{}
	errc := make(chan error)
	buildc := make(chan Build)

	for _, provider := range c.providers {
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			select {
			case errc <- p.Builds(subCtx, repositoryURL, 0, buildc):
				return
			case <-subCtx.Done():
				errc <- subCtx.Err()
				return
			}
		}(provider)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case build := <-buildc:
				c.Save(&build)
				// Signal change on update channel but don't block current goroutine
				wg.Add(1)
				go func() {
					defer wg.Done()
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
		if err == nil && e != nil {
			err = e
			cancel()
		}
	}

	return err
}

func (c *Cache) FetchJobs(jobsKeys []JobKey) []Job {
	jobs := make([]Job, 0, len(jobsKeys))
	c.mutex.Lock()
	defer c.mutex.Unlock()

	for _, key := range jobsKeys {
		build, exists := c.builds[buildKey{
			AccountID: key.AccountID,
			BuildID:   key.BuildID,
		}]
		if exists {
			if key.StageID == 0 {
				job, exists := build.Jobs[key.ID]
				if exists {
					jobs = append(jobs, *job)
				}
			} else {
				stage, exists := build.Stages[key.StageID]
				if exists {
					job, exists := stage.Jobs[key.ID]
					if exists {
						jobs = append(jobs, *job)
					}
				}
			}
		}
	}

	return jobs
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

func (c Cache) StreamLogs(ctx context.Context, writerByJob map[Job]io.WriteCloser) error {
	argsByAccountID := make(map[string]map[int]io.WriteCloser)
	for job, writer := range writerByJob {
		if _, exists := argsByAccountID[job.Key.AccountID]; !exists {
			argsByAccountID[job.Key.AccountID] = make(map[int]io.WriteCloser)
		}

		argsByAccountID[job.Key.AccountID][job.RemoteID] = writer
	}

	errc := make(chan error)
	wg := sync.WaitGroup{}
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for accountID, writerByJobID := range argsByAccountID {
		wg.Add(1)
		go func(accountID string, writerByJobID map[int]io.WriteCloser) {
			defer wg.Done()

			provider, exists := c.providers[accountID]
			if !exists {
				errc <- fmt.Errorf("no matching provider found for account ID '%s'", accountID)
				return
			}
			if err := provider.StreamLogs(subCtx, writerByJobID); err != nil {
				errc <- err
				return
			}
		}(accountID, writerByJobID)
	}

	var err error
	for e := range errc {
		if err == nil {
			err = e
		}
		cancel()
	}

	return err
}
