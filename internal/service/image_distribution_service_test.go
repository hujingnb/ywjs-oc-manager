package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oc-manager/internal/runtime/imagesync"
)

func TestImageDistributionServiceRequiresDistributor(t *testing.T) {
	svc := NewImageDistributionService(nil)
	_, err := svc.EnsureRuntimeImage(context.Background(), "node-1", "openclaw:dev")
	if err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("EnsureRuntimeImage() error = %v, want config error", err)
	}
}

func TestImageDistributionServiceRejectsEmptyArgs(t *testing.T) {
	svc := NewImageDistributionService(&fakeDistributor{})
	if _, err := svc.EnsureRuntimeImage(context.Background(), "", "img"); err == nil {
		t.Fatalf("expected error for empty nodeID")
	}
	if _, err := svc.EnsureRuntimeImage(context.Background(), "node-1", ""); err == nil {
		t.Fatalf("expected error for empty image")
	}
}

func TestImageDistributionServicePropagatesUnderlyingError(t *testing.T) {
	svc := NewImageDistributionService(&fakeDistributor{err: errors.New("boom")})
	_, err := svc.EnsureRuntimeImage(context.Background(), "node-1", "openclaw:dev")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "openclaw:dev") || !strings.Contains(err.Error(), "node-1") {
		t.Fatalf("error missing context: %v", err)
	}
}

func TestImageDistributionServiceReturnsTransferredTrue(t *testing.T) {
	svc := NewImageDistributionService(&fakeDistributor{result: imagesync.SyncResult{Image: "img", NodeID: "node-1", LocalID: "L", RemoteID: "L", Transferred: true}})
	got, err := svc.EnsureRuntimeImage(context.Background(), "node-1", "img")
	if err != nil {
		t.Fatalf("EnsureRuntimeImage() error = %v", err)
	}
	if !got.Transferred || got.LocalID != "L" {
		t.Fatalf("result = %+v", got)
	}
}

type fakeDistributor struct {
	result imagesync.SyncResult
	err    error
}

func (f *fakeDistributor) SyncOpenClawImage(_ context.Context, _ string, _ string) (imagesync.SyncResult, error) {
	return f.result, f.err
}
