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

func (b Build) Get(stageID int, jobID int) (Job, bool) {
	var jobs map[int]*Job
	if stageID == 0 {
		jobs = b.Jobs
	} else {
		stage, exists := b.Stages[stageID]
		if !exists {
			return Job{}, false
		}
		jobs = stage.Jobs
	}

	job, exists := jobs[jobID]
	if !exists {
		return Job{}, false
	}
	return *job, true
}

type Stage struct {
	ID    int
	Name  string
	State State
	Jobs  map[int]*Job
}

type Job struct {
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

type InvalidBuild struct {
	cause string
}

func (e InvalidBuild) Error() string { return e.cause }

func (c *Cache) Save(build *Build) error {
	if build.Repository == nil {
		return InvalidBuild{cause: "build.Repository must not be nil"}
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.builds[buildKey{
		AccountID: build.Repository.AccountID,
		BuildID:   build.ID,
	}] = build
	return nil
}

func (c *Cache) SaveJob(accountID string, buildID string, stageID int, job Job) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	key := buildKey{
		AccountID: accountID,
		BuildID:   buildID,
	}
	build, exists := c.builds[key]
	if !exists {
		return fmt.Errorf("no matching build found in cache for key %v", key)
	}
	if stageID == 0 {
		build.Jobs[job.ID] = &job
	} else {
		stage, exists := build.Stages[stageID]
		if !exists {
			return fmt.Errorf("build has no stage %d", stageID)
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
				if err := c.Save(&build); err != nil {
					errc <- err
					return
				}
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
			// FIXME We should also cancel if all Builds() exit with nil
			cancel()
		}
	}

	return err
}

var ErrNoJobFound = errors.New("no job found")

func (c *Cache) fetchBuild(accountID string, buildID string) (Build, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	build, exists := c.builds[buildKey{
		AccountID: accountID,
		BuildID:   buildID,
	}]
	if exists {
		return *build, exists
	}

	return Build{}, false
}

func (c *Cache) fetchJob(accountID string, buildID string, stageID int, jobID int) (Job, error) {
	build, exists := c.fetchBuild(accountID, buildID)
	if !exists {
		return Job{}, ErrNoJobFound
	}

	job, exists := build.Get(stageID, jobID)
	if !exists {
		return Job{}, ErrNoJobFound
	}

	return job, nil
}

var ErrIncompleteLog = errors.New("log not complete")

func (c *Cache) WriteLog(ctx context.Context, accountID string, buildID string, stageID int, jobID int, writer io.Writer) error {
	build, exists := c.fetchBuild(accountID, buildID)
	if !exists {
		return fmt.Errorf("no matching build for %v %v", accountID, buildID)
	}
	job, err := c.fetchJob(accountID, buildID, stageID, jobID)
	if err != nil {
		return err
	}
	if job.State.IsActive() {
		return ErrIncompleteLog
	}

	if !job.Log.Valid {
		provider, exists := c.providers[accountID]
		if !exists {
			return fmt.Errorf("no matching provider found in cache for account ID %q", accountID)
		}
		log, err := provider.Log(ctx, *build.Repository, job.ID)
		if err != nil {
			return err
		}

		job.Log = utils.NullString{String: log, Valid: true}
		if err = c.SaveJob(accountID, buildID, stageID, job); err != nil {
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

func (c Cache) StreamLog(ctx context.Context, accountID string, buildID string, stageID int, jobID int, writer io.WriteCloser) error {
	build, exists := c.fetchBuild(accountID, buildID)
	if !exists {
		return fmt.Errorf("no matching build for %v %v", accountID, buildID)
	}
	job, err := c.fetchJob(accountID, buildID, stageID, jobID)
	if err != nil {
		return err
	}

	provider, exists := c.providers[accountID]
	if !exists {
		return fmt.Errorf("no matching provider found for account ID %q", accountID)
	}

	return provider.StreamLog(ctx, build.Repository.ID, job.ID, writer)
}
