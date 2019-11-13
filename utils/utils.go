package utils

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gopkg.in/src-d/go-git.v4"
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

func RepositorySlugFromURL(repositoryURL string) (string, error) {
	// Turn "git@host:path.git" into "host/path" so that it is compatible with url.Parse()
	if strings.HasPrefix(repositoryURL, "git@") {
		repositoryURL = strings.TrimPrefix(repositoryURL, "git@")
		repositoryURL = strings.Replace(repositoryURL, ":", "/", 1)
	}
	repositoryURL = strings.TrimSuffix(repositoryURL, ".git")

	u, err := url.Parse(repositoryURL)
	if err != nil {
		return "", err
	}

	components := strings.Split(u.Path, "/")
	if len(components) < 3 {
		err := fmt.Errorf("invalid repository path: %q (expected at least three components)",
			u.Path)
		return "", err
	}

	return strings.Join(components[1:3], "/"), nil
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

func GitOriginURL(path string) (string, error) {
	r, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return "", err
	}

	remote, err := r.Remote("origin")
	if err != nil {
		return "", err
	}

	if len(remote.Config().URLs) == 0 {
		return "", fmt.Errorf("GIT repository %q: remote 'origin' has no associated URL", path)
	}

	return remote.Config().URLs[0], nil
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
