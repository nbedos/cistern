package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v3"
	"github.com/nbedos/citop/utils"
)

var ErrRepositoryNotFound = errors.New("repository not found")
var ErrUnknownURL = errors.New("URL not recognized")

type CIProvider interface {
	AccountID() string
	Log(ctx context.Context, repository Repository, jobID string) (string, error)
	BuildFromURL(ctx context.Context, u string) (Build, error)
}

type SourceProvider interface {
	// BuildURLs must close 'urls' channel
	BuildURLs(ctx context.Context, owner string, repo string, sha string) ([]string, error)
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
	Jobs            []*Job
}

func (b Build) Status() State        { return b.State }
func (b Build) AllowedFailure() bool { return false }

func (b Build) Get(stageID int, jobID string) (Job, bool) {
	var jobs []*Job
	if stageID == 0 {
		jobs = b.Jobs
	} else {
		stage, exists := b.Stages[stageID]
		if !exists {
			return Job{}, false
		}
		jobs = stage.Jobs
	}

	// meh.
	for _, job := range jobs {
		if job.ID == jobID {
			return *job, true
		}
	}

	return Job{}, false
}

type Stage struct {
	ID    int
	Name  string
	State State
	Jobs  []*Job
}

type Job struct {
	ID           string
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
	builds          map[buildKey]*Build
	mutex           *sync.Mutex
	ciProvidersById map[string]CIProvider
	sourceProviders []SourceProvider
}

func NewCache(CIProviders []CIProvider, sourceProviders []SourceProvider) Cache {
	providersByAccountID := make(map[string]CIProvider, len(CIProviders))
	for _, provider := range CIProviders {
		providersByAccountID[provider.AccountID()] = provider
	}

	return Cache{
		builds:          make(map[buildKey]*Build),
		mutex:           &sync.Mutex{},
		ciProvidersById: providersByAccountID,
		sourceProviders: sourceProviders,
	}
}

var ErrOlderBuild = errors.New("build to save is older than current build in cache")

