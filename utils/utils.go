package utils

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
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

func RepositoryHostAndSlug(repositoryURL string) (string, string, error) {
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
	if u.Host == "" && !strings.Contains(repositoryURL, "://") {
		// example.com/aaa/bbb is parsed as url.url{Host: "", Path:"example.com/aaa/bbb"}
		// but we expect url.url{Host: "example.com", Path:"/aaa/bbb"}. Adding a scheme fixes this.
		//
		u, err = url.Parse("https://" + repositoryURL)
		if err != nil {
			return "", "", err
		}
	}

	if l := len(strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })); l < 2 {
		return "", "", fmt.Errorf("invalid repository path: %q (expected at least two path components)", repositoryURL)
	}

	return u.Hostname(), strings.Trim(u.Path, "/"), nil
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

type NullDuration struct {
	Valid    bool
	Duration time.Duration
}

func (d NullDuration) String() string {
	if !d.Valid {
		return "-"
	}

	hours := d.Duration / time.Hour
	minutes := (d.Duration - hours*time.Hour) / time.Minute
	seconds := (d.Duration - (hours*time.Hour + minutes*time.Minute)) / time.Second

	if hours != 0 {
		return fmt.Sprintf("%dh%02dm%02ds", hours, minutes, seconds)
	} else if minutes != 0 {
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	} else if seconds > 0 || d.Duration == 0 {
		return fmt.Sprintf("%ds", seconds)
	} else {
		return "<1s"
	}
}

func NullSub(after NullTime, before NullTime) NullDuration {
	return NullDuration{
		Valid:    after.Valid && before.Valid,
		Duration: after.Time.Sub(before.Time),
	}
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
