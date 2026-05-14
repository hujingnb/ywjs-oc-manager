// Package service 的 image_distribution_service_test 覆盖运行时镜像分发服务的配置缺失和代理错误传播。
package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/runtime/imagesync"
)

// TestImageDistributionServiceRequiresDistributor 验证镜像分发服务要求Distributor的预期行为场景。
func TestImageDistributionServiceRequiresDistributor(t *testing.T) {
	svc := NewImageDistributionService(nil)
	_, err := svc.EnsureRuntimeImage(context.Background(), "node-1", "openclaw:dev")
	if err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("EnsureRuntimeImage() error = %v, want config error", err)
	}
}

// TestImageDistributionServiceRejectsEmptyArgs 验证镜像分发服务拒绝空值参数的异常或拒绝路径场景。
func TestImageDistributionServiceRejectsEmptyArgs(t *testing.T) {
	svc := NewImageDistributionService(&fakeDistributor{})
	_, err := svc.EnsureRuntimeImage(context.Background(), "", "img")
	require.Error(t, err)
	_, err = svc.EnsureRuntimeImage(context.Background(), "node-1", "")
	require.Error(t, err)
}

// TestImageDistributionServicePropagatesUnderlyingError 验证镜像分发服务透传底层错误的错误映射或错误记录场景。
func TestImageDistributionServicePropagatesUnderlyingError(t *testing.T) {
	svc := NewImageDistributionService(&fakeDistributor{err: errors.New("boom")})
	_, err := svc.EnsureRuntimeImage(context.Background(), "node-1", "openclaw:dev")
	require.Error(t, err)
	if !strings.Contains(err.Error(), "openclaw:dev") || !strings.Contains(err.Error(), "node-1") {
		t.Fatalf("error missing context: %v", err)
	}
}

// TestImageDistributionServiceReturnsTransferredTrue 验证镜像分发服务返回已传输标记的成功路径场景。
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

func (f *fakeDistributor) SyncRuntimeImage(_ context.Context, _ string, _ string) (imagesync.SyncResult, error) {
	return f.result, f.err
}
