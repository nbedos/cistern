package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/cistern/tui"
	"github.com/nbedos/cistern/utils"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
	"gopkg.in/src-d/go-git.v4/plumbing/object"
)

var ErrUnknownRepositoryURL = errors.New("unknown repository url")
var ErrUnknownPipelineURL = errors.New("unknown pipeline url")
var ErrUnknownGitReference = errors.New("unknown git reference")

var defaultPollingStrategy = utils.PollingStrategy{
	InitialInterval: 10 * time.Second,
	Multiplier:      1.5,
	Randomizer:      0.25,
	MaxInterval:     120 * time.Second,
	Forever:         false,
}

type CIProvider interface {
	// Unique identifier of the Provider instance among all other instances
	ID() string
	// Host part of the url of the Provider
	Host() string
	// Display name of the Provider
	Name() string
	// FIXME Replace stepID by stepIDs
	Log(ctx context.Context, step Step) (string, error)
	BuildFromURL(ctx context.Context, u string) (Pipeline, error)
}

type SourceProvider interface {
	// Unique identifier of the Provider instance among all other instances
	ID() string
	RefStatuses(ctx context.Context, url string, ref string, sha string) ([]string, error)
	Commit(ctx context.Context, repo string, sha string) (Commit, error)
}

// Poll Provider at increasing interval for the url of statuses associated to "ref"
func monitorRefStatuses(ctx context.Context, p SourceProvider, s utils.PollingStrategy, remoteName string, url string, ref string, commitc chan<- Commit) error {
	commit, err := p.Commit(ctx, url, ref)
	if err != nil {
		return err
	}
	for i := range commit.Branches {
		if remoteName != "" {
			commit.Branches[i] = fmt.Sprintf("%s/%s", remoteName, commit.Branches[i])
		} else {
			commit.Branches[i] = commit.Branches[i]
		}
	}
	select {
	case commitc <- commit:
		// Do nothing
	case <-ctx.Done():
		return ctx.Err()
	}

	for waitTime := time.Duration(0); s.Forever || waitTime < s.MaxInterval; waitTime = s.NextInterval(waitTime) {
		select {
		case <-time.After(waitTime):
			// Do nothing
		case <-ctx.Done():
			return ctx.Err()
		}

		statuses, err := p.RefStatuses(ctx, url, ref, commit.Sha)
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
			waitTime = 0
		}
	}

	return nil
}

func References(path string, conf GitStyle) (tui.Suggestions, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			err = ErrUnknownRepositoryURL
		}
		return nil, err
	}

	r, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, err
	}

	references := make([]tui.Suggestion, 0)

	commIter, err := r.CommitObjects()
	if err != nil {
		return nil, err
	}
	if err := commIter.ForEach(func(c *object.Commit) error {
		references = append(references, tui.Suggestion{
			Value:        c.Hash.String(),
			DisplayValue: tui.NewStyledString(c.Hash.String(), conf.SHA),
			DisplayInfo:  tui.NewStyledString(strings.SplitN(c.Message, "\n", 2)[0]),
		})
		return nil
	}); err != nil {
		return nil, err
	}

	refIter, err := r.References()
	if err != nil {
		return nil, err
	}
	if err := refIter.ForEach(func(ref *plumbing.Reference) error {
		suggestion := tui.Suggestion{
			Value: ref.Name().Short(),
		}

		switch {
		case ref.Name().IsTag():
			suggestion.DisplayValue = tui.NewStyledString(suggestion.Value, conf.Tag)
		case ref.Name().IsBranch(), ref.Name().IsRemote():
			suggestion.DisplayValue = tui.NewStyledString(suggestion.Value, conf.Branch)
		case suggestion.Value == "HEAD":
			suggestion.DisplayValue = tui.NewStyledString(suggestion.Value, conf.Head)
		default:
			suggestion.DisplayValue = tui.NewStyledString(suggestion.Value)
		}

		c, err := r.CommitObject(ref.Hash())
		if err == plumbing.ErrObjectNotFound {
			return nil
		} else if err != nil {
			return err
		}
		suggestion.DisplayInfo = tui.NewStyledString(strings.SplitN(c.Message, "\n", 2)[0])

		references = append(references, suggestion)

		return nil
	}); err != nil {
		return nil, err
	}

	return references, nil
}

