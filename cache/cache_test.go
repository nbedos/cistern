package cache

import (
	"fmt"
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

func TestBuild_Get(t *testing.T) {
	build := Build{
		Stages: map[int]*Stage{
			1: {ID: 1},
			2: {
				ID: 2,
				Jobs: map[int]*Job{
					3: {ID: 3},
					4: {ID: 4},
				},
			},
		},
		Jobs: map[int]*Job{
			5: {ID: 5},
			6: {ID: 6},
			7: {ID: 7},
		},
	}

	successTestCases := []struct {
		stageID int
		jobID   int
	}{
		{
			stageID: 2,
			jobID:   3,
		},
		{
			stageID: 2,
			jobID:   4,
		},
		{
			stageID: 0,
			jobID:   5,
		},
	}

	for _, testCase := range successTestCases {
		t.Run(fmt.Sprintf("Case %+v", testCase), func(t *testing.T) {
			job, exists := build.Get(testCase.stageID, testCase.jobID)
			if !exists {
				t.Fail()
			}
			if job.ID != testCase.jobID {
				t.Fatalf("expected jobID %d but got %d", testCase.jobID, job.ID)
			}
		})
	}

	failureTestCases := []struct {
		stageID int
		jobID   int
	}{
		{
			stageID: 0,
			jobID:   0,
		},
		{
			stageID: 2,
			jobID:   0,
		},
		{
			stageID: -1,
			jobID:   -1,
		},
	}

	for _, testCase := range failureTestCases {
		t.Run(fmt.Sprintf("Case %+v", testCase), func(t *testing.T) {
			_, exists := build.Get(testCase.stageID, testCase.jobID)
			if exists {
				t.Fatalf("expected to not find job %+v", testCase)
			}
		})
	}
}

func TestCache_Save(t *testing.T) {
	c := NewCache(nil)
	c.Save(&Build{ID: "42", State: Passed})
	c.Save(&Build{ID: "43", State: Passed})
	c.Save(&Build{ID: "42", State: Failed})

}
