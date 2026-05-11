package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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
	_, err := svc.EnsureRuntimeImage(context.Background(), "", "img")
	require.Error(t, err)
	_, err = svc.EnsureRuntimeImage(context.Background(), "node-1", "")
	require.Error(t, err)
}

func TestImageDistributionServicePropagatesUnderlyingError(t *testing.T) {
	svc := NewImageDistributionService(&fakeDistributor{err: errors.New("boom")})
	_, err := svc.EnsureRuntimeImage(context.Background(), "node-1", "openclaw:dev")
	require.Error(t, err)
	if !strings.Contains(err.Error(), "openclaw:dev") || !strings.Contains(err.Error(), "node-1") {
		t.Fatalf("error missing context: %v", err)
	}
}

func TestImageDistributionServiceReturnsTransferredTrue(t *testing.T) {
	svc := NewImageDistributionService(&fakeDistributor{result: imagesync.SyncResult{Image: "img", NodeID: "node-1", LocalID: "L", RemoteID: "L", Transferred: true}})
	got, err := svc.EnsureRuntimeImage(context.Background(), "node-1", "img")
	require.NoError(t, err)
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
