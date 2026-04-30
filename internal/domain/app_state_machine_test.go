package domain

import "testing"

func TestIsAppTransitionAllowedHappyPath(t *testing.T) {
	cases := [][2]string{
		{AppStatusDraft, AppStatusInitializing},
		{AppStatusInitializing, AppStatusBindingWaiting},
		{AppStatusBindingWaiting, AppStatusRunning},
		{AppStatusBindingWaiting, AppStatusBindingFailed},
		{AppStatusBindingFailed, AppStatusBindingWaiting},
		{AppStatusRunning, AppStatusStopped},
		{AppStatusStopped, AppStatusRunning},
		{AppStatusError, AppStatusInitializing},
	}
	for _, c := range cases {
		if !IsAppTransitionAllowed(c[0], c[1]) {
			t.Errorf("expected %s -> %s allowed", c[0], c[1])
		}
	}
}

func TestIsAppTransitionAllowedRejectsBackwards(t *testing.T) {
	if IsAppTransitionAllowed(AppStatusRunning, AppStatusInitializing) {
		t.Fatalf("running should not jump to initializing")
	}
	if IsAppTransitionAllowed(AppStatusDraft, AppStatusRunning) {
		t.Fatalf("draft must go through initializing")
	}
	if IsAppTransitionAllowed(AppStatusDraft, AppStatusDraft) {
		t.Fatalf("self transition rejected")
	}
}

func TestIsAppTransitionAllowedDeletedOnlyFromError(t *testing.T) {
	if IsAppTransitionAllowed(AppStatusRunning, AppStatusDeleted) {
		t.Fatalf("delete must go through SoftDeleteApp not state machine for running")
	}
	if !IsAppTransitionAllowed(AppStatusError, AppStatusDeleted) {
		t.Fatalf("error -> deleted should be allowed")
	}
}

func TestEnsureAppTransitionWraps(t *testing.T) {
	if err := EnsureAppTransition(AppStatusRunning, AppStatusInitializing); err == nil {
		t.Fatalf("expected error")
	}
	if err := EnsureAppTransition(AppStatusDraft, AppStatusInitializing); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAppIsTerminalOnlyDeleted(t *testing.T) {
	if !AppIsTerminal(AppStatusDeleted) {
		t.Fatalf("deleted should be terminal")
	}
	for _, status := range []string{AppStatusError, AppStatusRunning, AppStatusStopped, AppStatusDraft} {
		if AppIsTerminal(status) {
			t.Fatalf("%s should not be terminal", status)
		}
	}
}

func TestIsAPIKeyTransitionAllowedHappyPath(t *testing.T) {
	cases := [][2]string{
		{APIKeyStatusPending, APIKeyStatusActive},
		{APIKeyStatusPending, APIKeyStatusError},
		{APIKeyStatusActive, APIKeyStatusDisabled},
		{APIKeyStatusActive, APIKeyStatusError},
		{APIKeyStatusDisabled, APIKeyStatusActive},
		{APIKeyStatusError, APIKeyStatusPending},
	}
	for _, c := range cases {
		if !IsAPIKeyTransitionAllowed(c[0], c[1]) {
			t.Errorf("expected api_key %s -> %s allowed", c[0], c[1])
		}
	}
}

func TestAPIKeyAndAppStateAreIndependent(t *testing.T) {
	// 如果 app 进入 stopped，api_key 仍可保持 active；反之亦然。
	if !IsAppTransitionAllowed(AppStatusRunning, AppStatusStopped) {
		t.Fatalf("running -> stopped allowed")
	}
	if IsAppTransitionAllowed(APIKeyStatusActive, AppStatusStopped) {
		t.Fatalf("api_key active should not be confused with app status")
	}
	if !IsAPIKeyTransitionAllowed(APIKeyStatusActive, APIKeyStatusDisabled) {
		t.Fatalf("api_key active -> disabled allowed")
	}
}

func TestEnsureAPIKeyTransitionFailsForInvalid(t *testing.T) {
	if err := EnsureAPIKeyTransition(APIKeyStatusDisabled, APIKeyStatusError); err == nil {
		t.Fatalf("expected error for disabled -> error")
	}
	if err := EnsureAPIKeyTransition(APIKeyStatusPending, APIKeyStatusActive); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
