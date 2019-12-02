package utils

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/nbedos/citop/text"
	"gopkg.in/src-d/go-git.v4"
	"gopkg.in/src-d/go-git.v4/plumbing"
)

func Modulo(a, b int) int {
	result := a % b
	if result < 0 {
		result += b
	}

	return result
}

func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func MinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func Bounded(a, lower, upper int) int {
	return MaxInt(lower, MinInt(a, upper))
}

type TreeNode interface {
	Children() []TreeNode
	Traversable() bool
	SetTraversable(traversable bool, recursive bool)
}

func DepthFirstTraversal(node TreeNode, traverseAll bool) []TreeNode {
	explored := make([]TreeNode, 0)
	toBeExplored := []TreeNode{node}

	for len(toBeExplored) > 0 {
		node = toBeExplored[len(toBeExplored)-1]
		toBeExplored = toBeExplored[:len(toBeExplored)-1]
		if traverseAll || node.Traversable() {
			children := node.Children()
			for i := len(children) - 1; i >= 0; i-- {
				toBeExplored = append(toBeExplored, children[i])
			}
		}

		explored = append(explored, node)
	}

	return explored
}

func RepoOwnerAndName(repositoryURL string) (string, string, error) {
	// Turn "git@host:path.git" into "host/path" so that it is compatible with url.Parse()
	if strings.HasPrefix(repositoryURL, "git@") {
		repositoryURL = strings.TrimPrefix(repositoryURL, "git@")
		repositoryURL = strings.Replace(repositoryURL, ":", "/", 1)
	}
	repositoryURL = strings.TrimSuffix(repositoryURL, ".git")

	u, err := url.Parse(repositoryURL)
	if err != nil {
		return "", "", err
	}

	components := strings.Split(u.Path, "/")
	if len(components) < 3 {
		err := fmt.Errorf("invalid repository path: %q (expected at least three components)",
			u.Path)
		return "", "", err
	}

	return components[1], components[2], nil
}

func Prefix(s string, prefix string) string {
	builder := strings.Builder{}
	for _, line := range strings.Split(s, "\n") {
		builder.WriteString(fmt.Sprintf("%s%s\n", prefix, line))
	}

	return builder.String()
}

type NullString struct {
	Valid  bool
	String string
}

type NullTime struct {
	Valid bool
	Time  time.Time
}

func NullTimeFromTime(t *time.Time) NullTime {
	if t == nil {
		return NullTime{}
	}
	return NullTime{
		Time:  *t,
		Valid: true,
	}
}

func NullTimeFromString(s string) (t NullTime, err error) {
	if s != "" {
		t.Time, err = time.Parse(time.RFC3339, s)
		t.Valid = err == nil
	}

	return
}

func MinNullTime(times ...NullTime) NullTime {
	result := NullTime{}
	for _, t := range times {
		if result.Valid {
			if t.Valid && t.Time.Before(result.Time) {
				result = t
			}
		} else {
			result = t
		}
	}
	return result
}

func MaxNullTime(times ...NullTime) NullTime {
	result := NullTime{}
	for _, t := range times {
		if result.Valid {
			if t.Valid && t.Time.After(result.Time) {
				result = t
			}
		} else {
			result = t
		}
	}
	return result
}

var deleteEraseInLine = regexp.MustCompile(".*\x1b\\[0K")
var deleteUntilCarriageReturn = regexp.MustCompile(`.*\r([^\r\n])`)

func PostProcess(line string) string {
	tmp := deleteEraseInLine.ReplaceAllString(line, "")
	return deleteUntilCarriageReturn.ReplaceAllString(tmp, "$1")
}

type ANSIStripper struct {
	writer io.WriteCloser
	buffer bytes.Buffer
}

func NewANSIStripper(w io.WriteCloser) ANSIStripper {
	return ANSIStripper{writer: w}
}

func (a ANSIStripper) Write(p []byte) (int, error) {
	var (
		line []byte
		err  error
	)
	a.buffer.Write(p)
	for {
		line, err = a.buffer.ReadBytes('\n')
		if err != nil {
			break
		}

		s := PostProcess(string(line))
		if _, err := a.writer.Write([]byte(s)); err != nil {
			return 0, err
		}
	}
	a.buffer.Write(line)

	return len(p), nil
}

func (a ANSIStripper) Close() error {
	s := a.buffer.String()
	if len(s) > 0 {
		if !strings.HasSuffix(s, "\n") {
			s = s + "\n"
		}
		processed := PostProcess(s)
		if _, err := a.writer.Write([]byte(processed)); err != nil {
			return err
		}
	}

	return a.writer.Close()
}

type Commit struct {
	Sha      string
	Author   string
	Date     time.Time
	Message  string
	Branches []string
	Tags     []string
	Head     string
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
		text.NewStyledString(fmt.Sprintf("Date: %s", c.Date.Truncate(time.Second).String())),
		text.NewStyledString(""),
	}
	for _, line := range strings.Split(c.Message, "\n") {
		texts = append(texts, text.NewStyledString("    "+line))
		break
	}

	return texts
}

func GitOriginURL(path string, sha string) (string, Commit, error) {
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

	head, err := r.Head()
	if err != nil {
		return "", Commit{}, err
	}

	var hash plumbing.Hash
	if sha == "HEAD" {
		hash = head.Hash()
	} else {
		hash = plumbing.NewHash(sha)
	}
	commit, err := r.CommitObject(hash)
	switch err {
	case nil:
		// Do nothing
	case plumbing.ErrObjectNotFound:
		// go-git cannot resolve a revision from an abbreviated SHA. This is quite
		// useful so, for now, circumvent the problem by using the local git binary.
		cmd := exec.Command("git", "show", sha, "--pretty=format:%H")
		bs, err := cmd.Output()
		if err != nil {
			// FIXME There may also be multiple commit matching the abbreviated sha
			return "", Commit{}, plumbing.ErrObjectNotFound
		}

		hash = plumbing.NewHash(strings.SplitN(string(bs), "\n", 2)[0])
		commit, err = r.CommitObject(hash)
		if err != nil {
			return "", Commit{}, err
		}
	default:
		return "", Commit{}, err
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
		return "", Commit{}, err
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
		case ref.Name().IsBranch():
			c.Branches = append(c.Branches, ref.Name().Short())
		}

		return nil
	})
	if err != nil {
		return "", Commit{}, err
	}

	return remote.Config().URLs[0], c, nil
}

type NullDuration struct {
	Valid    bool
	Duration time.Duration
}

func (d NullDuration) String() string {
	if !d.Valid {
		return "-"
	}

	minutes := d.Duration / time.Minute
	seconds := (d.Duration - minutes*time.Minute) / time.Second

	if minutes == 0 {
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

func getEnvWithDefault(key string, d string) string {
	value := os.Getenv(key)
	if value == "" {
		value = d
	}
	return value
}

// Return possible locations of configuration files based on
// https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html
func XDGConfigLocations(filename string) []string {
	confHome := getEnvWithDefault("XDG_CONFIG_HOME", path.Join(os.Getenv("HOME"), ".config"))
	locations := []string{
		path.Join(confHome, filename),
	}

	dirs := getEnvWithDefault("XDG_CONFIG_DIRS", "/etc/xdg")
	for _, dir := range strings.Split(dirs, ":") {
		locations = append(locations, path.Join(dir, filename))
	}

	return locations
}