func ResolveCommit(path string, ref string) (Commit, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			err = ErrUnknownRepositoryURL
		}
		return Commit{}, err
	}

	r, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return Commit{}, err
	}

	head, err := r.Head()
	if err != nil {
		return Commit{}, err
	}

	var hash plumbing.Hash
	if ref == "HEAD" {
		hash = head.Hash()
	} else {
		if p, err := r.ResolveRevision(plumbing.Revision(ref)); err == nil {
			hash = *p
		} else {
			// The failure of ResolveRevision may be due to go-git failure to resolve an
			// abbreviated SHA. Abbreviated SHAs are quite useful so, for now, circumvent the
			// problem by using the local git binary.
			cmd := exec.Command("git", "-C", path, "show", ref, "--pretty=format:%H")
			bs, err := cmd.Output()
			if err != nil {
				return Commit{}, ErrUnknownGitReference
			}

			hash = plumbing.NewHash(strings.SplitN(string(bs), "\n", 2)[0])
		}
	}
	commit, err := r.CommitObject(hash)
	if err != nil {
		return Commit{}, err
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
		return Commit{}, err
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
		return Commit{}, err
	}

	return c, nil
}

func Remotes(path string) (map[string][]string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			err = ErrUnknownRepositoryURL
		}
		return nil, err
	}

	r, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, err
	}

	remotes, err := r.Remotes()
	if err != nil {
		return nil, err
	}
	remoteURLs := make(map[string][]string, 0)
	for _, remote := range remotes {
		// Try calling the local git binary to get the list of push URLs for
		// this remote.  For now this is better than what go-git offers since
		// it takes into account insteadOf and pushInsteadOf configuration
		// options. If it fails, use the url list provided by go-git.
		//
		// Remove this once the following issue is fixed:
		//    https://github.com/src-d/go-git/issues/1266/
		cmd := exec.Command("git", "remote", "get-url", "--push", "--all", remote.Config().Name)
		cmd.Dir = path
		if bs, err := cmd.Output(); err == nil {
			for _, u := range strings.Split(string(bs), "\n") {
				if u = strings.TrimSuffix(u, "\r"); u != "" {
					remoteURLs[remote.Config().Name] = append(remoteURLs[remote.Config().Name], u)
				}
			}
		} else {
			remoteURLs[remote.Config().Name] = remote.Config().URLs
		}
	}

	return remoteURLs, nil
}

type Cache struct {
	ciProvidersByID map[string]CIProvider
	sourceProviders []SourceProvider
	pollStrat       utils.PollingStrategy
	mutex           *sync.Mutex
	// All the following data structures must be accessed after acquiring mutex
	commitsByRef  map[string]Commit
	pipelineByKey map[PipelineKey]*Pipeline
	pipelineBySha map[string]map[PipelineKey]*Pipeline
}

type Configuration struct {
	Polling struct {
		InitialInterval int  `toml:"initial-interval"`
		MaxInterval     int  `toml:"max-interval"`
		Forever         bool `toml:"forever"`
	}
	GitLab []struct {
		Name              string   `toml:"name" default:"gitlab"`
		URL               string   `toml:"url"`
		SSHHost           string   `toml:"ssh-host"`
		Token             string   `toml:"token"`
		TokenFromProcess  []string `toml:"token-from-process"`
		RequestsPerSecond float64  `toml:"max-requests-per-second"`
	}
	GitHub []struct {
		Token            string   `toml:"token"`
		TokenFromProcess []string `toml:"token-from-process"`
	}
	CircleCI []struct {
		Name              string   `toml:"name" default:"circleci"`
		Token             string   `toml:"token"`
		TokenFromProcess  []string `toml:"token-from-process"`
		RequestsPerSecond float64  `toml:"max-requests-per-second"`
	}
	Travis []struct {
		Name              string   `toml:"name" default:"travis"`
		URL               string   `toml:"url"`
		Token             string   `toml:"token"`
		TokenFromProcess  []string `toml:"token-from-process"`
		RequestsPerSecond float64  `toml:"max-requests-per-second"`
	}
	AppVeyor []struct {
		Name              string   `toml:"name" default:"appveyor"`
		Token             string   `toml:"token"`
		TokenFromProcess  []string `toml:"token-from-process"`
		RequestsPerSecond float64  `toml:"max-requests-per-second"`
	}
	Azure []struct {
		Name              string   `toml:"name" default:"azure"`
		Token             string   `toml:"token"`
		TokenFromProcess  []string `toml:"token-from-process"`
		RequestsPerSecond float64  `toml:"max-requests-per-second"`
	}
}

