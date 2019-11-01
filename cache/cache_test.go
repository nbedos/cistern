package cache

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"testing"
)

func TestDumpTodir(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", t.Name())
	if err != nil {
		t.Fatal(err)
	}

	jobs := []Job{
		{
			Log: sql.NullString{String: "log1\n", Valid: true},
		},
		{
			Log: sql.NullString{String: "log2\n", Valid: true},
		},
		{
			Log: sql.NullString{String: "log3\n", Valid: true},
		},
	}

	paths := make([]string, len(jobs))
	writerByJob := make(map[Job]io.WriteCloser)
	for i, job := range jobs {
		filepath := path.Join(tmpDir, fmt.Sprintf("job_%d.log", i))
		file, err := os.Create(filepath)
		if err != nil {
			t.Fatal(err)
		}
		writerByJob[job] = file
		paths[i] = filepath
	}

	cache := NewCache(nil)
	if err := cache.WriteLogs(context.Background(), writerByJob); err != nil {
		t.Fatal(err)
	}

	if len(paths) != len(jobs) {
		t.Fatalf("expected %d file paths, got %d", len(jobs), len(paths))
	}

	for i, filepath := range paths {
		bs, err := ioutil.ReadFile(filepath)
		if err != nil {
			t.Fatal(err)
		}

		if string(bs) != jobs[i].Log.String {
			t.Fatalf("invalid log content: expected '%s' but got '%s'", jobs[i].Log.String, string(bs))
		}
	}
}
