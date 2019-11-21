package providers

import (
	"net/url"
	"testing"
)

func TestParseCircleCIWebURL(t *testing.T) {
	u := "https://circleci.com/gh/nbedos/citop/36"
	baseURL := url.URL{
		Scheme: "https",
		Host:   "circleci.com",
	}

	owner, repo, id, err := parseCircleCIWebURL(&baseURL, u)
	if err != nil {
		t.Fatal(err)
	}

	if owner != "nbedos" || repo != "citop" || id != "36" {
		t.Fail()
	}
}
