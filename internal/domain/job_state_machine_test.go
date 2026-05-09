package domain

import (
	"testing"
	"github.com/stretchr/testify/require"
)

func TestIsJobTransitionAllowedValid(t *testing.T) {
	cases := []struct {
		from string
		to   string
	}{
		{JobStatusPending, JobStatusRunning},
		{JobStatusPending, JobStatusCanceled},
		{JobStatusRunning, JobStatusSucceeded},
		{JobStatusRunning, JobStatusFailed},
		{JobStatusRunning, JobStatusPending},
		{JobStatusFailed, JobStatusPending},
	}
	for _, c := range cases {
		if !IsJobTransitionAllowed(c.from, c.to) {
			t.Errorf("expected %s -> %s allowed", c.from, c.to)
		}
	}
}

func TestIsJobTransitionAllowedRejectsBackToPendingFromTerminal(t *testing.T) {
	require.False(t, IsJobTransitionAllowed(JobStatusSucceeded, JobStatusPending))
	require.False(t, IsJobTransitionAllowed(JobStatusCanceled, JobStatusRunning))
	require.False(t, IsJobTransitionAllowed(JobStatusRunning, JobStatusRunning))
}

func TestEnsureJobTransitionReturnsError(t *testing.T) {
	err := EnsureJobTransition(JobStatusSucceeded, JobStatusPending)
	require.Error(t, err)
	err = EnsureJobTransition(JobStatusPending, JobStatusRunning)
	require.NoError(t, err)
}

func TestJobIsTerminal(t *testing.T) {
	require.True(t, JobIsTerminal(JobStatusSucceeded))
	require.True(t, JobIsTerminal(JobStatusFailed))
	require.True(t, JobIsTerminal(JobStatusCanceled))
	require.False(t, JobIsTerminal(JobStatusPending))
	require.False(t, JobIsTerminal(JobStatusRunning))
}

func TestAllowedJobTransitionsCount(t *testing.T) {
	require.Len(t, AllowedJobTransitions(), 6)
}
