package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/citop/text"
	"github.com/nbedos/citop/utils"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
)

var ErrUnknownRepositoryURL = errors.New("unknown repository URL")
var ErrUnknownPipelineURL = errors.New("unknown pipeline URL")
var ErrUnknownGitReference = errors.New("unknown git reference")

type CIProvider interface {
	ID() string
	Log(ctx context.Context, repository Repository, jobID string) (string, error)
	BuildFromURL(ctx context.Context, u string) (Build, error)
}

type SourceProvider interface {
	ID() string
	RefStatuses(ctx context.Context, url string, sha string) ([]string, error)
	Commit(ctx context.Context, repo string, sha string) (Commit, error)
}

// Poll provider at increasing interval for the URL of statuses associated to "ref"
func monitorRefStatuses(ctx context.Context, p SourceProvider, url string, ref string, commitc chan<- Commit) error {
	b := backoff.ExponentialBackOff{
		InitialInterval:     5 * time.Second,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         1 * time.Minute,
		MaxElapsedTime:      0,
		Clock:               backoff.SystemClock,
	}
	b.Reset()

	commit, err := p.Commit(ctx, url, ref)
	if err != nil {
		return err
	}
	select {
	case commitc <- commit:
		// Do nothing
	case <-ctx.Done():
		return ctx.Err()
	}

	for waitTime := time.Duration(0); waitTime != backoff.Stop; waitTime = b.NextBackOff() {
		select {
		case <-time.After(waitTime):
			// Do nothing
		case <-ctx.Done():
			return ctx.Err()
		}

		statuses, err := p.RefStatuses(ctx, url, ref)
		if err != nil {
			if err != ErrUnknownRepositoryURL && err != context.Canceled {
				err = fmt.Errorf("provider %s: %v (%s@%s)", p.ID(), err, ref, url)
			}
			return err
		}

		sort.Strings(statuses)
		if diff := cmp.Diff(statuses, commit.Statuses); len(diff) > 0 {
			commit.Statuses = statuses
			select {
			case commitc <- commit:
				// Do nothing
			case <-ctx.Done():
				return ctx.Err()
			}
			b.Reset()
		}
	}

	return nil
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

type Statuser interface {
	Status() State
	AllowedFailure() bool
}

func AggregateStatuses(ss []Statuser) State {
	if len(ss) == 0 {
		return Unknown
	}

	statePrecedence := map[State]int{
		Unknown:  80,
		Running:  70,
		Pending:  60,
		Canceled: 50,
		Failed:   40,
		Passed:   30,
		Skipped:  20,
		Manual:   10,
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

type Provider struct {
	ID   string
	Name string
}

type Repository struct {
	Provider Provider
	ID       int
	URL      string
	Owner    string
	Name     string
}

func (r Repository) Slug() string {
	return fmt.Sprintf("%s/%s", r.Owner, r.Name)
}

type Commit struct {
	Sha      string
	Author   string
	Date     time.Time
	Message  string
	Branches []string
	Tags     []string
	Head     string
	Statuses []string
}

func (c Commit) Strings() []text.StyledString {
	var title text.StyledString
	commit := text.NewStyledString(fmt.Sprintf("commit %s", c.Sha), text.GitSha)
	if len(c.Branches) > 0 || len(c.Tags) > 0 {
		refs := make([]text.StyledString, 0, len(c.Branches)+len(c.Tags))
		for _, tag := range c.Tags {
			refs = append(refs, text.NewStyledString(fmt.Sprintf("tag: %s", tag), text.GitTag))
		}
		for _, branch := range c.Branches {
			if branch == c.Head {
				var s text.StyledString
				s.Append("HEAD -> ", text.GitHead)
				s.Append(branch, text.GitBranch)
				refs = append([]text.StyledString{s}, refs...)
			} else {
				refs = append(refs, text.NewStyledString(branch, text.GitBranch))
			}
		}

		title = text.Join([]text.StyledString{
			commit,
			text.NewStyledString(" (", text.GitSha),
			text.Join(refs, text.NewStyledString(", ", text.GitSha)),
			text.NewStyledString(")", text.GitSha),
		}, text.NewStyledString(""))
	} else {
		title = commit
	}

	texts := []text.StyledString{
		title,
		text.NewStyledString(fmt.Sprintf("Author: %s", c.Author)),
		text.NewStyledString(fmt.Sprintf("Date: %s", c.Date.Truncate(time.Second).Local().String())),
		text.NewStyledString(""),
	}
	for _, line := range strings.Split(c.Message, "\n") {
		texts = append(texts, text.NewStyledString("    "+line))
		break
	}

	return texts
}

func GitOriginURL(path string, sha string) (string, Commit, error) {
	// If a path does not refer to an existing file or directory, go-git will continue
	// running and will walk its way up the directory structure looking for a .git repository.
	// This is not ideal for us since running 'citop -r github.com/owner/remoterepo' from
	// /home/user/localrepo will make go-git look for a .git repository in
	// /home/user/localrepo/github.com/owner/remoterepo which will inevitably lead to
	// /home/user/localrepo which is not what the user expected since the user was
	// refering to the online repository https://github.com/owner/remoterepo. So instead
	// we bail out early if the path is invalid, meaning it's not a local path but a URL.
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			err = ErrUnknownRepositoryURL
		}
		return "", Commit{}, err
	}

	r, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", Commit{}, err
	}

	remote, err := r.Remote("origin")
	if err != nil {
		return "", Commit{}, err
	}
	if len(remote.Config().URLs) == 0 {
		return "", Commit{}, fmt.Errorf("GIT repository %q: remote 'origin' has no associated URL", path)
	}
	origin := remote.Config().URLs[0]

	head, err := r.Head()
	if err != nil {
		return origin, Commit{}, err
	}

	var hash plumbing.Hash
	if sha == "HEAD" {
		hash = head.Hash()
	} else {
		if p, err := r.ResolveRevision(plumbing.Revision(sha)); err == nil {
			hash = *p
		} else {
			// Ideally we'd take this path only for certain error cases but some errors
			// from go-git that should not be fatal to us are internal making it impossible
			// to test against them.

			// The failure of ResolveRevision may be due to go-git failure to resolve an
			// abbreviated SHA. This is quite useful so, for now, circumvent the problem by
			// using the local git binary.
			cmd := exec.Command("git", "show", sha, "--pretty=format:%H")
			bs, err := cmd.Output()
			if err != nil {
				return origin, Commit{}, ErrUnknownGitReference
			}

			hash = plumbing.NewHash(strings.SplitN(string(bs), "\n", 2)[0])
		}

	}
	commit, err := r.CommitObject(hash)
	if err != nil {
		return origin, Commit{}, err
	}

	c := Commit{
		Sha:      commit.Hash.String(),
		Author:   commit.Author.String(),
		Date:     commit.Author.When,
		Message:  commit.Message,
		Branches: nil,
		Tags:     nil,
		Head:     head.Name().Short(),
	}

	refs, err := r.References()
	if err != nil {
		return origin, Commit{}, err
	}

	err = refs.ForEach(func(ref *plumbing.Reference) error {
		if ref.Hash() != commit.Hash {
			return nil
		}

		if ref.Name() == head.Name() {
			c.Head = head.Name().Short()
		}

		switch {
		case ref.Name().IsTag():
			c.Tags = append(c.Tags, ref.Name().Short())
		case ref.Name().IsBranch(), ref.Name().IsRemote():
			c.Branches = append(c.Branches, ref.Name().Short())
		}

		return nil
	})
	if err != nil {
		return origin, Commit{}, err
	}

	return origin, c, nil
}

