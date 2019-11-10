package cache

import (
	"testing"
)

func TestAggregateStatuses(t *testing.T) {
	testCases := []struct {
		name      string
		statusers []Statuser
		result    State
	}{
		{
			name:      "Empty list",
			statusers: []Statuser{},
			result:    Unknown,
		},
		{
			name: "Jobs: No allowed failure",
			statusers: []Statuser{
				Job{
					AllowFailure: false,
					State:        Passed,
				},
				Job{
					AllowFailure: false,
					State:        Failed,
				},
				Job{
					AllowFailure: false,
					State:        Passed,
				},
			},
			result: Failed,
		},
		{
			name: "Jobs: Allowed failure",
			statusers: []Statuser{
				Job{
					AllowFailure: false,
					State:        Passed,
				},
				Job{
					AllowFailure: true,
					State:        Failed,
				},
				Job{
					AllowFailure: false,
					State:        Passed,
				},
			},
			result: Passed,
		},
		{
			name: "Builds",
			statusers: []Statuser{
				Build{
					State: Passed,
				},
				Build{
					State: Failed,
				},
				Build{
					State: Passed,
				},
			},
			result: Failed,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if state := AggregateStatuses(testCase.statusers); state != testCase.result {
				t.Fatalf("expected %q but got %q", testCase.result, state)
			}
		})
	}
}
