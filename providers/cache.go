package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/cistern/tui"
	"github.com/nbedos/cistern/utils"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
)

var ErrUnknownRepositoryURL = errors.New("unknown repository url")
var ErrUnknownPipelineURL = errors.New("unknown pipeline url")
var ErrUnknownGitReference = errors.New("unknown git reference")

type CIProvider interface {
	// Unique identifier of the Provider instance among all other instances
	ID() string
	// Host part of the url of the Provider
	Host() string
	// Display nName of the Provider
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
func monitorRefStatuses(ctx context.Context, p SourceProvider, b backoff.ExponentialBackOff, url string, ref string, commitc chan<- Commit) error {
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
			b.Reset()
		}
	}

	return nil
}

type State string

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

func (s State) IsActive() bool {
	return s == Pending || s == Running
}

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

func (s State) merge(other State) State {
	if statePrecedence[s] > statePrecedence[other] {
		return s
	}
	return other
}

func Aggregate(steps []Step) Step {
	switch len(steps) {
	case 0:
		return Step{}
	case 1:
		return steps[0]
	}

	first, last := steps[0], Aggregate(steps[1:])
	for _, p := range []*Step{&first, &last} {
		if p.AllowFailure && (p.State == Canceled || p.State == Failed) {
			p.State = Passed
		}
	}

	s := Step{
		State:      first.State.merge(last.State),
		StartedAt:  utils.MinNullTime(first.StartedAt, last.StartedAt),
		FinishedAt: utils.MaxNullTime(first.FinishedAt, last.FinishedAt),
		Children:   steps,
	}

	s.CreatedAt = first.CreatedAt
	if last.CreatedAt.Before(s.CreatedAt) {
		s.CreatedAt = last.CreatedAt
	}
	s.Duration = utils.NullSub(s.FinishedAt, s.StartedAt)
	s.UpdatedAt = first.UpdatedAt
	if last.UpdatedAt.After(s.UpdatedAt) {
		s.UpdatedAt = last.UpdatedAt
	}

	return s
}

type Provider struct {
	ID   string
	Name string
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

type GitStyle struct {
	Location *time.Location
	SHA      tui.StyleTransform
	Head     tui.StyleTransform
	Branch   tui.StyleTransform
	Tag      tui.StyleTransform
}

func (c Commit) StyledStrings(conf GitStyle) []tui.StyledString {
	var title tui.StyledString
	commit := tui.NewStyledString(fmt.Sprintf("commit %s", c.Sha), conf.SHA)
	if len(c.Branches) > 0 || len(c.Tags) > 0 {
		refs := make([]tui.StyledString, 0, len(c.Branches)+len(c.Tags))
		for _, tag := range c.Tags {
			refs = append(refs, tui.NewStyledString(fmt.Sprintf("tag: %s", tag), conf.Tag))
		}
		for _, branch := range c.Branches {
			if branch == c.Head {
				var s tui.StyledString
				s.Append("HEAD -> ", conf.Head)
				s.Append(branch, conf.Branch)
				refs = append([]tui.StyledString{s}, refs...)
			} else {
				refs = append(refs, tui.NewStyledString(branch, conf.Branch))
			}
		}

		title = tui.Join([]tui.StyledString{
			commit,
			tui.NewStyledString(" (", conf.SHA),
			tui.Join(refs, tui.NewStyledString(", ", conf.SHA)),
			tui.NewStyledString(")", conf.SHA),
		}, tui.NewStyledString(""))
	} else {
		title = commit
	}

	texts := []tui.StyledString{
		title,
		tui.NewStyledString(fmt.Sprintf("Author: %s", c.Author)),
		tui.NewStyledString(fmt.Sprintf("Date: %s", c.Date.Truncate(time.Second).In(conf.Location).String())),
		tui.NewStyledString(""),
	}
	for _, line := range strings.Split(c.Message, "\n") {
		texts = append(texts, tui.NewStyledString("    "+line))
		break
	}

	return texts
}

func RemotesAndCommit(path string, ref string) ([]string, Commit, error) {
	// If a path does not refer to an existing file or directory, go-git will continue
	// running and will walk its way up the directory structure looking for a .git repository.
	// This is not ideal for us since running 'cistern -r github.com/owner/repo' from
	// /home/user/localrepo will make go-git look for a .git repository in
	// /home/user/localrepo/github.com/owner/repo which will inevitably lead to
	// /home/user/localrepo which is not what the user expected since the user was
	// referring to the online repository https://github.com/owner/repo. So instead
	// we bail out early if the path is invalid, meaning it's not a local path but a url.
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			err = ErrUnknownRepositoryURL
		}
		return nil, Commit{}, err
	}

	r, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, Commit{}, err
	}

	head, err := r.Head()
	if err != nil {
		return nil, Commit{}, err
	}

	var hash plumbing.Hash
	if ref == "HEAD" {
		hash = head.Hash()
	} else {
		if p, err := r.ResolveRevision(plumbing.Revision(ref)); err == nil {
			hash = *p
		} else {
			// Ideally we'd take this path only for certain error cases but some errors
			// from go-git that should not be fatal to us are internal making it impossible
			// to test against them.

			// The failure of ResolveRevision may be due to go-git failure to resolve an
			// abbreviated SHA. Abbreviated SHAs are quite useful so, for now, circumvent the
			// problem by using the local git binary.
			cmd := exec.Command("git", "-C", path, "show", ref, "--pretty=format:%H")
			bs, err := cmd.Output()
			if err != nil {
				return nil, Commit{}, ErrUnknownGitReference
			}

			hash = plumbing.NewHash(strings.SplitN(string(bs), "\n", 2)[0])
		}

	}
	commit, err := r.CommitObject(hash)
	if err != nil {
		return nil, Commit{}, err
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
		return nil, Commit{}, err
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
		return nil, Commit{}, err
	}

	remotes, err := r.Remotes()
	if err != nil {
		return nil, Commit{}, err
	}
	remoteURLs := make([]string, 0)
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
					remoteURLs = append(remoteURLs, u)
				}
			}
		} else {
			remoteURLs = append(remoteURLs, remote.Config().URLs...)
		}
	}

	return remoteURLs, c, nil
}