type Build struct {
	Repository      *Repository
	ID              string
	Sha             string
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
		if b.Stages == nil {
			return Job{}, false
		}
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
	commitsByRef    map[string]Commit
	buildsByKey     map[buildKey]*Build
	buildsByRef     map[string]map[buildKey]*Build
	mutex           *sync.Mutex
	ciProvidersById map[string]CIProvider
	sourceProviders []SourceProvider
}

func NewCache(CIProviders []CIProvider, sourceProviders []SourceProvider) Cache {
	providersByAccountID := make(map[string]CIProvider, len(CIProviders))
	for _, provider := range CIProviders {
		providersByAccountID[provider.ID()] = provider
	}

	return Cache{
		commitsByRef:    make(map[string]Commit),
		buildsByKey:     make(map[buildKey]*Build),
		buildsByRef:     make(map[string]map[buildKey]*Build),
		mutex:           &sync.Mutex{},
		ciProvidersById: providersByAccountID,
		sourceProviders: sourceProviders,
	}
}

var ErrObsoleteBuild = errors.New("build to save is older than current build in cache")

// Store build in cache. If a build from the same provider and with the same ID is
// already stored in cache, it will be overwritten if the build to save is more recent
// than the build in cache. If the build to save is older than the build in cache,
// SaveBuild will return ErrObsoleteBuild.
func (c *Cache) SaveBuild(ref string, build Build) error {
	if build.Repository == nil {
		return errors.New("build.repository must not be nil")
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	key := buildKey{
		AccountID: build.Repository.Provider.ID,
		BuildID:   build.ID,
	}

	existingBuild, exists := c.buildsByKey[key]
	// UpdatedAt refers to the last update of the build and does not reflect an eventual
	// update of a job so default to always updating an active build
	if exists && !build.State.IsActive() && !build.UpdatedAt.After(existingBuild.UpdatedAt) {
		// Point ref to existingBuild
		if _, exists := c.buildsByRef[ref]; !exists {
			c.buildsByRef[ref] = make(map[buildKey]*Build)
		}
		if c.buildsByRef[ref][key] == existingBuild {
			return ErrObsoleteBuild
		}
		c.buildsByRef[ref][key] = existingBuild
		return nil
	}

	c.buildsByKey[key] = &build
	// Point ref to new build
	if _, exists := c.buildsByRef[ref]; !exists {
		c.buildsByRef[ref] = make(map[buildKey]*Build)
	}
	c.buildsByRef[ref][key] = &build

	return nil
}

// Store commit in cache. If a commit with the same SHA exists, merge
// both commits.
func (c *Cache) SaveCommit(ref string, commit Commit) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if previousCommit, exists := c.commitsByRef[ref]; exists {
		if previousCommit.Sha != commit.Sha {
			delete(c.buildsByRef, ref)
		}

		previousBranches := make(map[string]struct{})
		for _, b := range previousCommit.Branches {
			previousBranches[b] = struct{}{}
		}
		for _, b := range commit.Branches {
			if _, exists := previousBranches[b]; !exists {
				previousCommit.Branches = append(previousCommit.Branches, b)
			}
		}

		previousTags := make(map[string]struct{})
		for _, t := range previousCommit.Tags {
			previousTags[t] = struct{}{}
		}

		for _, t := range commit.Tags {
			if _, exists := previousTags[t]; !exists {
				previousCommit.Tags = append(previousCommit.Tags, t)
			}
		}
		c.commitsByRef[ref] = previousCommit
	} else {
		c.commitsByRef[ref] = commit
	}
}

