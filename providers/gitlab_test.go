package providers

import (
	"github.com/nbedos/citop/cache"
	"os"
	"testing"
)

func TestGitLabGetUserBuilds(t *testing.T) {
	token := os.Getenv("GITLAB_API_TOKEN")
	if token == "" {
		t.Fatal("Environment variable GITLAB_API_TOKEN is not set")
	}
	account := cache.Account{
		Id:       "gitlab",
		Url:      "example.com/api/v3",
		UserId:   "42",
		Username: "oops",
	}
	client := NewGitLabClient(account, token)
	_, err := client.GetUserBuilds(token, "nbedos")
	if err != nil {
		t.Fatal(err)
	}
}
