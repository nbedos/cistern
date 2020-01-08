package utils

import (
	"fmt"
	"testing"
)


func TestRepositorySlugFromURL(t *testing.T) {
	urls := []string{
		// SSH git url
		"git@github.com:nbedos/cistern.git",
		"git@github.com:nbedos/cistern",
		// HTTPS git url
		"https://github.com/nbedos/cistern.git",
		// Host shouldn't matter
		"git@gitlab.com:nbedos/cistern.git",
		// Web URLs should work too
		"https://gitlab.com/nbedos/cistern",
		// Extraneous path components should be ignored
		"https://gitlab.com/nbedos/cistern/tree/master/cache",
		// url scheme shouldn't matter
		"http://gitlab.com/nbedos/cistern",
		"gitlab.com/nbedos/cistern",
	}
	expectedOwner, expectedRepo := "nbedos", "cistern"

	for _, u := range urls {
		t.Run(fmt.Sprintf("url: %v", u), func(t *testing.T) {
			_, owner, repo, err := RepoHostOwnerAndName(u)
			if err != nil {
				t.Fatal(err)
			}
			if owner != expectedOwner || repo != expectedRepo {
				t.Fatalf("expected %s/%s but got %s/%s", expectedOwner, expectedRepo, owner, repo)
			}
		})
	}

	urls = []string{
		// Missing 1 path component
		"git@github.com:nbedos.git",
		// Invalid url (colon)
		"https://github.com:nbedos/cistern.git",
	}

	for _, u := range urls {
		t.Run(fmt.Sprintf("url: %v", u), func(t *testing.T) {
			if _, _, _, err := RepoHostOwnerAndName(u); err == nil {
				t.Fatalf("expected error but got nil for url %q", u)
			}
		})
	}
}

func TestStartAndRelease(t *testing.T) {
	if err := StartAndRelease("go", []string{"--version"}); err != nil {
		t.Fatal(err)
	}
}
