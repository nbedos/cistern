package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/nbedos/citop/utils"
)

var ErrRepositoryNotFound = errors.New("repository not found")

type Provider interface {
	AccountID() string
	// Builds should return err == ErrRepositoryNotFound when appropriate
	Builds(ctx context.Context, repositoryURL string, duration time.Duration, buildc chan<- Build) error
	Log(ctx context.Context, repository Repository, jobID int) (string, error)
	StreamLog(ctx context.Context, repositoryID int, jobID int, writer io.Writer) error
}

type State string

func (s State) IsActive() bool {
	return s == Pending || s == Running
}

const (
	Unknown  State = ""
	Pending  State = "pending"
	Running  State = "running"
	Passed   State = "passed"
	Failed   State = "failed"
	Canceled State = "canceled"
	Manual   State = "manual"
	Skipped  State = "skipped"
)

var statePrecedence = map[State]int{
	Unknown:  80,
	Running:  70,
	Pending:  60,
	Canceled: 50,
	Failed:   40,
	Passed:   30,
	Skipped:  20,
	Manual:   10,
}

type Statuser interface {
	Status() State
	AllowedFailure() bool
}

func AggregateStatuses(ss []Statuser) State {
	if len(ss) == 0 {
		return Unknown
	}

	state := ss[0].Status()
	for _, s := range ss {
		if !s.AllowedFailure() || (s.Status() != Canceled && s.Status() != Failed) {
			if statePrecedence[s.Status()] > statePrecedence[state] {
				state = s.Status()
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
	Date    utils.NullTime
}

type Build struct {
	Repository      *Repository
	ID              string
	Commit          Commit
	Ref             string
	IsTag           bool
	RepoBuildNumber string
	State           State
	CreatedAt       utils.NullTime
	StartedAt       utils.NullTime
	FinishedAt      utils.NullTime
	UpdatedAt       time.Time
	Duration        utils.NullDuration
	WebURL          string
	Stages          map[int]*Stage
	Jobs            map[int]*Job
}

func (b Build) Status() State        { return b.State }
func (b Build) AllowedFailure() bool { return false }

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
	BuildID   string
	StageID   int
	ID        int
}

type Job struct {
	Build        *Build
	Stage        *Stage // nil if the Job is only linked to a Build
	ID           int
	State        State
	Name         string
	CreatedAt    utils.NullTime
	StartedAt    utils.NullTime
	FinishedAt   utils.NullTime
	Duration     utils.NullDuration
	Log          utils.NullString
	WebURL       string
	AllowFailure bool
}

func (j Job) Status() State        { return j.State }
func (j Job) AllowedFailure() bool { return j.AllowFailure }

type buildKey struct {
	AccountID string
	BuildID   string
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

func (c *Cache) SaveJob(job Job) error {
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
	if job.Stage == nil {
		build.Jobs[job.ID] = &job
	} else {
		stage, exists := build.Stages[job.Stage.ID]
		if !exists {
			return fmt.Errorf("build has no stage %d", job.Stage.ID)
		}
		stage.Jobs[job.ID] = &job
	}
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

	repositoryNotFoundCount := 0
	for _, provider := range c.providers {
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			err := p.Builds(subCtx, repositoryURL, maxAge, buildc)
			if err == ErrRepositoryNotFound {
				repositoryNotFoundCount++
			}
			// FIXME We probably want to log this
			if err != nil && (err != ErrRepositoryNotFound || repositoryNotFoundCount == len(c.providers)) {
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

var ErrNoJobFound = errors.New("no job found")

func (c *Cache) fetchJob(key JobKey) (Job, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	build, exists := c.builds[buildKey{
		AccountID: key.AccountID,
		BuildID:   key.BuildID,
	}]
	if exists {
		if key.StageID == 0 {
			job, exists := build.Jobs[key.ID]
			if exists {
				return *job, nil
			}
		} else {
			stage, exists := build.Stages[key.StageID]
			if exists {
				job, exists := stage.Jobs[key.ID]
				if exists {
					return *job, nil
				}
			}
		}
	}

	return Job{}, ErrNoJobFound
}

var ErrIncompleteLog = errors.New("log not complete")

func (c *Cache) WriteLog(ctx context.Context, key JobKey, writer io.Writer) error {
	job, err := c.fetchJob(key)
	if err != nil {
		return err
	}
	if job.State.IsActive() {
		return ErrIncompleteLog
	}

	if !job.Log.Valid {
		accountID := job.Build.Repository.AccountID
		provider, exists := c.providers[accountID]
		if !exists {
			return fmt.Errorf("no matching provider found in cache for account ID '%s'", accountID)
		}
		log, err := provider.Log(ctx, *job.Build.Repository, job.ID)
		if err != nil {
			return err
		}

		job.Log = utils.NullString{String: log, Valid: true}
		if err = c.SaveJob(job); err != nil {
			return err
		}
	}

	log := job.Log.String
	if !strings.HasSuffix(log, "\n") {
		log = log + "\n"
	}
	processedLog := utils.PostProcess(log)
	_, err = writer.Write([]byte(processedLog))
	return err
}

func (c Cache) StreamLog(ctx context.Context, key JobKey, writer io.WriteCloser) error {
	job, err := c.fetchJob(key)
	if err != nil {
		return err
	}

	provider, exists := c.providers[key.AccountID]
	if !exists {
		return fmt.Errorf("no matching provider found for account ID '%s'", key.AccountID)
	}

	return provider.StreamLog(ctx, job.Build.Repository.ID, job.ID, writer)
}
