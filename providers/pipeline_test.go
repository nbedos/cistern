package providers

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

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

func TestStep_StatusDiff(t *testing.T) {
	before := Step{
		ID:    "0",
		Type:  StepPipeline,
		State: Running,
		Children: []Step{
			{
				ID:    "1",
				Type:  StepStage,
				State: Running,
				Children: []Step{
					{
						ID:    "2",
						Type:  StepJob,
						State: Running,
					},
					{
						ID:    "3",
						Type:  StepJob,
						State: Pending,
					},
				},
			},
		},
	}

	t.Run("job status change", func(t *testing.T) {
		after := Step{
			ID:    "0",
			Type:  StepPipeline,
			State: Running,
			Children: []Step{
				{
					ID:    "1",
					Type:  StepStage,
					State: Running,
					Children: []Step{
						{
							ID:    "2",
							Type:  StepJob,
							State: Running,
						},
						{
							ID:    "3",
							Type:  StepJob,
							State: Running,
						},
					},
				},
			},
		}

		expected := map[StepType]*StepStatusChanges{
			StepPipeline: {},
			StepStage:    {},
			StepJob: {
				Started: [][]string{
					{"0", "1", "3"},
				},
			},
			StepTask: {},
		}

		if diff := cmp.Diff(after.statusDiff(before, nil), expected); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("stage status change", func(t *testing.T) {
		after := Step{
			ID:    "0",
			Type:  StepPipeline,
			State: Running,
			Children: []Step{
				{
					ID:    "1",
					Type:  StepStage,
					State: Failed,
				},
			},
		}

		expected := map[StepType]*StepStatusChanges{
			StepPipeline: {},
			StepStage: {
				Failed: [][]string{
					{"0", "1"},
				},
			},
			StepJob:  {},
			StepTask: {},
		}

		if diff := cmp.Diff(after.statusDiff(before, nil), expected); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("pipeline status change", func(t *testing.T) {
		after := Step{
			ID:    "0",
			Type:  StepPipeline,
			State: Passed,
		}

		expected := map[StepType]*StepStatusChanges{
			StepPipeline: {
				Passed: [][]string{
					{"0"},
				},
			},
			StepStage: {},
			StepJob:   {},
			StepTask:  {},
		}

		if diff := cmp.Diff(after.statusDiff(before, nil), expected); diff != "" {
			t.Fatal(diff)
		}
	})

	t.Run("new step", func(t *testing.T) {
		after := Step{
			ID:    "0",
			Type:  StepPipeline,
			State: Running,
			Children: []Step{
				{
					ID:    "42",
					Type:  StepStage,
					State: Failed,
				},
			},
		}

		expected := map[StepType]*StepStatusChanges{
			StepPipeline: {},
			StepStage: {
				Failed: [][]string{
					{"0", "42"},
				},
			},
			StepJob:  {},
			StepTask: {},
		}

		if diff := cmp.Diff(after.statusDiff(before, nil), expected); diff != "" {
			t.Fatal(diff)
		}
	})
}