type StepType int

const (
	StepPipeline = iota
	StepStage
	StepJob
	StepTask
)

type Log struct {
	Key     string
	Content utils.NullString
}

type Step struct {
	ID           string
	Name         string
	Type         StepType
	State        State
	AllowFailure bool
	CreatedAt    time.Time
	StartedAt    utils.NullTime
	FinishedAt   utils.NullTime
	UpdatedAt    time.Time
	Duration     utils.NullDuration
	WebURL       utils.NullString
	Log          Log
	Children     []Step
}

func (s Step) Diff(other Step) string {
	return cmp.Diff(s, other)
}

func (s Step) Map(f func(Step) Step) Step {
	s = f(s)

	for i, child := range s.Children {
		s.Children[i] = child.Map(f)
	}

	return s
}

const (
	ColumnRef tui.ColumnID = iota
	ColumnPipeline
	ColumnType
	ColumnState
	ColumnCreated
	ColumnStarted
	ColumnFinished
	ColumnUpdated
	ColumnDuration
	ColumnName
	ColumnWebURL
	ColumnAllowedFailure
)

func (s Step) NodeID() interface{} {
	return s.ID
}

func (s Step) NodeChildren() []tui.TableNode {
	children := make([]tui.TableNode, 0)
	for _, child := range s.Children {
		children = append(children, child)
	}

	return children
}

func (s Step) InheritedValues() []tui.ColumnID {
	return []tui.ColumnID{ColumnRef, ColumnPipeline}
}

type StepStyle struct {
	GitStyle
	Provider tui.StyleTransform
	Status   struct {
		Failed   tui.StyleTransform
		Canceled tui.StyleTransform
		Passed   tui.StyleTransform
		Running  tui.StyleTransform
		Pending  tui.StyleTransform
		Skipped  tui.StyleTransform
		Manual   tui.StyleTransform
	}
}

