//go:build integration

package newapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/stretchr/testify/require"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestIntegrationFullProvisionAndTokenFlow 用真实 new-api 实例验证全链路：
//  1. admin 创业务 user（CreateUser → FindUserByUsername）；
//  2. 业务 user 凭据 login + GET /api/user/token 拿 access_token（BootstrapUserAccessToken）；
//  3. 切到 user 身份创 token + 拉完整 sk-（CreateAPIKey + GetTokenFullKey）；
//  4. 同 user 身份禁用并恢复 token（SetAPIKeyStatus）；
//  5. admin 给该 user 充值（RechargeUser，走 POST /api/user/manage action=add_quota）。
//
// 仅在 -tags=integration 时编译。运行示例：
//
//	NEWAPI_BASE_URL_LOCAL=http://127.0.0.1:3000 \
//	NEWAPI_ADMIN_TOKEN=<access_token> \
//	NEWAPI_ADMIN_USER_ID=1 \
//	go test -tags=integration ./internal/integrations/newapi -run TestIntegrationFullProvisionAndTokenFlow -v
func TestIntegrationFullProvisionAndTokenFlow(t *testing.T) {
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

	// 给 username 加随机后缀，避免重复运行同测试时 new-api username 重复冲突。
	suffix := randSuffix(t)
	username := "smoke-" + suffix
	password := "Pwd-" + suffix + "-x9!"

	// 1. CreateUser
	user, err := c.CreateUser(ctx, CreateUserInput{
		Username:    username,
		Password:    password,
		DisplayName: "smoke " + suffix,
	})
	require.NoError(t, err)
	if user.ID == 0 || user.Username != username {
		t.Fatalf("CreateUser returned %+v", user)
	}
	t.Logf("CreateUser OK id=%d username=%s", user.ID, user.Username)

	// 2. login + 取 access_token
	accessToken, err := c.BootstrapUserAccessToken(ctx, username, password)
	require.NoError(t, err)
	require.NotEqual(t, "", accessToken)
	t.Logf("BootstrapUserAccessToken OK access_token len=%d", len(accessToken))

	// 3. user-scoped 创 token + 拉完整 sk-
	user_scoped := c.AsUser(user.ID, accessToken)
	tokenName := fmt.Sprintf("smoke-key-%s", suffix)
	tk, err := user_scoped.CreateAPIKey(ctx, CreateAPIKeyInput{
		Name:       tokenName,
		Quota:      0,
		UnlimitedQ: true,
	})
	require.NoError(t, err)
	require.NotEqual(t, 0, tk.ID)
	t.Logf("CreateAPIKey OK id=%d name=%s", tk.ID, tk.Name)

	fullKey, err := user_scoped.GetTokenFullKey(ctx, tk.ID)
	require.NoError(t, err)
	if len(fullKey) <= 18 {
		t.Fatalf("fullKey len=%d, want > 18 (truncated)", len(fullKey))
	}
	t.Logf("GetTokenFullKey OK len=%d prefix=%s", len(fullKey), fullKey[:8])

	// 4. 禁用 + 恢复 token
	err := user_scoped.SetAPIKeyStatus(ctx, tk.ID, 2)
	require.NoError(t, err)
	t.Logf("SetAPIKeyStatus disable OK")
	err := user_scoped.SetAPIKeyStatus(ctx, tk.ID, 1)
	require.NoError(t, err)
	t.Logf("SetAPIKeyStatus restore OK")

	// 5. admin 给业务 user 充值 100，验证 manage add_quota 路径
	rr, err := c.RechargeUser(ctx, RechargeInput{
		NewAPIUserID: user.ID,
		CreditAmount: 100,
		Remark:       fmt.Sprintf("integration-smoke-%s", suffix),
	})
	require.NoError(t, err)
	require.NotEqual(t, "", rr.RefID)
	if rr.RemainQuota < 100 {
		t.Fatalf("RemainQuota = %d, want >= 100", rr.RemainQuota)
	}
	t.Logf("RechargeUser OK ref_id=%s remain_quota=%d", rr.RefID, rr.RemainQuota)
}

// randSuffix 生成 12 字符随机十六进制串，避免 username 重复冲突。
func randSuffix(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 6)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	return hex.EncodeToString(raw)
}