func (c *Cache) Save(build Build) error {
	if build.Repository == nil {
		return errors.New("build.repository must not be nil")
	}

	cacheBuild, exists := c.fetchBuild(build.Repository.AccountID, build.ID)
	if exists && !cacheBuild.UpdatedAt.Before(build.UpdatedAt) {
		return ErrOlderBuild
	}
	if build.Jobs == nil {
		build.Jobs = make([]*Job, 0)
	}
	if build.Stages == nil {
		build.Stages = make(map[int]*Stage)
	} else {
		for _, stage := range build.Stages {
			if stage.Jobs == nil {
				stage.Jobs = make([]*Job, 0)
			}
		}
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.builds[buildKey{
		AccountID: build.Repository.AccountID,
		BuildID:   build.ID,
	}] = &build

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
		for i := range build.Jobs {
			if build.Jobs[i].ID == job.ID {
				build.Jobs[i] = &job
				return nil
			}
		}
		build.Jobs = append(build.Jobs, &job)
	} else {
		stage, exists := build.Stages[stageID]
		if !exists {
			return fmt.Errorf("build has no stage %d", stageID)
		}
		for i := range stage.Jobs {
			if stage.Jobs[i].ID == job.ID {
				stage.Jobs[i] = &job
				return nil
			}
		}
		stage.Jobs = append(stage.Jobs, &job)
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

func (c *Cache) MonitorPipeline(ctx context.Context, p CIProvider, u string, updates chan time.Time) error {
	b := backoff.ExponentialBackOff{
		InitialInterval:     5 * time.Second,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         5 * time.Minute,
		MaxElapsedTime:      15 * time.Minute,
		Clock:               backoff.SystemClock,
	}
	b.Reset()

	for waitTime := time.Duration(0); waitTime != backoff.Stop; waitTime = b.NextBackOff() {
		select {
		case <-time.After(waitTime):
			// Do nothing
		case <-ctx.Done():
			return ctx.Err()
		}

		build, err := p.BuildFromURL(ctx, u)
		if err != nil {
			return err
		}

		switch err := c.Save(build); err {
		case nil:
			go func() {
				select {
				case updates <- time.Now():
				case <-ctx.Done():
				}
			}()
		case ErrOlderBuild:
			// Do nothing. This is useful to avoid deleting logs on an existing build since
			// p.BuildFromURL() will always return build without logs.
		default:
			return err
		}
	}

	return nil
}

func (c *Cache) GetPipelines(ctx context.Context, repositoryURL string, commit utils.Commit, updates chan time.Time) error {
	var err error
	slug, err := utils.RepositorySlugFromURL(repositoryURL)
	if err != nil {
		return err
	}
	cs := strings.Split(slug, "/")
	if len(cs) != 2 {
		return errors.New("invalid repository slug")
	}
	owner, repo := cs[0], cs[1]

	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	wg := sync.WaitGroup{}
	for _, p := range c.sourceProviders {
		wg.Add(1)
		go func(p SourceProvider) {
			defer wg.Done()

			b := backoff.ExponentialBackOff{
				InitialInterval:     5 * time.Second,
				RandomizationFactor: backoff.DefaultRandomizationFactor,
				Multiplier:          backoff.DefaultMultiplier,
				MaxInterval:         5 * time.Minute,
				MaxElapsedTime:      15 * time.Minute,
				Clock:               backoff.SystemClock,
			}
			b.Reset()

			for waitTime := time.Duration(0); waitTime != backoff.Stop; waitTime = b.NextBackOff() {
				select {
				case <-time.After(waitTime):
					// Do nothing
				case <-ctx.Done():
					errc <- ctx.Err()
					return
				}

				us, err := p.BuildURLs(ctx, owner, repo, commit.Sha)
				if err != nil {
					errc <- err
					return
				}
				for _, u := range us {
					// All providers but 1 should return ErrRepositoryNotFound
					for _, p := range c.ciProvidersById {
						wg.Add(1)
						go func(p CIProvider, u string) {
							defer wg.Done()
							err := c.MonitorPipeline(ctx, p, u, updates)
							if err != nil && err != ErrUnknownURL {
								errc <- err
								return
							}
						}(p, u)
					}
				}
			}
		}(p)
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	for e := range errc {
		if err == nil && e != nil {
			cancel()
			err = e
		}
	}

	return err
}

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

func (c *Cache) fetchJob(accountID string, buildID string, stageID int, jobID string) (Job, bool) {
	build, exists := c.fetchBuild(accountID, buildID)
	if !exists {
		return Job{}, false
	}

	return build.Get(stageID, jobID)
}

var ErrIncompleteLog = errors.New("log not complete")

func (c *Cache) WriteLog(ctx context.Context, accountID string, buildID string, stageID int, jobID string, writer io.Writer) error {
	build, exists := c.fetchBuild(accountID, buildID)
	if !exists {
		return fmt.Errorf("no matching build for %v %v", accountID, buildID)
	}
	job, exists := c.fetchJob(accountID, buildID, stageID, jobID)
	if !exists {
		return fmt.Errorf("no matching job for %v %v %v %v", accountID, buildID, stageID, jobID)
	}

	if !job.Log.Valid {
		provider, exists := c.ciProvidersById[accountID]
		if !exists {
			return fmt.Errorf("no matching provider found in cache for account ID %q", accountID)
		}
		log, err := provider.Log(ctx, *build.Repository, job.ID)
		if err != nil {
			return err
		}

		job.Log = utils.NullString{String: log, Valid: true}
		if !job.State.IsActive() {
			if err = c.SaveJob(accountID, buildID, stageID, job); err != nil {
				return err
			}
		}
	}

	log := job.Log.String
	if !strings.HasSuffix(log, "\n") {
		log = log + "\n"
	}
	processedLog := utils.PostProcess(log)
	_, err := writer.Write([]byte(processedLog))
	return err
}