func (s Step) Values(v interface{}) map[tui.ColumnID]tui.StyledString {
	conf := v.(StepStyle)

	timeToString := func(t time.Time) string {
		return t.In(conf.Location).Truncate(time.Second).Format("Jan 2 15:04")
	}

	nullTimeToString := func(t utils.NullTime) tui.StyledString {
		s := "-"
		if t.Valid {
			s = timeToString(t.Time)
		}
		return tui.NewStyledString(s)
	}

	var typeChar string
	switch s.Type {
	case StepPipeline:
		typeChar = "P"
	case StepStage:
		typeChar = "S"
	case StepJob:
		typeChar = "J"
	case StepTask:
		typeChar = "T"
	}

	state := tui.NewStyledString(string(s.State))
	switch s.State {
	case Failed:
		state.Apply(conf.Status.Failed)
	case Canceled:
		state.Apply(conf.Status.Canceled)
	case Passed:
		state.Apply(conf.Status.Passed)
	case Running:
		state.Apply(conf.Status.Running)
	case Pending:
		state.Apply(conf.Status.Pending)
	case Skipped:
		state.Apply(conf.Status.Skipped)
	case Manual:
		state.Apply(conf.Status.Manual)
	}

	webURL := "-"
	if s.WebURL.Valid {
		webURL = s.WebURL.String
	}

	allowedFailure := "no"
	if s.AllowFailure {
		allowedFailure = "yes"
	}
	return map[tui.ColumnID]tui.StyledString{
		ColumnType:           tui.NewStyledString(typeChar),
		ColumnState:          state,
		ColumnAllowedFailure: tui.NewStyledString(allowedFailure),
		ColumnCreated:        tui.NewStyledString(timeToString(s.CreatedAt)),
		ColumnStarted:        nullTimeToString(s.StartedAt),
		ColumnFinished:       nullTimeToString(s.FinishedAt),
		ColumnUpdated:        tui.NewStyledString(timeToString(s.UpdatedAt)),
		ColumnDuration:       tui.NewStyledString(s.Duration.String()),
		ColumnName:           tui.NewStyledString(s.Name),
		ColumnWebURL:         tui.NewStyledString(webURL),
	}
}

type GitReference struct {
	SHA   string
	Ref   string
	IsTag bool
}

type Pipeline struct {
	Number       string
	providerID   string
	ProviderHost string
	ProviderName string
	GitReference
	Step
}

func (p Pipeline) Diff(other Pipeline) string {
	return cmp.Diff(p, other, cmp.AllowUnexported(Pipeline{}, Step{}))
}

type Pipelines []Pipeline

func (ps Pipelines) Diff(others Pipelines) string {
	return cmp.Diff(ps, others, cmp.AllowUnexported(Pipeline{}, Step{}))
}

// Return step identified by stepIDs
func (p Pipeline) getStep(stepIDs []string) (Step, bool) {
	step := p.Step
	for _, id := range stepIDs {
		exists := false
		for _, childStep := range step.Children {
			if childStep.ID == id {
				exists = true
				step = childStep
				break
			}
		}
		if !exists {
			return Step{}, false
		}
	}

	return step, true
}

type PipelineKey struct {
	ProviderHost string
	ID           string
}

func (p Pipeline) Key() PipelineKey {
	return PipelineKey{
		ProviderHost: p.ProviderHost,
		ID:           p.Step.ID,
	}
}

func (p Pipeline) NodeID() interface{} {
	return p.Key()
}

func (p Pipeline) InheritedValues() []tui.ColumnID {
	return nil
}

func (p Pipeline) Values(v interface{}) map[tui.ColumnID]tui.StyledString {
	conf := v.(StepStyle)

	values := p.Step.Values(conf)

	number := p.Number
	if number == "" {
		number = p.ID
	}
	if _, err := strconv.Atoi(number); err == nil {
		number = "#" + number
	}
	values[ColumnPipeline] = tui.NewStyledString(number)

	name := tui.NewStyledString(p.ProviderName, conf.Provider)
	if p.Name != "" {
		name.Append(fmt.Sprintf(": %s", p.Name))
	}
	values[ColumnName] = name

	if p.IsTag {
		values[ColumnRef] = tui.NewStyledString(p.Ref, conf.Tag)
	} else {
		values[ColumnRef] = tui.NewStyledString(p.Ref, conf.Branch)
	}

	return values
}

