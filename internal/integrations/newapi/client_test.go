package newapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListModelsPrefersDashboardEndpoint 验证模型列表优先解析 new-api Dashboard 模型映射接口。
func TestListModelsPrefersDashboardEndpoint(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/models", r.URL.Path)
		require.Equal(t, "Bearer admin-token", r.Header.Get("Authorization"))
		require.Equal(t, "1", r.Header.Get("New-Api-User"))
		_, _ = w.Write([]byte(`{"success":true,"data":{"1":["qwen2.5:7b","deepseek-r1:14b"],"2":["qwen2.5:7b"]}}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "admin-token", 1)
	models, err := client.ListModels(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []Model{{ID: "deepseek-r1:14b", Name: "deepseek-r1:14b"}, {ID: "qwen2.5:7b", Name: "qwen2.5:7b"}}, models)
}

// TestListModelsFallsBackToOpenAIEndpoint 验证 Dashboard 模型接口不可用时兼容 OpenAI 模型列表。
func TestListModelsFallsBackToOpenAIEndpoint(t *testing.T) {
	t.Parallel()
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/api/models" {
			http.NotFound(w, r)
			return
		}
		require.Equal(t, "/v1/models", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"id":"b-model"},{"id":"a-model"}]}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "admin-token", 1)
	models, err := client.ListModels(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"/api/models", "/v1/models"}, paths)
	assert.Equal(t, []Model{{ID: "a-model", Name: "a-model"}, {ID: "b-model", Name: "b-model"}}, models)
}

// TestUserScopedCreateAPIKeyHappyPath 校验 user-scoped client 调 POST /api/token/ 时携带
// user 鉴权两件套（Authorization Bearer + New-Api-User），并能解析 success+data.id。
func TestUserScopedCreateAPIKeyHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/token/", r.URL.Path)
		require.Equal(t, "Bearer user-token", r.Header.Get("Authorization"))
		require.Equal(t, "7", r.Header.Get("New-Api-User"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"user_id":7,"name":"alice","key":"sk-truncated","remain_quota":1000}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin-token", 1)
	user := client.AsUser(7, "user-token")
	got, err := user.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice", Quota: 1000})
	require.NoError(t, err)
	require.Equal(t, int64(42), got.ID)
}

// TestUserScopedCreateAPIKeyMapsUnauthorized 校验 401 → ErrUnauthorized 错误映射。
func TestUserScopedCreateAPIKeyMapsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	user := client.AsUser(0, "")
	_, err := user.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice"})
	require.ErrorIs(t, err, ErrUnauthorized)
}

// TestUserScopedCreateAPIKeySurfacesUpstreamSuccessFalse 校验 success=false 把 message
// 包到 ErrUpstream 错误链里。
func TestUserScopedCreateAPIKeySurfacesUpstreamSuccessFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"message":"quota exhausted"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	user := client.AsUser(7, "tok")
	_, err := user.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice"})
	require.ErrorIs(t, err, ErrUpstream)
	require.True(t, strings.Contains(err.Error(), "quota exhausted"))
}

// TestUserScopedSetAPIKeyStatusPropagatesErrors 校验 PUT /api/token/?status_only=true
// 在 409 时映射成 ErrConflict。
func TestUserScopedSetAPIKeyStatusPropagatesErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	user := client.AsUser(1, "tok")
	if err := user.SetAPIKeyStatus(context.Background(), 1, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

// TestUserScopedGetTokenFullKeyHappyPath 校验 POST /api/token/:id/key 的 user 鉴权头
// 与 data.key 字段解析。
func TestUserScopedGetTokenFullKeyHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/token/42/key", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "7", r.Header.Get("New-Api-User"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-real-1234567890abcdef"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	got, err := client.AsUser(7, "tok").GetTokenFullKey(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "sk-real-1234567890abcdef", got)
}

// TestUserScopedGetTokenFullKeyMapsNotFound 校验 success=false + message="not found"
// 映射成 ErrNotFound（new-api 对不存在的 token id 走 200+success=false）。
func TestUserScopedGetTokenFullKeyMapsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"message":"record not found"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	_, err := client.AsUser(1, "tok").GetTokenFullKey(context.Background(), 999)
	require.ErrorIs(t, err, ErrNotFound)
}

// TestNewApiUserHeaderOmittedWhenAdminUserIDZero 校验 AdminUserID=0 时不发送 New-Api-User
// header；旧测试构造空 client 依赖此行为，避免 strict mock 拒绝未知 header。
func TestNewApiUserHeaderOmittedWhenAdminUserIDZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("New-Api-User"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"remain_quota":0,"used_quota":0}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	_, err := client.GetUserBalance(context.Background(), 1)
	require.NoError(t, err)
}

// TestRechargeUserUsesManageEndpoint 校验 RechargeUser 改走 POST /api/user/manage
// (action=add_quota, mode=add)，并按 new-api quota_per_unit 把展示额度转换为内部 quota。
// 这样 manager 输入 1000 时，new-api 用户页「总额度」也增加 1000，而不是只增加 1000 个内部计费点。
// 紧跟一次 GET /api/user/{id} 把"加完后的 quota"拉回作为 RechargeResult.RemainQuota。
func TestRechargeUserUsesManageEndpoint(t *testing.T) {
	var (
		gotManage  bool
		gotGet     bool
		manageBody map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/user/manage":
			gotManage = true
			_ = decodeJSONBody(r, &manageBody)
			_, _ = w.Write([]byte(`{"success":true,"message":""}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/42":
			gotGet = true
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"quota":1500,"used_quota":12}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok", 1)
	got, err := client.RechargeUser(context.Background(), RechargeInput{
		NewAPIUserID: 42, CreditAmount: 1000, Remark: "test-recharge",
	})
	require.NoError(t, err)
	if !gotManage || !gotGet {
		t.Fatalf("expected manage + GET, got manage=%v get=%v", gotManage, gotGet)
	}
	if manageBody["action"] != "add_quota" || manageBody["mode"] != "add" {
		t.Fatalf("manage body action/mode wrong: %v", manageBody)
	}
	require.Equal(t, int64(500000000), int64Of(manageBody["value"]))
	require.Equal(t, int64(1500), got.RemainQuota)
	require.NotEqual(t, "", got.RefID)
}

// TestRechargeUserRejectsInvalidQuotaPerUnit 校验 new-api 状态接口返回非法换算比例时，
// client 不会继续调用充值接口，避免写入错误额度。
func TestRechargeUserRejectsInvalidQuotaPerUnit(t *testing.T) {
	gotManage := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":0}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/user/manage":
			gotManage = true
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok", 1)
	_, err := client.RechargeUser(context.Background(), RechargeInput{NewAPIUserID: 42, CreditAmount: 1000})
	require.ErrorIs(t, err, ErrPayloadInvalid)
	require.False(t, gotManage)
}

// TestRechargeUserRejectsQuotaOverflow 校验展示额度乘以 quota_per_unit 可能溢出 int64 时直接拒绝，
// 避免向 new-api 发送截断后的负数或错误额度。
func TestRechargeUserRejectsQuotaOverflow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.Equal(t, "/api/status", r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"data":{"quota_per_unit":500000}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok", 1)
	_, err := client.RechargeUser(context.Background(), RechargeInput{
		NewAPIUserID: 42,
		CreditAmount: math.MaxInt64/500000 + 1,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "超出")
}

// TestGetAllQuotaDatesBackfillsDateFromCreatedAt 校验 new-api 聚合接口只返回 created_at 时，
// client 会补齐 DATE 列需要的日期字段，避免用量页面展示空日期。
func TestGetAllQuotaDatesBackfillsDateFromCreatedAt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		require.Equal(t, "/api/data/", r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"data":[{"created_at":1778562000,"model_name":"","count":1,"quota":5,"token_used":10}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok", 1)
	items, err := client.GetAllQuotaDates(context.Background(), 0, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "2026-05-12", items[0].Date)
	assert.Equal(t, int64(1778562000), items[0].CreatedAt)
}

// TestGetUserQuotaDatesBackfillsModelNameFromLogs 校验 new-api 用户聚合接口缺失 model_name 时，
// client 会用同一 username 的日志补齐模型名，避免组织用量页显示“未知模型”。
func TestGetUserQuotaDatesBackfillsModelNameFromLogs(t *testing.T) {
	var gotLogQuery bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/data/users":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"username":"org-demo","created_at":1778569200,"model_name":"","count":2,"quota":15,"token_used":30}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/log/":
			gotLogQuery = true
			assert.Equal(t, "org-demo", r.URL.Query().Get("username"))
			_, _ = w.Write([]byte(`{"success":true,"data":{"total":2,"items":[{"username":"org-demo","created_at":1778570000,"model_name":"qwen2.5:0.5b","quota":5,"prompt_tokens":3,"completion_tokens":7},{"username":"org-demo","created_at":1778570100,"model_name":"qwen2.5:0.5b","quota":10,"prompt_tokens":8,"completion_tokens":12}]}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok", 1)
	items, err := client.GetUserQuotaDates(context.Background(), 8, 1778486400, 1778572799)
	require.NoError(t, err)
	require.True(t, gotLogQuery)
	require.Len(t, items, 1)
	assert.Equal(t, "qwen2.5:0.5b", items[0].ModelName)
	assert.Equal(t, 2, items[0].Count)
	assert.Equal(t, int64(15), items[0].Quota)
	assert.Equal(t, 30, items[0].Tokens)
}

// TestCreateUserCallsAdminEndpoint 校验 CreateUser 调 admin POST /api/user/ 并回查 user_id：
// new-api v1 该端点响应不返回 data.id，client 必须通过 GET /api/user/search?keyword=username
// 拿到完整 user 实体。
func TestCreateUserCallsAdminEndpoint(t *testing.T) {
	var (
		gotPost   bool
		gotSearch bool
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/user/":
			gotPost = true
			_, _ = w.Write([]byte(`{"success":true,"message":""}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/user/search"):
			gotSearch = true
			require.Equal(t, "alice", r.URL.Query().Get("keyword"))
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":99,"username":"alice","display_name":"Alice"}]}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin-tok", 1)
	user, err := client.CreateUser(context.Background(), CreateUserInput{
		Username: "alice", Password: "pwd", DisplayName: "Alice",
	})
	require.NoError(t, err)
	if !gotPost || !gotSearch {
		t.Fatalf("expected POST + search, got post=%v search=%v", gotPost, gotSearch)
	}
	require.Equal(t, int64(99), user.ID)
}

// TestBootstrapUserAccessTokenLoginThenGetToken 校验登录拿 cookie + 带 cookie 调
// GET /api/user/token 拿 access_token 这条两步流程。
//
// new-api Login 的 session 写入 cookie，GenerateAccessToken 必须在同一会话内调，
// 这里用 httptest 的 cookie jar 行为（默认 httptest.NewServer 不强制 secure）模拟。
func TestBootstrapUserAccessTokenLoginThenGetToken(t *testing.T) {
	var (
		loginCalled  bool
		tokenCalled  bool
		issuedCookie string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/user/login":
			loginCalled = true
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "sess-abc", Path: "/"})
			issuedCookie = "sess-abc"
			// login 响应必须返回 data.id，BootstrapUserAccessToken 用它填 GET /api/user/token 的
			// New-Api-User header（new-api 即使在 session 鉴权下也要求该 header）。
			_, _ = w.Write([]byte(`{"success":true,"message":"","data":{"id":42}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/token":
			tokenCalled = true
			c, err := r.Cookie("session")
			if err != nil || c.Value != issuedCookie {
				t.Fatalf("get-token 缺失或不匹配的 session cookie: err=%v cookie=%v", err, c)
			}
			require.Equal(t, "42", r.Header.Get("New-Api-User"))
			_, _ = w.Write([]byte(`{"success":true,"data":"access-tok-xyz"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	got, err := client.BootstrapUserAccessToken(context.Background(), "alice", "pw")
	require.NoError(t, err)
	if !loginCalled || !tokenCalled {
		t.Fatalf("expected login + get-token, got login=%v token=%v", loginCalled, tokenCalled)
	}
	require.Equal(t, "access-tok-xyz", got)
}

// decodeJSONBody 是测试 helper：把请求 body 解到 target；忽略错误（测试中调用方自行检查 target）。
func decodeJSONBody(r *http.Request, target any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

// int64Of 把 json 数字（float64 默认）转 int64，方便测试比对。
func int64Of(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	default:
		return 0
	}
}

// TestClient_DeleteUser_AdminAuthHeaders 验证客户端删除用户管理员认证响应头的预期行为场景。
func TestClient_DeleteUser_AdminAuthHeaders(t *testing.T) {
	var (
		gotAuthHeader string
		gotUserHeader string
		gotMethod     string
		gotPath       string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		gotUserHeader = r.Header.Get("New-Api-User")
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true,"message":"deleted"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "admin-token", 1)
	err := c.DeleteUser(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, "DELETE", gotMethod)
	assert.Equal(t, "/api/user/99", gotPath)
	assert.Equal(t, "Bearer admin-token", gotAuthHeader)
	assert.Equal(t, "1", gotUserHeader)
}

// TestClient_DeleteUser_NotFoundMappedToErrNotFound 验证客户端删除用户未找到Mapped到错误未找到的异常或拒绝路径场景。
func TestClient_DeleteUser_NotFoundMappedToErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "t", 1)
	err := c.DeleteUser(context.Background(), 999)
	require.ErrorIs(t, err, ErrNotFound)
}

// fakeRefresher 模拟 access_token 自愈逻辑。
type fakeRefresher struct {
	callCount       int
	nextAccessToken string
	err             error
}

func (f *fakeRefresher) RefreshAccessToken(ctx context.Context) (string, error) {
	f.callCount++
	if f.err != nil {
		return "", f.err
	}
	return f.nextAccessToken, nil
}

// TestUserScopedClient_401TriggersRefreshAndRetries 验证用户Scoped客户端401Triggers刷新并Retries的预期行为场景。
func TestUserScopedClient_401TriggersRefreshAndRetries(t *testing.T) {
	var requestCount int
	var lastAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		lastAuthHeader = r.Header.Get("Authorization")
		if requestCount == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true,"data":{"key":"sk-fresh"}}`))
	}))
	defer srv.Close()

	base := NewClient(srv.URL, "a", 1)
	refresher := &fakeRefresher{nextAccessToken: "fresh-token"}
	user := base.AsUserWithRefresh(99, "stale-token", refresher)

	fullKey, err := user.GetTokenFullKey(context.Background(), 13)
	require.NoError(t, err)
	assert.Equal(t, "sk-fresh", fullKey)
	assert.Equal(t, 2, requestCount)
	assert.Equal(t, "Bearer fresh-token", lastAuthHeader)
	assert.Equal(t, 1, refresher.callCount)
}

// TestUserScopedClient_401WithoutRefresherPropagates 验证用户Scoped客户端401不使用Refresher透传的错误映射或错误记录场景。
func TestUserScopedClient_401WithoutRefresherPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	base := NewClient(srv.URL, "", 0)
	user := base.AsUser(99, "stale-token") // 旧签名，无 refresher
	_, err := user.GetTokenFullKey(context.Background(), 13)
	require.ErrorIs(t, err, ErrUnauthorized)
}

// TestUserScopedClient_RefresherFailurePropagatesUnauthorized 验证用户Scoped客户端Refresher失败透传未授权的异常或拒绝路径场景。
func TestUserScopedClient_RefresherFailurePropagatesUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	base := NewClient(srv.URL, "", 0)
	refresher := &fakeRefresher{err: errors.New("login 5xx")}
	user := base.AsUserWithRefresh(99, "stale-token", refresher)
	_, err := user.GetTokenFullKey(context.Background(), 13)
	require.ErrorIs(t, err, ErrUnauthorized)
}

// TestUserScopedClient_SecondCall401AfterRefreshDoesNotLoop 验证用户Scoped客户端SecondCall401后刷新Does未循环的预期行为场景。
func TestUserScopedClient_SecondCall401AfterRefreshDoesNotLoop(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	base := NewClient(srv.URL, "", 0)
	refresher := &fakeRefresher{nextAccessToken: "fresh"}
	user := base.AsUserWithRefresh(99, "stale", refresher)
	_, err := user.GetTokenFullKey(context.Background(), 13)
	require.ErrorIs(t, err, ErrUnauthorized)
	assert.Equal(t, 2, requestCount)
}
