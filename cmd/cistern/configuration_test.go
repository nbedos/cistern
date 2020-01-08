package main

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nbedos/cistern/providers"
	"github.com/nbedos/cistern/tui"
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

func TestConfiguration_DeterministicSort(t *testing.T) {
	var stepStyle = providers.StepStyle{
		GitStyle: providers.GitStyle{
			Location: time.UTC,
		},
		Provider: nil,
		Status: struct {
			Failed   tui.StyleTransform
			Canceled tui.StyleTransform
			Passed   tui.StyleTransform
			Running  tui.StyleTransform
			Pending  tui.StyleTransform
			Skipped  tui.StyleTransform
			Manual   tui.StyleTransform
		}{},
	}
	// We need multiple attempts to identify non-deterministic sorting
	const nbrAttempts = 20
	for _, column := range defaultTableColumns {
		t.Run(fmt.Sprintf("sort by %s", column.Header), func(t *testing.T) {
			pipelines := []tui.TableNode{
				providers.Pipeline{
					ProviderHost: "a",
					Step: providers.Step{
						ID: "0",
					},
				},
				providers.Pipeline{
					ProviderHost: "c",
					Step: providers.Step{
						ID: "2",
					},
				},
				providers.Pipeline{
					ProviderHost: "b",
					Step: providers.Step{
						ID: "1",
					},
				},
			}
			expected := []tui.TableNode{pipelines[0], pipelines[2], pipelines[1]}
			for i := 0; i < nbrAttempts; i++ {
				sort.Slice(pipelines, column.Less(pipelines, true, stepStyle))
				if diff := cmp.Diff(expected, pipelines, cmp.AllowUnexported(providers.Pipeline{})); diff != "" {
					t.Fatalf("attempt #%d failed: %s", i, diff)
				}
			}
		})
	}
}