func (s Step) Compare(t tui.TableNode, id tui.ColumnID, i interface{}) int {
	other := t.(Step)
	switch id {
	case ColumnType, ColumnState, ColumnAllowedFailure, ColumnName, ColumnWebURL:
		lhs, rhs := s.Values(i)[id].String(), other.Values(i)[id].String()
		if lhs < rhs {
			return -1
		} else if lhs == rhs {
			return 0
		} else {
			return 1
		}

	case ColumnCreated:
		if s.CreatedAt.Before(other.CreatedAt) {
			return -1
		} else if s.CreatedAt.Equal(other.CreatedAt) {
			return 0
		} else {
			return 1
		}

	case ColumnStarted, ColumnFinished:
		var v, vOther utils.NullTime
		if id == ColumnStarted {
			v = s.StartedAt
			vOther = other.StartedAt
		} else {
			v = s.FinishedAt
			vOther = other.FinishedAt
		}

		// Assume that Null values are attributed to events that will occur in the
		// future, so give them a maximal value
		if !v.Valid {
			v.Time = time.Unix(1<<62, 0)
		}

		if !vOther.Valid {
			vOther.Time = time.Unix(1<<62, 0)
		}

		if v.Time.Before(vOther.Time) {
			return -1
		} else if v.Time.Equal(vOther.Time) {
			return 0
		} else {
			return 1
		}

	case ColumnDuration:
		v := s.Duration
		vOther := other.Duration

		if !v.Valid {
			v.Duration = 1<<63 - 1
		}
		if !vOther.Valid {
			vOther.Duration = 1<<63 - 1
		}

		if v.Duration < vOther.Duration {
			return -1
		} else if v.Duration == vOther.Duration {
			return 0
		} else {
			return 1
		}

	default:
		return 0
	}
}

func (p Pipeline) Compare(other tui.TableNode, id tui.ColumnID, i interface{}) int {
	switch q := other.(Pipeline); id {
	case ColumnRef, ColumnPipeline, ColumnName:
		lhs, rhs := p.Values(i)[id].String(), other.Values(i)[id].String()
		if lhs < rhs {
			return -1
		} else if lhs == rhs {
			return 0
		} else {
			return 1
		}
	default:
		return p.Step.Compare(q.Step, id, i)
	}
}

type Cache struct {
	ciProvidersByID map[string]CIProvider
	sourceProviders []SourceProvider
	mutex           *sync.Mutex
	// All the following data structures must be accessed after acquiring mutex
	commitsByRef  map[string]Commit
	pipelineByKey map[PipelineKey]*Pipeline
	pipelineByRef map[string]map[PipelineKey]*Pipeline
}

