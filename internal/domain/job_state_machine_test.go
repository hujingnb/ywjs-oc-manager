package domain

import "testing"

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
	if IsJobTransitionAllowed(JobStatusSucceeded, JobStatusPending) {
		t.Fatalf("succeeded should be terminal")
	}
	if IsJobTransitionAllowed(JobStatusCanceled, JobStatusRunning) {
		t.Fatalf("canceled should be terminal")
	}
	if IsJobTransitionAllowed(JobStatusRunning, JobStatusRunning) {
		t.Fatalf("self-transition should be rejected")
	}
}

func TestEnsureJobTransitionReturnsError(t *testing.T) {
	if err := EnsureJobTransition(JobStatusSucceeded, JobStatusPending); err == nil {
		t.Fatalf("expected error")
	}
	if err := EnsureJobTransition(JobStatusPending, JobStatusRunning); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestJobIsTerminal(t *testing.T) {
	if !JobIsTerminal(JobStatusSucceeded) || !JobIsTerminal(JobStatusFailed) || !JobIsTerminal(JobStatusCanceled) {
		t.Fatalf("terminal statuses should be terminal")
	}
	if JobIsTerminal(JobStatusPending) || JobIsTerminal(JobStatusRunning) {
		t.Fatalf("non-terminal statuses should not be terminal")
	}
}

func TestAllowedJobTransitionsCount(t *testing.T) {
	if got := len(AllowedJobTransitions()); got != 6 {
		t.Fatalf("AllowedJobTransitions count = %d, want 6", got)
	}
}
