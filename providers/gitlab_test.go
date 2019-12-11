package providers

import (
	"testing"
	"time"
)

func TestParsePipelineURL(t *testing.T) {
	c := NewGitLabClient("gitlab", "gitlab", "", time.Millisecond)

	slug, id, err := c.parsePipelineURL("https://gitlab.com/nbedos/citop/pipelines/97604657")
	if err != nil {
		t.Fatal(err)
	}

	if slug != "nbedos/citop" || id != 97604657 {
		t.Fail()
	}
}
