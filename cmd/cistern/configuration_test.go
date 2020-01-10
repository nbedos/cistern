package main

import (
	"testing"
)

func TestConfiguration_CisternToml(t *testing.T) {
	c, err := ConfigFromPaths("cistern.toml")
	if err != nil {
		t.Fatal(err)
	}

	_, err = c.TableConfig(defaultTableColumns)
	if err != nil {
		t.Fatal(err)
	}
}