type Configuration struct {
	GitLab []struct {
		Name              string   `toml:"name" default:"gitlab"`
		URL               string   `toml:"url"`
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
		client, err := NewGitLabClient(id, conf.Name, conf.URL, token, conf.RequestsPerSecond)
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

	return NewCache(ci, source), nil
}

var ErrNoProvider = errors.New("list of providers must not be empty")

func NewCache(CIProviders []CIProvider, sourceProviders []SourceProvider) Cache {
	providersByAccountID := make(map[string]CIProvider, len(CIProviders))
	for _, provider := range CIProviders {
		providersByAccountID[provider.ID()] = provider
	}

	return Cache{
		commitsByRef:    make(map[string]Commit),
		pipelineByKey:   make(map[PipelineKey]*Pipeline),
		pipelineByRef:   make(map[string]map[PipelineKey]*Pipeline),
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
func (c *Cache) SavePipeline(ref string, p Pipeline) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	existingBuild, exists := c.pipelineByKey[p.Key()]
	// updatedAt refers to the last update of the build and does not reflect an eventual
	// update of a job so default to always updating an active build
	if exists && !p.State.IsActive() && !p.UpdatedAt.After(existingBuild.UpdatedAt) {
		// Point ref to existingBuild
		if _, exists := c.pipelineByRef[ref]; !exists {
			c.pipelineByRef[ref] = make(map[PipelineKey]*Pipeline)
		}
		if c.pipelineByRef[ref][p.Key()] == existingBuild {
			return ErrObsoleteBuild
		}
		c.pipelineByRef[ref][p.Key()] = existingBuild
		return nil
	}

	c.pipelineByKey[p.Key()] = &p
	// Point ref to new build
	if _, exists := c.pipelineByRef[ref]; !exists {
		c.pipelineByRef[ref] = make(map[PipelineKey]*Pipeline)
	}
	c.pipelineByRef[ref][p.Key()] = &p

	return nil
}

// Store commit in  If a commit with the same SHA exists, merge
// both commits.
func (c *Cache) SaveCommit(ref string, commit Commit) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if previousCommit, exists := c.commitsByRef[ref]; exists {
		if previousCommit.Sha != commit.Sha {
			delete(c.pipelineByRef, ref)
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

func (c Cache) Pipelines(ref string) []Pipeline {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	pipelines := make([]Pipeline, 0, len(c.pipelineByRef[ref]))
	for _, p := range c.pipelineByRef[ref] {
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
func (c *Cache) monitorPipeline(ctx context.Context, p CIProvider, u string, ref string, updates chan<- time.Time) error {
	b := backoff.ExponentialBackOff{
		InitialInterval:     10 * time.Second,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         2 * time.Minute,
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

		pipeline, err := p.BuildFromURL(ctx, u)
		if err != nil {
			return err
		}
		pipeline.providerID = p.ID()
		pipeline.ProviderHost = p.Host()
		pipeline.ProviderName = p.Name()

		switch err := c.SavePipeline(ref, pipeline); err {
		case nil:
			go func() {
				select {
				case updates <- time.Now():
				case <-ctx.Done():
				}
			}()
			// If SavePipeline() does not return an error then the build object we just saved
			// differs from the previous one. This most likely means the pipeline is
			// currently running so reset the backoff object.
			b.Reset()
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

		if !pipeline.State.IsActive() {
			break
		}
	}

	return nil
}

// Ask all providers to monitor the CI pipeline identified by the url u. A message is sent on the
// channel 'updates' each time the cache is updated with new information for this specific pipeline.
// If no Provider is able to handle the specified url, ErrUnknownPipelineURL is returned.
func (c *Cache) broadcastMonitorPipeline(ctx context.Context, u string, ref string, updates chan<- time.Time) error {
	wg := sync.WaitGroup{}
	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	for _, p := range c.ciProvidersByID {
		wg.Add(1)
		go func(p CIProvider) {
			defer wg.Done()
			// Almost all calls will return immediately with ErrUnknownPipelineURL. Other calls won't,
			// meaning these providers can handle the url they've been given. These calls
			// will run longer or possibly never return unless their context is canceled or
			// they encounter an error.
			err := c.monitorPipeline(ctx, p, u, ref, updates)
			if err != nil {
				if err != ErrUnknownPipelineURL && err != context.Canceled {
					err = fmt.Errorf("Provider %s: monitorPipeline failed with %v (%s)", p.ID(), err, u)
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
func (c *Cache) broadcastMonitorRefStatus(ctx context.Context, repo string, ref string, commitc chan<- Commit, b backoff.ExponentialBackOff) error {
	repositoryURLs, commit, err := RemotesAndCommit(repo, ref)
	switch err {
	case nil:
		select {
		case commitc <- commit:
		case <-ctx.Done():
			return ctx.Err()
		}
		ref = commit.Sha
	case ErrUnknownRepositoryURL:
		// The path does not refer to a local repository so it probably is a url
		repositoryURLs = []string{repo}
	default:
		return err
	}

	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	wg := sync.WaitGroup{}
	requestCount := 0
	// FIXME Deduplicate repositoryURLs
	for _, u := range repositoryURLs {
		for _, p := range c.sourceProviders {
			requestCount++
			wg.Add(1)
			go func(p SourceProvider, u string) {
				defer wg.Done()
				errc <- monitorRefStatuses(ctx, p, b, u, ref, commitc)
			}(p, u)
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
func (c *Cache) MonitorPipelines(ctx context.Context, repositoryURL string, ref string, updates chan<- time.Time) error {
	commitc := make(chan Commit)
	errc := make(chan error)
	ctx, cancel := context.WithCancel(ctx)
	wg := sync.WaitGroup{}

	b := backoff.ExponentialBackOff{
		InitialInterval:     10 * time.Second,
		RandomizationFactor: backoff.DefaultRandomizationFactor,
		Multiplier:          backoff.DefaultMultiplier,
		MaxInterval:         2 * time.Minute,
		MaxElapsedTime:      0,
		Clock:               backoff.SystemClock,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(commitc)
		// This gives us a stream of commits with a 'Statuses' attribute that may contain
		// URLs refering to CI pipelines
		errc <- c.broadcastMonitorRefStatus(ctx, repositoryURL, ref, commitc, b)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		urls := make(map[string]struct{})
		// Ask for monitoring of each url
		for commit := range commitc {
			c.SaveCommit(ref, commit)
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
			for _, u := range commit.Statuses {
				if _, exists := urls[u]; !exists {
					wg.Add(1)
					go func(u string) {
						defer wg.Done()
						err := c.broadcastMonitorPipeline(ctx, u, ref, updates)
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
