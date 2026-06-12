package ginkgo

import (
	"testing"

	"github.com/onsi/ginkgo/v2/types"
)

func TestFindSpecReport(t *testing.T) {
	tests := []struct {
		name          string
		reports       types.SpecReports
		expectedState types.SpecState
	}{
		{
			name:          "empty reports returns zero-valued report",
			reports:       types.SpecReports{},
			expectedState: types.SpecStateInvalid,
		},
		{
			name: "passed spec with NumAttempts > 0",
			reports: types.SpecReports{
				{
					State:       types.SpecStatePassed,
					NumAttempts: 1,
				},
			},
			expectedState: types.SpecStatePassed,
		},
		{
			name: "failed spec with NumAttempts > 0",
			reports: types.SpecReports{
				{
					State:       types.SpecStateFailed,
					NumAttempts: 2,
				},
			},
			expectedState: types.SpecStateFailed,
		},
		{
			name: "pending spec has NumAttempts 0 - falls back to last report",
			reports: types.SpecReports{
				{
					State:       types.SpecStatePending,
					NumAttempts: 0,
				},
			},
			expectedState: types.SpecStatePending,
		},
		{
			name: "skipped spec has NumAttempts 0 - falls back to last report",
			reports: types.SpecReports{
				{
					State:       types.SpecStateSkipped,
					NumAttempts: 0,
				},
			},
			expectedState: types.SpecStateSkipped,
		},
		{
			name: "multiple reports picks attempted one over pending",
			reports: types.SpecReports{
				{
					State:       types.SpecStatePending,
					NumAttempts: 0,
				},
				{
					State:       types.SpecStatePassed,
					NumAttempts: 1,
				},
			},
			expectedState: types.SpecStatePassed,
		},
		{
			name: "multiple attempted reports picks last attempted",
			reports: types.SpecReports{
				{
					State:       types.SpecStatePassed,
					NumAttempts: 1,
				},
				{
					State:       types.SpecStateFailed,
					NumAttempts: 1,
				},
			},
			expectedState: types.SpecStateFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findSpecReport(tt.reports)
			if result.State != tt.expectedState {
				t.Errorf("findSpecReport() state = %v, want %v", result.State, tt.expectedState)
			}
		})
	}
}
