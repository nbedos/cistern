package cache

import (
	"context"
	"io/ioutil"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/nbedos/citop/utils"
)

var build = Build{
	Repository: &Repository{
		AccountID: "provider",
		ID:        42,
		URL:       "github.com/owner/project",
		Owner:     "owner",
		Name:      "project",
	},
	ID: "42",
	Commit: Commit{
		Sha:     "c2bb562365d40caec0b37138f73a87b6339a8b7a",
		Message: "commit title\nline #1\nline #2\nline #3\n",
		Date: utils.NullTime{
			Valid: true,
			Time:  time.Date(2019, 11, 13, 13, 12, 11, 0, time.UTC),
		},
	},
	Ref:             "master",
	IsTag:           false,
	RepoBuildNumber: "43",
	State:           "passed",
	CreatedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 11, 0, time.UTC),
	},
	StartedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 12, 0, time.UTC),
	},
	FinishedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 13, 0, time.UTC),
	},
	UpdatedAt: time.Date(2019, 11, 13, 13, 12, 13, 0, time.UTC),
	Duration: utils.NullDuration{
		Valid:    true,
		Duration: 3 * time.Second,
	},
	WebURL: "example.com/pipeline/42",
	Stages: map[int]*Stage{
		stage.ID: &stage,
	},
	Jobs: nil,
}

var buildAsRow = buildRow{
	key: buildRowKey{
		ref:       "master",
		sha:       "c2bb562365d40caec0b37138f73a87b6339a8b7a",
		accountID: "provider",
		buildID:   "42",
	},
	type_:    "P",
	state:    "passed",
	name:     "#42",
	provider: "provider",
	prefix:   "",
	createdAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 11, 0, time.UTC),
	},
	startedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 12, 0, time.UTC),
	},
	finishedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 13, 0, time.UTC),
	},
	updatedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 13, 0, time.UTC),
	},
	duration: utils.NullDuration{
		Valid:    true,
		Duration: 3 * time.Second,
	},
	url: "example.com/pipeline/42",
	children: []*buildRow{
		&stageAsRow,
	},
}

var stage = Stage{
	ID:    1,
	Name:  "test",
	State: "passed",
	Jobs:  []*Job{&job},
}

var stageAsRow = buildRow{
	key: buildRowKey{
		ref:       "master",
		sha:       "c2bb562365d40caec0b37138f73a87b6339a8b7a",
		accountID: "provider",
		buildID:   "42",
		stageID:   1,
	},
	type_:    "S",
	state:    "passed",
	name:     "test",
	provider: "provider",
	createdAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 11, 0, time.UTC),
	},
	startedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 12, 0, time.UTC),
	},
	finishedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 13, 0, time.UTC),
	},
	updatedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 13, 0, time.UTC),
	},
	duration: utils.NullDuration{
		Valid:    true,
		Duration: time.Second,
	},
	url: "example.com/pipeline/42",
	children: []*buildRow{
		&jobAsRow,
	},
}

var job = Job{
	ID:    "54",
	State: "passed",
	Name:  "golang 1.12",
	CreatedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 11, 0, time.UTC),
	},
	StartedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 12, 0, time.UTC),
	},
	FinishedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 13, 0, time.UTC),
	},
	Duration: utils.NullDuration{
		Valid:    true,
		Duration: 3 * time.Second,
	},
	Log:          utils.NullString{},
	WebURL:       "",
	AllowFailure: false,
}

var jobAsRow = buildRow{
	key: buildRowKey{
		ref:       "master",
		sha:       "c2bb562365d40caec0b37138f73a87b6339a8b7a",
		accountID: "provider",
		buildID:   "42",
		stageID:   1,
		jobID:     "54",
	},
	type_:    "J",
	state:    "passed",
	name:     "golang 1.12",
	provider: "provider",
	createdAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 11, 0, time.UTC),
	},
	startedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 12, 0, time.UTC),
	},
	finishedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 13, 0, time.UTC),
	},
	updatedAt: utils.NullTime{
		Valid: true,
		Time:  time.Date(2019, 11, 13, 13, 12, 13, 0, time.UTC),
	},
	duration: utils.NullDuration{
		Valid:    true,
		Duration: 3 * time.Second,
	},
	url: "",
}

func Test_buildRowFromJob(t *testing.T) {
	row := buildRowFromJob(build.Repository.AccountID, build.Commit.Sha, build.Ref, build.ID, 1, job)
	if diff := row.Diff(jobAsRow); diff != "" {
		t.Log(diff)
		t.Fail()
	}
}

func Test_buildRowFromStage(t *testing.T) {
	row := buildRowFromStage(build.Repository.AccountID, build.Commit.Sha, build.Ref, build.ID, build.WebURL, stage)
	if diff := row.Diff(stageAsRow); diff != "" {
		t.Log(diff)
		t.Fail()
	}
}