func (c Cache) Commit(ref string) (Commit, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	commit, exists := c.commitsByRef[ref]
	return commit, exists
}

// Store a job in cache. Parent build and stage must already exist.
func (c *Cache) SaveJob(accountID string, buildID string, stageID int, job Job) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	key := buildKey{
		AccountID: accountID,
		BuildID:   buildID,
	}
	build, exists := c.buildsByKey[key]
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
		if build.Stages == nil {
			return fmt.Errorf("build has no stage %d", stageID)
		}
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
	builds := make([]Build, 0, len(c.buildsByKey))
	for _, build := range c.buildsByKey {
		builds = append(builds, *build)
	}

	return builds
}

func (c Cache) BuildsByRef(ref string) []Build {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	builds := make([]Build, 0, len(c.buildsByRef[ref]))
	for _, build := range c.buildsByRef[ref] {
		builds = append(builds, *build)
	}

	return builds
}

// Poll provider at increasing interval for information about the CI pipeline identified by the URL
// u. A message is sent on the channel 'updates' each time the cache is updated with new information
// for this specific pipeline.
func (c *Cache) monitorPipeline(ctx context.Context, p CIProvider, u string, ref string, updates chan<- time.Time) error {
	b := backoff.ExponentialBackOff{
		InitialInterval:     5 * time.Second,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         1 * time.Minute,
		MaxElapsedTime:      0,
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

		switch err := c.SaveBuild(ref, build); err {
		case nil:
			go func() {
				select {
				case updates <- time.Now():
				case <-ctx.Done():
				}
			}()
			// If SaveBuild() does not return an error then the build object we just saved
			// differs from the previous one. This most likely means the pipeline is
			// currently running so reset the backoff object.
			b.Reset()
		case ErrObsoleteBuild:
			// This error means that the build object we wanted to save was last updated by
			// the CI provider at the same time or before the one already saved in cache with the
			// same ID, so cache.SaveBuild rejected or request to store our build.
			// It's OK. In particular, since builds returned by BuildFromURL never contain
			// logs, it prevents us from overwriting a build that may have logs (these would have
			// been committed to the cache by a call to cache.SaveJob() after the user asks to
			// view the logs of a job) by a build with no log.
		default:
			return err
		}
	}

	return nil
}