func token(token string, process []string) (string, error) {
	if len(process) == 0 {
		return token, nil
	}
	if token != "" {
		return "", errors.New("at most one of \"token\" and \"token-from-process\" must be set")
	}

	cmd := exec.Command(process[0], process[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	bs, err := cmd.Output()
	return strings.Trim(string(bs), "\r\n"), err
}

func (c Configuration) ToCache(ctx context.Context) (Cache, error) {
	source := make([]SourceProvider, 0)
	ci := make([]CIProvider, 0)

	for i, conf := range c.GitLab {
		id := fmt.Sprintf("gitlab-%d", i)
		token, err := token(conf.Token, conf.TokenFromProcess)
		if err != nil {
			return Cache{}, err
		}
		client, err := NewGitLabClient(id, conf.Name, conf.URL, token, conf.RequestsPerSecond, conf.SSHHost)
		if err != nil {
			return Cache{}, err
		}
		source = append(source, client)
		ci = append(ci, client)
	}

	for i, conf := range c.GitHub {
		id := fmt.Sprintf("github-%d", i)
		token, err := token(conf.Token, conf.TokenFromProcess)
		if err != nil {
			return Cache{}, err
		}
		client := NewGitHubClient(ctx, id, &token)
		source = append(source, client)
	}

	for i, conf := range c.CircleCI {
		id := fmt.Sprintf("circleci-%d", i)
		token, err := token(conf.Token, conf.TokenFromProcess)
		if err != nil {
			return Cache{}, err
		}
		client := NewCircleCIClient(id, conf.Name, token, conf.RequestsPerSecond)
		ci = append(ci, client)
	}

	for i, conf := range c.AppVeyor {
		id := fmt.Sprintf("appveyor-%d", i)
		token, err := token(conf.Token, conf.TokenFromProcess)
		if err != nil {
			return Cache{}, err
		}
		client := NewAppVeyorClient(id, conf.Name, token, conf.RequestsPerSecond)
		ci = append(ci, client)
	}

	for i, conf := range c.Travis {
		id := fmt.Sprintf("travis-%d", i)
		token, err := token(conf.Token, conf.TokenFromProcess)
		if err != nil {
			return Cache{}, err
		}
		client, err := NewTravisClient(id, conf.Name, token, conf.URL, conf.RequestsPerSecond)
		if err != nil {
			return Cache{}, err
		}
		ci = append(ci, client)
	}

	for i, conf := range c.Azure {
		id := fmt.Sprintf("azure-%d", i)
		token, err := token(conf.Token, conf.TokenFromProcess)
		if err != nil {
			return Cache{}, err
		}
		client := NewAzurePipelinesClient(id, conf.Name, token, conf.RequestsPerSecond)
		ci = append(ci, client)
	}

	if len(ci) == 0 || len(source) == 0 {
		return Cache{}, ErrNoProvider
	}

	s, err := utils.NewPollingStrategy(c.Polling.InitialInterval, c.Polling.MaxInterval, c.Polling.Forever, defaultPollingStrategy)
	if err != nil {
		return Cache{}, err
	}

	return NewCache(ci, source, s), nil
}

var ErrNoProvider = errors.New("list of providers must not be empty")

func NewCache(CIProviders []CIProvider, sourceProviders []SourceProvider, strategy utils.PollingStrategy) Cache {
	providersByAccountID := make(map[string]CIProvider, len(CIProviders))
	for _, provider := range CIProviders {
		providersByAccountID[provider.ID()] = provider
	}

	return Cache{
		pollStrat:       strategy,
		commitsByRef:    make(map[string]Commit),
		pipelineByKey:   make(map[PipelineKey]*Pipeline),
		pipelineBySha:   make(map[string]map[PipelineKey]*Pipeline),
		mutex:           &sync.Mutex{},
		ciProvidersByID: providersByAccountID,
		sourceProviders: sourceProviders,
	}
}

var ErrObsoleteBuild = errors.New("build to save is older than current build in cache")

// Store build in  If a build from the same Provider and with the same ID is
// already stored in cache, it will be overwritten if the build to save is more recent
// than the build in  If the build to save is older than the build in cache,
// SavePipeline will return ErrObsoleteBuild.
func (c *Cache) SavePipeline(p Pipeline) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	existingBuild, exists := c.pipelineByKey[p.Key()]
	// updatedAt refers to the last update of the build and does not reflect an eventual
	// update of a job so default to always updating an active build
	if exists && !p.State.IsActive() && !p.UpdatedAt.After(existingBuild.UpdatedAt) {
		// Point ref to existingBuild
		if _, exists := c.pipelineBySha[p.SHA]; !exists {
			c.pipelineBySha[p.SHA] = make(map[PipelineKey]*Pipeline)
		}
		if c.pipelineBySha[p.SHA][p.Key()] == existingBuild {
			return ErrObsoleteBuild
		}
		c.pipelineBySha[p.SHA][p.Key()] = existingBuild
		return nil
	}

	c.pipelineByKey[p.Key()] = &p
	// Point ref to new build
	if _, exists := c.pipelineBySha[p.SHA]; !exists {
		c.pipelineBySha[p.SHA] = make(map[PipelineKey]*Pipeline)
	}
	c.pipelineBySha[p.SHA][p.Key()] = &p

	return nil
}

// Store commit in  If a commit with the same SHA exists, merge
// both commits.
func (c *Cache) SaveCommit(ref string, commit Commit) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if previousCommit, exists := c.commitsByRef[ref]; exists && previousCommit.Sha == commit.Sha {
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

func (c Cache) Pipelines(ref string) []Pipeline {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	commit, exists := c.commitsByRef[ref]
	if !exists {
		return nil
	}

	pipelines := make([]Pipeline, 0, len(c.pipelineBySha[ref]))
	for _, p := range c.pipelineBySha[commit.Sha] {
		pipelines = append(pipelines, *p)
	}

	sort.Slice(pipelines, func(i, j int) bool {
		pi := pipelines[i]
		pj := pipelines[j]
		return pi.ProviderHost < pj.ProviderHost || (pi.ProviderHost == pj.ProviderHost && pi.ID < pj.ID)
	})

	return pipelines
}

// Poll Provider at increasing interval for information about the CI pipeline identified by the url
// u. A message is sent on the channel 'updates' each time the cache is updated with new information
// for this specific pipeline.
func (c *Cache) monitorPipeline(ctx context.Context, pid string, u string, updates chan<- time.Time) error {
	p, exists := c.ciProvidersByID[pid]
	if !exists {
		return fmt.Errorf("cache does not contain any CI provider with ID %q", pid)
	}

	for waitTime, active := time.Duration(0), true; c.pollStrat.Forever || active; waitTime = c.pollStrat.NextInterval(waitTime) {
		select {
		case <-time.After(waitTime):
			// Do nothing
		case <-ctx.Done():
			return ctx.Err()
		}

		pipeline, err := p.BuildFromURL(ctx, u)
		if err != nil {
			return err
		}
		pipeline.providerID = p.ID()
		pipeline.ProviderHost = p.Host()
		pipeline.ProviderName = p.Name()

		switch err := c.SavePipeline(pipeline); err {
		case nil:
			if updates != nil {
				go func() {
					select {
					case updates <- time.Now():
					case <-ctx.Done():
					}
				}()
			}
			// If SavePipeline() does not return an error then the build object we just saved
			// differs from the previous one. This most likely means the pipeline is
			// currently running so reset the backoff object.
			waitTime = 0
		case ErrObsoleteBuild:
			// This error means that the build object we wanted to save was last updated by
			// the CI Provider at the same time or before the one already saved in cache with the
			// same ID, so SavePipeline rejected or request to store our build.
			// It's OK. In particular, since builds returned by BuildFromURL never contain
			// logs, it prevents us from overwriting a build that may have logs (these would have
			// been committed to the cache by a call to SaveJob() after the user asks to
			// view the logs of a job) by a build with no log.
		default:
			return err
		}

		active = pipeline.State.IsActive()
	}

	return nil
}

// Ask all providers to monitor the CI pipeline identified by the url u. A message is sent on the
// channel 'updates' each time the cache is updated with new information for this specific pipeline.
// If no Provider is able to handle the specified url, ErrUnknownPipelineURL is returned.
func (c *Cache) broadcastMonitorPipeline(ctx context.Context, u string, updates chan<- time.Time) error {
	wg := sync.WaitGroup{}
	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	for pid := range c.ciProvidersByID {
		wg.Add(1)
		go func(pid string) {
			defer wg.Done()
			// Almost all calls will return immediately with ErrUnknownPipelineURL. Other calls won't,
			// meaning these providers can handle the url they've been given. These calls
			// will run longer or possibly never return unless their context is canceled or
			// they encounter an error.
			if err := c.monitorPipeline(ctx, pid, u, updates); err != nil {
				if err != ErrUnknownPipelineURL && err != context.Canceled {
					err = fmt.Errorf("Provider %s: monitorPipeline failed with %v (%s)", pid, err, u)
				}
				errc <- err
			}
		}(pid)
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
				if n++; n < len(c.ciProvidersByID) {
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

// Ask all providers to monitor the statuses of 'ref'. The url of each status is written on the
// channel urlc once. If no Provider is able to handle the specified url, ErrUnknownRepositoryURL
// is returned.
func (c *Cache) broadcastMonitorRefStatus(ctx context.Context, repositoryURLs map[string][]string, ref string, commitc chan<- Commit) error {
	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	wg := sync.WaitGroup{}
	requestCount := 0
	// FIXME Deduplicate repositoryURLs
	for remoteName, remoteURLs := range repositoryURLs {
		for _, u := range remoteURLs {
			for _, p := range c.sourceProviders {
				requestCount++
				wg.Add(1)
				go func(p SourceProvider, u string) {
					defer wg.Done()
					errc <- monitorRefStatuses(ctx, p, c.pollStrat, remoteName, u, ref, commitc)
				}(p, u)
			}
		}
	}

	go func() {
		wg.Wait()
		close(errc)
	}()

	errCounts := struct {
		nil   int
		url   int
		ref   int
		other int
	}{}

	var canceled = false
	var err error
	for e := range errc {
		if !canceled {
			// ErrUnknownRepositoryURL and ErrUnknownGitReference are returned if
			// all providers fail with one of these errors
			switch e {
			case nil:
				errCounts.nil++
			case ErrUnknownRepositoryURL:
				errCounts.url++
			case ErrUnknownGitReference:
				errCounts.ref++
			default:
				// Artificially trigger cancellation
				errCounts.other += requestCount
			}

			canceled = (errCounts.nil + errCounts.url + errCounts.ref + errCounts.other) >= requestCount
			if canceled {
				cancel()
				switch {
				case errCounts.other > 0:
					err = e
				case errCounts.nil > 0:
					err = nil
				case errCounts.ref > 0:
					err = ErrUnknownGitReference
				case errCounts.url > 0:
					err = ErrUnknownRepositoryURL
				}
			}
		}
	}

	return err
}

// Monitor CI pipelines associated to the git reference 'ref'. Every time the cache is
// updated with new data, a message is sent on the 'updates' channel.
// This function may return ErrUnknownRepositoryURL if none of the source providers is
// able to handle 'repositoryURL'.
func (c *Cache) MonitorPipelines(ctx context.Context, repositoryURLs map[string][]string, ref Ref, updates chan<- time.Time) error {
	commitc := make(chan Commit)
	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(commitc)

		refOrSha := ref.Name
		if ref.Commit.Sha != "" {
			select {
			case commitc <- ref.Commit:
				// Do nothing
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			}
			refOrSha = ref.Commit.Sha
		}
		// This gives us a stream of commits with a 'Statuses' attribute that may contain
		// URLs referring to CI pipelines
		errc <- c.broadcastMonitorRefStatus(ctx, repositoryURLs, refOrSha, commitc)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		urls := make(map[string]struct{})
		// Ask for monitoring of each url
		for commit := range commitc {
			c.SaveCommit(ref.Name, commit)
			if updates != nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					select {
					case updates <- time.Now():
						// Do nothing
					case <-ctx.Done():
						// Do nothing
					}
				}()
			}
			for _, u := range commit.Statuses {
				if _, exists := urls[u]; !exists {
					wg.Add(1)
					go func(u string) {
						defer wg.Done()
						err := c.broadcastMonitorPipeline(ctx, u, updates)
						// Ignore ErrUnknownPipelineURL. This error means that we don't integrate
						// with the application that created that particular url. No need to report
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

func (c *Cache) Pipeline(key PipelineKey) (Pipeline, bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	p, exists := c.pipelineByKey[key]
	if !exists || p == nil {
		return Pipeline{}, false
	}

	return *p, true
}

func (c *Cache) Step(key PipelineKey, stepIDs []string) (Step, bool) {
	p, exists := c.Pipeline(key)
	if !exists {
		return Step{}, false
	}

	return p.getStep(stepIDs)
}

var ErrNoLogHere = errors.New("no log is associated to this row")

func (c *Cache) Log(ctx context.Context, key PipelineKey, stepIDs []string) (string, error) {
	var err error

	step, exists := c.Step(key, stepIDs)
	if !exists {
		return "", fmt.Errorf("no matching step for %v %v", key, stepIDs)
	}

	log := step.Log.Content.String
	if !step.Log.Content.Valid {
		pipeline, exists := c.Pipeline(key)
		if !exists {

		}
		provider, exists := c.ciProvidersByID[pipeline.providerID]
		if !exists {
			return "", fmt.Errorf("no matching Provider found in cache for account ID %q", pipeline.providerID)
		}

		log, err = provider.Log(ctx, step)
		if err != nil {
			return "", err
		}

		/*if !step.State.IsActive() {
			if err = c.SaveStep(pKey, stepIDs,accountID, buildID, stageID, job); err != nil {
				return err
			}
		}*/
	}

	if !strings.HasSuffix(log, "\n") {
		log = log + "\n"
	}

	return log, err
}
