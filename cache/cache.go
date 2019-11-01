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

var ErrRepositoryNotFound = errors.New("repository not found")

type Provider interface {
	AccountID() string
	// Builds should return err == ErrRepositoryNotFound when appropriate
	Builds(ctx context.Context, repositoryURL string, duration time.Duration, buildc chan<- Build) error
	Log(ctx context.Context, repository Repository, jobID int) (string, error)
	StreamLogs(ctx context.Context, writerByJobID map[int]io.WriteCloser) error
}

type State string

func (s State) isActive() bool {
	return s == Pending || s == Running
}

const (
	Unknown  State = ""
	Pending  State = "pending"
	Running  State = "running"
	Passed   State = "passed"
	Failed   State = "failed"
	Canceled State = "canceled"
	Skipped  State = "skipped"
)

func StageState(jobs []Job) State {
	precedence := map[State]int{
		Unknown:  7,
		Running:  6,
		Pending:  5,
		Canceled: 4,
		Failed:   3,
		Passed:   2,
		Skipped:  1,
	}
	if len(jobs) == 0 {
		return Unknown
	}
	state := jobs[0].State
	for _, job := range jobs[1:] {
		if !job.AllowFailure || (job.State != Canceled && job.State != Failed) {
			if precedence[job.State] > precedence[state] {
				state = job.State
			}
		}
	}

	return state
}

type Account struct {
	ID       string
	URL      string
	UserID   string
	Username string
}

type Repository struct {
	AccountID string
	ID        int
	URL       string
	Owner     string
	Name      string
}

func (r Repository) Slug() string {
	return fmt.Sprintf("%s/%s", r.Owner, r.Name)
}

type Commit struct {
	Sha     string
	Message string
	Date    sql.NullTime
}

type Build struct {
	Repository      *Repository
	ID              int
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

func (b Build) Get(key JobKey) (*Job, bool) {
	if key.AccountID != b.Repository.AccountID || key.BuildID != b.ID {
		return nil, false
	}

	var jobs map[int]*Job
	if key.StageID == 0 {
		jobs = b.Jobs
	} else {
		stage, exists := b.Stages[key.StageID]
		if !exists {
			return nil, false
		}
		jobs = stage.Jobs
	}

	job, exists := jobs[key.ID]
	if !exists {
		return nil, false
	}
	return job, true
}

type Stage struct {
	Build *Build
	ID    int
	Name  string
	State State
	Jobs  map[int]*Job
}

type JobKey struct {
	AccountID string
	BuildID   int
	StageID   int
	ID        int
}

type Job struct {
	Build        *Build
	Stage        *Stage // nil if the Job is only linked to a Build
	ID           int
	State        State
	Name         string
	CreatedAt    sql.NullTime
	StartedAt    sql.NullTime
	FinishedAt   sql.NullTime
	Duration     sql.NullInt64
	Log          sql.NullString
	WebURL       string
	AllowFailure bool
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
		AccountID: build.Repository.AccountID,
		BuildID:   build.ID,
	}] = build
}

func (c *Cache) SaveJob(job *Job) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	key := buildKey{
		AccountID: job.Build.Repository.AccountID,
		BuildID:   job.Build.ID,
	}
	build, exists := c.builds[key]
	if !exists {
		return fmt.Errorf("no matching build found in cache for key %v", key)
	}
	build.Jobs[job.ID] = job
	return nil
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

func (c *Cache) UpdateFromProviders(ctx context.Context, repositoryURL string, maxAge time.Duration, updates chan time.Time) error {
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	wg := sync.WaitGroup{}
	errc := make(chan error)
	buildc := make(chan Build)

	for _, provider := range c.providers {
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			err := p.Builds(subCtx, repositoryURL, maxAge, buildc)
			// FIXME We probably want to log this
			if err != nil && err != ErrRepositoryNotFound {
				errc <- err
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

func (c *Cache) WriteLogs(ctx context.Context, writerByJob map[Job]io.WriteCloser) error {
	wg := sync.WaitGroup{}
	errc := make(chan error)
	subCtx, cancel := context.WithCancel(ctx)
	for job, writer := range writerByJob {
		wg.Add(1)
		go func(job Job, writer io.WriteCloser) {
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

			if !job.Log.Valid {
				accountID := job.Build.Repository.AccountID
				provider, exists := c.providers[accountID]
				if !exists {
					err = fmt.Errorf("no matching provider found in cache for account ID '%s'", accountID)
					return
				}
				log, err := provider.Log(subCtx, *job.Build.Repository, job.ID)
				if err != nil {
					return
				}

				job.Log = sql.NullString{String: log, Valid: true}
				if err = c.SaveJob(&job); err != nil {
					return
				}
			}

			log := job.Log.String
			if !strings.HasSuffix(log, "\n") {
				log = log + "\n"
			}
			processedLog := utils.PostProcess(log)
			if _, err = writer.Write([]byte(processedLog)); err != nil {
				return
			}
		}(job, writer)
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
			cancel()
		}
	}

	return err
}

func (c Cache) StreamLogs(ctx context.Context, writerByJob map[Job]io.WriteCloser) error {
	argsByAccountID := make(map[string]map[int]io.WriteCloser)
	for job, writer := range writerByJob {
		if _, exists := argsByAccountID[job.Build.Repository.AccountID]; !exists {
			argsByAccountID[job.Build.Repository.AccountID] = make(map[int]io.WriteCloser)
		}

		argsByAccountID[job.Build.Repository.AccountID][job.ID] = writer
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
