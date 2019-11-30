package main

import (
	"io/ioutil"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestConfiguration(t *testing.T) {
	t.Run("no path", func(t *testing.T) {
		if _, err := ConfigFromPaths(); err != ErrMissingConf {
			t.Fatalf("expected %v but got %v", ErrMissingConf, err)
		}
	})

	t.Run("full configuration", func(t *testing.T) {
		s := `
			[[providers.gitlab]]
			url = "https://gitlab.com"
			token = "token"
			max_requests_per_second = 20.2
			
			[[providers.gitlab]]
			url = "https://gitlab.org"
			token = "token"
			max_requests_per_second = 1
			
			[[providers.github]]
			url = "https://github.com"
			token = "token"
			max_requests_per_second = 20.2
		`

		expected := Configuration{
			Providers: ProvidersConfiguration{
				GitLab: []ProviderConfiguration{
					{
						Url:               "https://gitlab.com",
						Token:             "token",
						RequestsPerSecond: 20.2,
					},
					{
						Url:               "https://gitlab.org",
						Token:             "token",
						RequestsPerSecond: 1,
					},
				},
				GitHub: []ProviderConfiguration{
					{
						Url:               "https://github.com",
						Token:             "token",
						RequestsPerSecond: 20.2,
					},
				},
			},
		}

		f, err := ioutil.TempFile("", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
		c, err := ConfigFromPaths(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(c, expected); len(diff) > 0 {
			t.Fatal(diff)
		}
	})
}
