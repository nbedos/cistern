package providers

import "testing"

func TestAggregateStatuses(t *testing.T) {
	testCases := []struct {
		name   string
		steps  []Step
		result State
	}{
		{
			name:   "Empty list",
			steps:  []Step{},
			result: Unknown,
		},
		{
			name: "Jobs: No allowed failure",
			steps: []Step{
				{
					AllowFailure: false,
					State:        Passed,
				},
				{
					AllowFailure: false,
					State:        Failed,
				},
				{
					AllowFailure: false,
					State:        Passed,
				},
			},
			result: Failed,
		},
		{
			name: "Jobs: Allowed failure",
			steps: []Step{
				{
					AllowFailure: false,
					State:        Passed,
				},
				{
					AllowFailure: true,
					State:        Failed,
				},
				{
					AllowFailure: false,
					State:        Passed,
				},
			},
			result: Passed,
		},
		{
			name: "Builds",
			steps: []Step{
				{
					State: Passed,
				},
				{
					State: Failed,
				},
				{
					State: Passed,
				},
			},
			result: Failed,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if agg := Aggregate(testCase.steps); agg.State != testCase.result {
				t.Fatalf("expected %q but got %q", testCase.result, agg.State)
			}
		})
	}
}