func Test_buildRowFromBuild(t *testing.T) {
	row := buildRowFromBuild(build)
	if diff := row.Diff(buildAsRow); diff != "" {
		t.Log(diff)
		t.Fail()
	}
}

func TestBuildRow_TreeNode(t *testing.T) {
	nodes := utils.DepthFirstTraversal(&buildAsRow, true)
	keys := []interface{}{
		buildAsRow.Key(),
		stageAsRow.Key(),
		jobAsRow.Key(),
	}

	if len(nodes) != len(keys) {
		t.Fatalf("expected %d nodes but got %d", len(keys), len(nodes))
	}

	for i, key := range keys {
		if nodeKey := nodes[i].(*buildRow).Key(); nodeKey != key {
			t.Fatalf("expected key %+v but got %+v", key, nodeKey)
		}
	}
}

func Delay(b Build, d time.Duration) Build {
	b.CreatedAt.Time.Add(d)
	b.StartedAt.Time.Add(d)
	b.FinishedAt.Time.Add(d)

	for _, job := range build.Jobs {
		job.CreatedAt.Time.Add(d)
		job.StartedAt.Time.Add(d)
		job.FinishedAt.Time.Add(d)
	}

	for _, stage := range b.Stages {
		for _, job := range stage.Jobs {
			job.CreatedAt.Time.Add(d)
			job.StartedAt.Time.Add(d)
			job.FinishedAt.Time.Add(d)
		}
	}

	return b
}

func TestBuildRow_Tabular(t *testing.T) {
	t.Run("null dates should be replaced by placeholder", func(t *testing.T) {
		text := buildRow{}.Tabular(time.UTC)
		for _, column := range []string{"CREATED", "STARTED", "FINISHED", "UPDATED"} {
			if text[column].String() != "-" {
				t.Fatalf("expected %q but got %q", "-", text[column])
			}
		}
	})

	t.Run("tabular version of cache.Build", func(t *testing.T) {
		expected := map[string]string{
			"COMMIT":   "c2bb562",
			"CREATED":  "Nov 13 13:12",
			"DURATION": "3s",
			"FINISHED": "Nov 13 13:12",
			"NAME":     "provider (#42)",
			"REF":      "master",
			"STARTED":  "Nov 13 13:12",
			"STATE":    "passed",
			"TYPE":     "P",
			"UPDATED":  "Nov 13 13:12",
		}
		for column, text := range buildAsRow.Tabular(time.UTC) {
			if s := text.String(); s != expected[column] {
				t.Fatalf("expected %q but got %q", expected[column], s)
			}
		}
	})
}

func TestBuildsByCommit_Rows(t *testing.T) {
	c := NewCache(nil, nil)
	shas := []string{"aaaaaa", "bbbbbb", "cccccc"}
	ids := []int{1, 2, 4, 8, 16, 32}
	for i, sha := range shas {
		for _, n := range ids {
			build := Delay(build, time.Duration(n)*time.Hour)
			build.ID = strconv.Itoa(1000*i + n)
			build.Commit.Sha = sha
			if err := c.Save(build); err != nil {
				t.Fatal(err)
			}
		}
	}

	rows := c.BuildsByCommit().Rows()
	if len(rows) != len(shas)*len(ids) {
		t.Fatalf("expected %d row but got %d", len(shas), len(rows))
	}
}

func TestBuildsByCommit_WriteToDisk(t *testing.T) {
	builds := []Build{build}
	c := NewCache([]CIProvider{
		mockProvider{
			id:     "provider",
			builds: builds,
		},
	}, nil)
	for _, build := range builds {
		if err := c.Save(build); err != nil {
			t.Fatal(err)
		}
	}

	source := c.BuildsByCommit()
	dir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	t.Run("no log is associated to builds", func(t *testing.T) {
		_, err := source.WriteToDisk(context.Background(), buildAsRow.Key(), dir)
		if err != ErrNoLogHere {
			t.Fatalf("expected %v but got %v", ErrNoLogHere, err)
		}
	})

	t.Run("no log is associated to stages", func(t *testing.T) {
		_, err := source.WriteToDisk(context.Background(), stageAsRow.Key(), dir)
		if err != ErrNoLogHere {
			t.Fatalf("expected %v but got %v", ErrNoLogHere, err)
		}
	})

	t.Run("log of job must be written to disk", func(t *testing.T) {
		path, err := source.WriteToDisk(context.Background(), jobAsRow.Key(), dir)
		if err != nil {
			t.Fatal(err)
		}

		p, err := ioutil.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if log := "provider\n"; string(p) != log {
			t.Fatalf("expected %q but got %q", log, string(p))
		}
	})
}
