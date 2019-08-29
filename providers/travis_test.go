package providers

import (
	"fmt"
	"github.com/nbedos/citop/cache"
	"os"
	"testing"
)

func TestTravisGetUserBuilds(t *testing.T) {
	token := os.Getenv("TRAVIS_API_TOKEN")
	if token == "" {
		t.Fatal("Environment variable TRAVIS_API_TOKEN is not set")
	}
	account := cache.Account{
		Id:       "travis",
		Url:      TravisApiOrgUrl,
		UserId:   "42",
		Username: "oops",
	}
	client := NewTravisClient(account, token)
	inserters, err := client.GetUserBuilds(token, "")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(inserters)
}
