//go:build integration

package newapi

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestIntegrationRechargeAndCreateToken 用真实 new-api 实例验证 RechargeUser → CreateAPIKey → SetAPIKeyStatus
// 三步全链路。仅在 -tags=integration 时编译。
//
// 用法：
//
//	NEWAPI_BASE_URL_LOCAL=http://127.0.0.1:3000 \
//	NEWAPI_ADMIN_TOKEN=<access_token> \
//	NEWAPI_ADMIN_USER_ID=1 \
//	go test -tags=integration ./internal/integrations/newapi -run TestIntegrationRechargeAndCreateToken -v
func TestIntegrationRechargeAndCreateToken(t *testing.T) {
	baseURL := os.Getenv("NEWAPI_BASE_URL_LOCAL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:3000"
	}
	token := os.Getenv("NEWAPI_ADMIN_TOKEN")
	if token == "" {
		t.Skip("NEWAPI_ADMIN_TOKEN 未配置；跳过真实 new-api 集成 smoke")
	}
	uidStr := os.Getenv("NEWAPI_ADMIN_USER_ID")
	if uidStr == "" {
		uidStr = "1"
	}
	adminUserID, _ := strconv.ParseInt(uidStr, 10, 64)

	c := NewClient(baseURL, token, adminUserID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 用 admin 自己 (id=1) 充值；他余额已经很大，加 100 不影响业务
	rr, err := c.RechargeUser(ctx, RechargeInput{
		NewAPIUserID: adminUserID,
		CreditAmount: 100,
		Remark:       fmt.Sprintf("integration-smoke-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("RechargeUser err: %v", err)
	}
	if rr.RemainQuota <= 0 {
		t.Fatalf("RemainQuota = %d, expect > 0", rr.RemainQuota)
	}
	if rr.RefID == "" {
		t.Fatalf("RefID empty")
	}
	t.Logf("RechargeUser OK ref_id=%s remain_quota=%d", rr.RefID, rr.RemainQuota)

	// 创建一个 token 验证 id fallback 真能拿到 id
	name := fmt.Sprintf("smoke-key-%d", time.Now().UnixNano())
	tk, err := c.CreateAPIKey(ctx, CreateAPIKeyInput{
		UserID:     adminUserID,
		Name:       name,
		Quota:      100,
		UnlimitedQ: false,
	})
	if err != nil {
		t.Fatalf("CreateAPIKey err: %v", err)
	}
	if tk.ID == 0 {
		t.Fatalf("CreateAPIKey returned id=0, fallback broken")
	}
	if tk.Name != name {
		t.Fatalf("CreateAPIKey name = %q, want %q", tk.Name, name)
	}
	t.Logf("CreateAPIKey OK id=%d name=%s", tk.ID, tk.Name)

	// 立即禁用刚创建的 token，验证 SetAPIKeyStatus 能用真 id 调通（同时验证 ?status_only=true query string 不被 escape）
	if err := c.SetAPIKeyStatus(ctx, tk.ID, 2); err != nil {
		t.Fatalf("SetAPIKeyStatus disable err: %v", err)
	}
	t.Logf("SetAPIKeyStatus OK disabled token id=%d", tk.ID)

	// 收尾：恢复 token，留给后续 cleanup（manager 删除应用时会一并禁用）
	if err := c.SetAPIKeyStatus(ctx, tk.ID, 1); err != nil {
		t.Fatalf("SetAPIKeyStatus restore err: %v", err)
	}
}
