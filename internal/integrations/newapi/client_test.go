package newapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestUserScopedCreateAPIKeyHappyPath 校验 user-scoped client 调 POST /api/token/ 时携带
// user 鉴权两件套（Authorization Bearer + New-Api-User），并能解析 success+data.id。
func TestUserScopedCreateAPIKeyHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/token/" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer user-token" {
			t.Fatalf("auth = %q, want Bearer user-token", got)
		}
		if got := r.Header.Get("New-Api-User"); got != "7" {
			t.Fatalf("new-api-user header = %q, want 7", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"user_id":7,"name":"alice","key":"sk-truncated","remain_quota":1000}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin-token", 1)
	user := client.AsUser(7, "user-token")
	got, err := user.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice", Quota: 1000})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if got.ID != 42 {
		t.Fatalf("api key id = %d, want 42", got.ID)
	}
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
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
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
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
	if !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("error message lost upstream context: %v", err)
	}
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
		if r.URL.Path != "/api/token/42/key" {
			t.Fatalf("path = %q, want /api/token/42/key", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		if got := r.Header.Get("New-Api-User"); got != "7" {
			t.Fatalf("new-api-user = %q, want 7", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"key":"sk-real-1234567890abcdef"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	got, err := client.AsUser(7, "tok").GetTokenFullKey(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetTokenFullKey() error = %v", err)
	}
	if got != "sk-real-1234567890abcdef" {
		t.Fatalf("key = %q", got)
	}
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
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// TestNewApiUserHeaderOmittedWhenAdminUserIDZero 校验 AdminUserID=0 时不发送 New-Api-User
// header；旧测试构造空 client 依赖此行为，避免 strict mock 拒绝未知 header。
func TestNewApiUserHeaderOmittedWhenAdminUserIDZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("New-Api-User"); got != "" {
			t.Fatalf("new-api-user header = %q, want empty when AdminUserID=0", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"remain_quota":0,"used_quota":0}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	if _, err := client.GetUserBalance(context.Background(), 1); err != nil {
		t.Fatalf("GetUserBalance() error = %v", err)
	}
}

// TestRechargeUserUsesManageEndpoint 校验 RechargeUser 改走 POST /api/user/manage
// (action=add_quota, mode=add)，避免之前 GET-修改-PUT 整对象的并发覆盖风险。
// 紧跟一次 GET /api/user/{id} 把"加完后的 quota"拉回作为 RechargeResult.RemainQuota。
func TestRechargeUserUsesManageEndpoint(t *testing.T) {
	var (
		gotManage bool
		gotGet    bool
		manageBody map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
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
	if err != nil {
		t.Fatalf("RechargeUser() error = %v", err)
	}
	if !gotManage || !gotGet {
		t.Fatalf("expected manage + GET, got manage=%v get=%v", gotManage, gotGet)
	}
	if manageBody["action"] != "add_quota" || manageBody["mode"] != "add" {
		t.Fatalf("manage body action/mode wrong: %v", manageBody)
	}
	if int64Of(manageBody["value"]) != 1000 {
		t.Fatalf("manage body value = %v, want 1000", manageBody["value"])
	}
	if got.RemainQuota != 1500 {
		t.Fatalf("RemainQuota = %d, want 1500 (post-recharge fresh GET)", got.RemainQuota)
	}
	if got.RefID == "" {
		t.Fatalf("RefID empty; need synthesized id for audit reconciliation")
	}
}

// TestCreateUserCallsAdminEndpoint 校验 CreateUser 调 admin POST /api/user/ 并回查 user_id：
// new-api v1 该端点响应不返回 data.id，client 必须通过 GET /api/user/search?keyword=username
// 拿到完整 user 实体。
func TestCreateUserCallsAdminEndpoint(t *testing.T) {
	var (
		gotPost bool
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
			if r.URL.Query().Get("keyword") != "alice" {
				t.Fatalf("search keyword = %q", r.URL.Query().Get("keyword"))
			}
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
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if !gotPost || !gotSearch {
		t.Fatalf("expected POST + search, got post=%v search=%v", gotPost, gotSearch)
	}
	if user.ID != 99 {
		t.Fatalf("user.ID = %d, want 99 (resolved via search fallback)", user.ID)
	}
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
			if got := r.Header.Get("New-Api-User"); got != "42" {
				t.Fatalf("New-Api-User header = %q, want 42 (从 login data.id 派生)", got)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":"access-tok-xyz"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	got, err := client.BootstrapUserAccessToken(context.Background(), "alice", "pw")
	if err != nil {
		t.Fatalf("BootstrapUserAccessToken() error = %v", err)
	}
	if !loginCalled || !tokenCalled {
		t.Fatalf("expected login + get-token, got login=%v token=%v", loginCalled, tokenCalled)
	}
	if got != "access-tok-xyz" {
		t.Fatalf("access_token = %q, want access-tok-xyz", got)
	}
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
