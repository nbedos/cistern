package cache

import (
	"io/ioutil"
	"path"
	"testing"
)

func TestCache_connect(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestCacheDb")
	if err != nil {
		t.Error(err)
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
			cache := Cache{FilePath: tc.filePath}
			err := cache.connect(true)
			if err != tc.err {
				t.Errorf("TestCacheDb: test case '%s' failed (err='%s')", tc.name, err)
			} else if err == nil {
				defer func() {
					if errClose := cache.Close(); err == nil && errClose != nil {
						t.Error(errClose)
					}
				}()

				var count int
				err = cache.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' ;").Scan(&count)
				if count == 0 {
					t.Error("Empty database")
				}
			}
		})
	}
}

func TestCache_SaveBuild(t *testing.T) {
	dir, err := ioutil.TempDir("", "TestLoadBuilds")
	if err != nil {
		t.Error(err)
	}

	build := Build{
		Id:              42,
		State:           "passed",
		Repository:      "citop",
		RepoBuildNumber: "65",
		Commit: Commit{
			Id:         64,
			Sha:        "azertyuiop",
			Ref:        "feature/xxx",
			Message:    "abcd",
			CompareUrl: "https://example.com/commit",
		},
		Stages: []Stage{
			{
				Id:     54,
				Number: 0,
				Name:   "first stage",
				State:  "passed",
				Jobs: []Job{
					{
						Id:    62,
						State: "passed",
						Config: Config{
							Name:     "config_1",
							Os:       "linux",
							Distrib:  "arch",
							Language: "python",
							Env:      "VARIABLE=value",
							Json:     "{}",
						},
						Number: "362.1",
						Log:    "loglogloglogloglogloglog",
					},
				},
			},
		},
	}

	cache := Cache{FilePath: path.Join(dir, "cache.db")}
	err = cache.connect(true)
	if err != nil {
		t.Error(err)
	}
	defer func() {
		if errClose := cache.Close(); err == nil && errClose != nil {
			t.Error(errClose)
		}
	}()

	err = cache.SaveBuild(build)
	if err != nil {
		t.Error(err)
	}
}