// Ask all providers to monitor the CI pipeline identified by the URL u. A message is sent on the
// channel 'updates' each time the cache is updated with new information for this specific pipeline.
// If no provider is able to handle the specified URL, ErrUnknownPipelineURL is returned.
func (c *Cache) broadcastMonitorPipeline(ctx context.Context, u string, ref string, updates chan<- time.Time) error {
	wg := sync.WaitGroup{}
	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	for _, p := range c.ciProvidersById {
		wg.Add(1)
		go func(p CIProvider) {
			defer wg.Done()
			// Almost all calls will return immediately with ErrUnknownPipelineURL. Other calls won't,
			// meaning these providers can handle the URL they've been given. These calls
			// will run longer or possibly never return unless their context is canceled or
			// they encounter an error.
			err := c.monitorPipeline(ctx, p, u, ref, updates)
			if err != nil {
				if err != ErrUnknownPipelineURL && err != context.Canceled {
					err = fmt.Errorf("provider %s: monitorPipeline failed with %v (%s)", p.ID(), err, u)
				}
				errc <- err
			}
		}(p)
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	var err error
	var n int
	for e := range errc {
		if e != nil {
			// Only report ErrUnknownPipelineURL if all providers returned this error.
			if e == ErrUnknownPipelineURL {
				if n++; n < len(c.ciProvidersById) {
					continue
				}
			}

			if err == nil {
				cancel()
				err = e
			}
		}
	}

	return err
}

// Ask all providers to monitor the statuses of 'ref'. The URL of each status is written on the
// channel urlc once. If no provider is able to handle the specified URL, ErrUnknownRepositoryURL
// is returned.
func (c *Cache) broadcastMonitorRefStatus(ctx context.Context, repo string, ref string, commitc chan<- Commit) error {
	originURL, commit, err := GitOriginURL(repo, ref)
	var repositoryURL string
	switch err {
	case nil:
		select {
		case commitc <- commit:
		case <-ctx.Done():
			return ctx.Err()
		}
		ref = commit.Sha
		repositoryURL = originURL
	case ErrUnknownGitReference:
		// The reference was not found but we the repository was and we now know the URL of origin
		repositoryURL = originURL
	case ErrUnknownRepositoryURL:
		// Do nothing. The path does not refer to a local repository so it must be resolved
		// by a SourceProvider
		repositoryURL = repo
	default:
		return err
	}

	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	wg := sync.WaitGroup{}
	for _, p := range c.sourceProviders {
		wg.Add(1)
		go func(p SourceProvider) {
			defer wg.Done()
			errc <- monitorRefStatuses(ctx, p, repositoryURL, ref, commitc)
		}(p)
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	var n int
	var canceled = false
	for e := range errc {
		if !canceled {
			// ErrUnknownRepositoryURL and ErrUnknownGitReference are returned if
			// all providers fail with one of these errors
			switch e {
			case ErrUnknownRepositoryURL:
				n++
				if err == nil {
					err = e
				}
			case ErrUnknownGitReference:
				n++
				// ErrUnknownGitReference must be returned over ErrUnknownRepositoryURL
				// since it means the repository was found but the reference was not
				if err == nil || err == ErrUnknownRepositoryURL {
					err = e
				}
			default:
				// Artificially trigger cancellation
				n = len(c.sourceProviders)
				err = e
			}

			if canceled = n >= len(c.sourceProviders); canceled {
				cancel()
				err = e
			}
		}
	}

	return err
}

// Monitor CI pipelines associated to the git reference 'ref'. Every time the cache is
// updated with new data, a message is sent on the 'updates' channel.
// This function may return ErrUnknownRepositoryURL if none of the source providers is
// able to handle 'repositoryURL'.
func (c *Cache) MonitorPipelines(ctx context.Context, repo string, ref string, updates chan<- time.Time) error {
	commitc := make(chan Commit)
	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(commitc)
		// This gives us a stream of commits with a 'Statuses' attribute that may contain
		// URLs refering to CI pipelines
		errc <- c.broadcastMonitorRefStatus(ctx, repo, ref, commitc)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		urls := make(map[string]struct{})
		// Ask for monitoring of each URL
		for commit := range commitc {
			c.SaveCommit(ref, commit)
			select {
			case updates <- time.Now():
				// Do nothing
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			}
			for _, u := range commit.Statuses {
				if _, exists := urls[u]; !exists {
					wg.Add(1)
					go func(u string) {
						defer wg.Done()
						err := c.broadcastMonitorPipeline(ctx, u, ref, updates)
						// Ignore ErrUnknownPipelineURL. This error means that we don't integrate
						// with the application that created that particular URL. No need to report
						// this up the chain, though it's nice to know our request couldn't be handled.
						if err != ErrUnknownPipelineURL {
							errc <- err
							return
						}
					}(u)
					urls[u] = struct{}{}
				}
			}
		}
	}()

	go func() {
		wg.Wait()
		close(errc)
	}()

	var err error
	for e := range errc {
		if e != nil && err == nil {
			cancel()
			err = e
		}
	}

	return err
}

func (c *Cache) fetchBuild(accountID string, buildID string) (Build, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	build, exists := c.buildsByKey[buildKey{
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
