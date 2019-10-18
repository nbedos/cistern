package cache

import (
	"context"
	"fmt"
	"io/ioutil"
	"path"
	"testing"
	"time"
)

var inserters = []Inserter{
	Account{
		ID:       "gitlab",
		URL:      "http://api.example.com/v3",
		UserID:   "F54E34EA",
		Username: "username",
	},
	Repository{
		AccountID: "gitlab",
		URL:       "github.com/owner/repository",
		Name:      "repository",
		Owner:     "owner",
	},
	Build{
		AccountID:     "gitlab",
		ID:            3,
		RepositoryURL: "github.com/owner/repository",
		Commit: Commit{
			AccountID:     "gitlab",
			ID:            "22f1e814995f60357b6dd82c8a43d03cd2a4a634",
			RepositoryURL: "github.com/owner/repository",
			Message:       "Test GitLab API",
		},
		State:           Passed,
		RepoBuildNumber: "138",
		UpdatedAt:       time.Now(),
		Stages: map[int]*Stage{
			1: {
				AccountID: "gitlab",
				BuildID:   3,
				ID:        1,
				Name:      "tests",
				State:     Passed,
				Jobs: map[int]*Job{
					1: {
						Key: JobKey{
							AccountID: "gitlab",
							BuildID:   3,
							StageID:   1,
							ID:        1,
						},
						State: Passed,
						Name:  "Python 3.5",
						Log:   "log2",
					},
					2: {
						Key: JobKey{
							AccountID: "gitlab",
							BuildID:   3,
							StageID:   1,
							ID:        2,
						},
						State: Passed,
						Name:  "Python 3.6",
						Log:   "log3",
					},
				},
			},
		},
		Jobs: map[int]*Job{
			1: {
				Key: JobKey{
					AccountID: "gitlab",
					BuildID:   3,
					StageID:   0,
					ID:        1,
				},
				State: Failed,
				Name:  "Python 3.5",
				Log:   "log1",
			},
		},
	},
	Build{
		AccountID:     "gitlab",
		ID:            4,
		RepositoryURL: "github.com/owner/repository",
		Commit: Commit{
			AccountID:     "gitlab",
			ID:            "94ca3d6e146b83aedf7975ebeb207ce6d815071c",
			RepositoryURL: "github.com/owner/repository",
			Message:       "Minimalist table",
		},
		State:           Passed,
		RepoBuildNumber: "140",
		UpdatedAt:       time.Now(),
	},
	Build{
		AccountID:     "gitlab",
		ID:            5,
		RepositoryURL: "github.com/owner/repository",
		Commit: Commit{
			AccountID:     "gitlab",
			ID:            "94ca3d6e146b83aedf7975ebeb207ce6d815071c",
			RepositoryURL: "github.com/owner/repository",
			Message:       "Minimalist table",
		},
		State:           Failed,
		RepoBuildNumber: "141",
		UpdatedAt:       time.Now(),
	},
	Build{
		AccountID:     "gitlab",
		ID:            6,
		RepositoryURL: "github.com/owner/repository",
		Commit: Commit{
			AccountID:     "gitlab",
			ID:            "c104a8448e0f88ebb9586a450fc2309353cf116f",
			RepositoryURL: "github.com/owner/repository",
			Message:       "Add basic Travis support",
		},
		State:           Failed,
		RepoBuildNumber: "142",
		UpdatedAt:       time.Now(),
	},
	Build{
		AccountID:     "gitlab",
		ID:            7,
		RepositoryURL: "github.com/owner/repository",
		Commit: Commit{
			AccountID:     "gitlab",
			ID:            "c104a8448e0f88ebb9586a450fc2309353cf116f",
			RepositoryURL: "github.com/owner/repository",
			Message:       "Add basic Travis support",
		},
		State:           Failed,
		RepoBuildNumber: "143",
		UpdatedAt:       time.Now(),
	},
}

func TestNewCache(t *testing.T) {
	dir, err := ioutil.TempDir("", t.Name())
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name     string
		remove   bool
		filePath string
		err      error
	}{
		{
			name:     "Non existing file",
			filePath: path.Join(dir, "cache.db"),
			err:      nil,
		},
		{
			// Depends on the previous test case for file creation
			name:     "Existing file",
			filePath: path.Join(dir, "cache.db"),
			err:      nil,
		},
		{
			// Depends on the previous test case for file creation
			name:     "Existing file to be removed",
			remove:   true,
			filePath: path.Join(dir, "cache.db"),
			err:      nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cache, err := NewCache(tc.filePath, false, nil)
			if err != tc.err {
				t.Fatalf("test case '%s' failed (err='%s')", tc.name, err)
			}
			defer func() {
				if errClose := cache.Close(); errClose != nil {
					if err != nil {
						err = fmt.Errorf("test failed: %w (%v)", err, errClose)
					} else {
						err = errClose
					}
					t.Fatal(err)
				}
			}()

			var count int
			err = cache.db.
				QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' ;").
				Scan(&count)
			if count == 0 {
				t.Fatal("Empty database")
			}
		})
	}
}

func TestCache_Save(t *testing.T) {
	cache, err := TemporaryCache(context.Background(), t.Name(), inserters)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if errClose := cache.Close(); errClose != nil {
			if err != nil {
				err = fmt.Errorf("test failed: %w (%v)", err, errClose)
			} else {
				err = errClose
			}
			t.Fatal(err)
		}
	}()
}

func TestFetchJobs(t *testing.T) {
	ctx := context.Background()

	cache, err := TemporaryCache(ctx, t.Name(), inserters)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if errClose := cache.Close(); errClose != nil {
			if err != nil {
				err = fmt.Errorf("test failed: %w (%v)", err, errClose)
			} else {
				err = errClose
			}
			t.Fatal(err)
		}
	}()

	keys := []JobKey{
		{
			AccountID: "gitlab",
			BuildID:   3,
			StageID:   0,
			ID:        1,
		},
		{
			AccountID: "gitlab",
			BuildID:   3,
			StageID:   1,
			ID:        1,
		},
		{
			AccountID: "gitlab",
			BuildID:   3,
			StageID:   1,
			ID:        2,
		},
	}
	jobs, err := cache.FetchJobs(ctx, keys)
	if err != nil {
		t.Fatal(err)
	}

	if len(jobs) != len(keys) {
		t.Fail()
	}

	for i, job := range jobs {
		if job.Key != keys[i] {
			t.Fail()
		}
	}
}

func TestDumpTodir(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", t.Name())
	if err != nil {
		t.Fatal(err)
	}

	jobs := []Job{
		{
			Key: JobKey{
				AccountID: "gitlab",
				BuildID:   3,
				StageID:   0,
				ID:        1,
			},
			Log: "log1",
		},
		{
			Key: JobKey{
				AccountID: "gitlab",
				BuildID:   3,
				StageID:   1,
				ID:        1,
			},
			Log: "log2",
		},
		{
			Key: JobKey{
				AccountID: "gitlab",
				BuildID:   3,
				StageID:   1,
				ID:        2,
			},
			Log: "log3",
		},
	}
	paths, err := WriteLogs(jobs, tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(paths) != len(jobs) {
		t.Fatalf("expected %d file paths, got %d", len(jobs), len(paths))
	}

	for i := range paths {
		bs, err := ioutil.ReadFile(path.Join(tmpDir, paths[i]))
		if err != nil {
			t.Fatal(err)
		}

		if string(bs) != jobs[i].Log {
			t.Fatalf("invalid log content: expected '%s' but got '%s'", jobs[i].Log, string(bs))
		}
	}
}
