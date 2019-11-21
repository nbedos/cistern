package providers

import (
	"net/url"
	"testing"
)

func TestParseGitlabWebURL(t *testing.T) {
	u := "https://gitlab.com/nbedos/citop/pipelines/97604657"
	baseURL := url.URL{
		Scheme: "https",
		Host:   "gitlab.com",
		Path:   "/api/v4",
	}

	owner, repo, id, err := parseGitlabWebURL(&baseURL, u)
	if err != nil {
		t.Fatal(err)
	}

	if owner != "nbedos" || repo != "citop" || id != 97604657 {
		t.Fail()
	}
}
