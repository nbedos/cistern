package cache

import (
	"io/ioutil"
	"path"
	"testing"
)

func TestCache_connect(t *testing.T) {
	dir, err := ioutil.TempDir("", t.Name())
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
				t.Errorf("test case '%s' failed (err='%s')", tc.name, err)
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

func TestCache_Save(t *testing.T) {
	dir, err := ioutil.TempDir("", t.Name())
	if err != nil {
		t.Error(err)
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

	stageId := 123
	inserters := []Inserter{
		Account{
			Id:       "gitlab account",
			Url:      "http://api.example.com/v3",
			UserId:   "F54E34EA",
			Username: "username",
		},
		Repository{
			Id:        42,
			AccountId: "gitlab account",
			Url:       "http://api.example.com/v3/repos/42",
			Name:      "namespace/slug",
		},
		Commit{
			AccountId:    "gitlab account",
			Id:           "22f1e814995f60357b6dd82c8a43d03cd2a4a634",
			RepositoryId: 42,
			Message:      "Test GitLab API",
		},
		Build{
			AccountId:       "gitlab account",
			Id:              3,
			CommitId:        "22f1e814995f60357b6dd82c8a43d03cd2a4a634",
			State:           "passed",
			RepoBuildNumber: "138",
		},
		Stage{
			AccountId: "gitlab account",
			Id: stageId,
			BuildId: 3,
			Number: 1,
			Name: "tests",
			State: "passed",
		},
		Job{
			AccountId: "gitlab account",
			Id: 5,
			BuildId: 3,
			StageId: &stageId,
			State: "passed",
			Number: "138.1",
			Log: "<not retrieved>",
		},
		Job{
			AccountId: "gitlab account",
			Id: 6,
			BuildId: 3,
			StageId: nil,
			State: "passed",
			Number: "138.1",
			Log: "<not retrieved>",
		},
	}

	for _, item := range inserters {
		err = cache.Save(item)
		if err != nil {
			t.Error(err)
		}
	}

}