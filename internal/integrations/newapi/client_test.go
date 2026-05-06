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

func TestCreateAPIKeyHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/token/" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer admin-token" {
			t.Fatalf("auth = %q", got)
		}
		// new-api admin API 要求 New-Api-User header 与 access_token 所属用户匹配；
		// client 必须把 AdminUserID 渲染成该 header，否则请求会被 new-api 拒绝。
		if got := r.Header.Get("New-Api-User"); got != "1" {
			t.Fatalf("new-api-user header = %q, want 1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"user_id":7,"name":"alice","key":"sk-test","remain_quota":1000}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "admin-token", 1)
	got, err := client.CreateAPIKey(context.Background(), CreateAPIKeyInput{UserID: 7, Name: "alice", Quota: 1000})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if got.ID != 42 || got.Key != "sk-test" {
		t.Fatalf("api key = %+v", got)
	}
}

func TestCreateAPIKeyMapsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	_, err := client.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error = %v, want ErrUnauthorized", err)
	}
}

func TestCreateAPIKeyMapsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	_, err := client.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestCreateAPIKeyMapsUpstream5xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	_, err := client.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice"})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
}

func TestCreateAPIKeySurfacesUpstreamSuccessFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"message":"quota exhausted"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	_, err := client.CreateAPIKey(context.Background(), CreateAPIKeyInput{Name: "alice"})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
	if !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("error message lost upstream context: %v", err)
	}
}

func TestSetAPIKeyStatusPropagatesErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	if err := client.SetAPIKeyStatus(context.Background(), 1, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v, want ErrConflict", err)
	}
}

func TestGetAPIKeyDecodesPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":42}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	got, err := client.GetAPIKey(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
	if got.ID != 42 {
		t.Fatalf("id = %d", got.ID)
	}
}

// TestNewApiUserHeaderOmittedWhenAdminUserIDZero 校验 AdminUserID = 0 时不发送 New-Api-User header；
// 部分 fake/mock 场景（旧测试构造空 client）依赖此行为，避免 strict mock 拒绝未知 header。
func TestNewApiUserHeaderOmittedWhenAdminUserIDZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("New-Api-User"); got != "" {
			t.Fatalf("new-api-user header = %q, want empty when AdminUserID=0", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":1}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", 0)
	if _, err := client.GetAPIKey(context.Background(), 1); err != nil {
		t.Fatalf("GetAPIKey() error = %v", err)
	}
}

// TestRechargeUserUsesGetThenPut 校验 RechargeUser 走 GET → 改 quota → PUT 三步。
// new-api v1 没有 /api/user/recharge endpoint；正确路径是 GET /api/user/{id}
// 拿当前 user 对象（含完整字段），把 quota 字段累加充值额，再 PUT /api/user/。
func TestRechargeUserUsesGetThenPut(t *testing.T) {
	var (
		gotGet bool
		gotPut bool
		putBody map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/42":
			gotGet = true
			// 模拟 new-api 返回的 user 对象，含 quota 与额外字段（如 group / role）
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"username":"alice","quota":500,"used_quota":12,"group":"default","role":1,"status":1}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/user/":
			gotPut = true
			_ = json.NewDecoder(r.Body).Decode(&putBody)
			_, _ = w.Write([]byte(`{"success":true,"message":""}`))
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
	if !gotGet || !gotPut {
		t.Fatalf("expected GET then PUT, got GET=%v PUT=%v", gotGet, gotPut)
	}
	// 必须把当前 quota (500) 加上充值额 (1000) 后写回
	if q, ok := putBody["quota"]; !ok {
		t.Fatalf("PUT body missing quota field: %v", putBody)
	} else if int64Of(q) != 1500 {
		t.Fatalf("PUT body quota = %v, want 1500", q)
	}
	// 必须保留原 user 对象其他字段，避免 PUT 把 username / group / role 清空
	if putBody["username"] != "alice" {
		t.Fatalf("PUT body lost username: %v", putBody)
	}
	if putBody["group"] != "default" {
		t.Fatalf("PUT body lost group: %v", putBody)
	}
	// RechargeResult.RemainQuota 必须反映加完后的最新值
	if got.RemainQuota != 1500 {
		t.Fatalf("RemainQuota = %d, want 1500", got.RemainQuota)
	}
	// RefID 自生成（new-api 没有该概念），用于 audit 对账，不能为空
	if got.RefID == "" {
		t.Fatalf("RefID empty; need synthesized id for audit reconciliation")
	}
}

// TestCreateAPIKeyFallsBackToListWhenIDMissing 校验 new-api v1 的 POST /api/token/
// 响应不含 data.id 时，client 用 token name 通过 list 接口回查 id。
//
// 注意：key 字段不强制——new-api 安全策略下 POST/GET 都不返回完整 key，本 client 只保证
// 拿到 id 用于后续 disable/restore；上层用 yaml 全局 sk- token 注入容器。
func TestCreateAPIKeyFallsBackToListWhenIDMissing(t *testing.T) {
	getCalled := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/token/":
			// 响应 success=true 但 data.id 为 null（new-api v1.0.0-alpha.1 行为）
			_, _ = w.Write([]byte(`{"success":true,"message":""}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/":
			// fallback list：含 id（key 字段空，模拟 new-api 安全策略）
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":99,"name":"alice","key":"","user_id":7}]}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/token/99":
			// 单条详情：返回 key=空 模拟 new-api truncated 策略
			getCalled++
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":99,"name":"alice","key":"","user_id":7}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok", 1)
	got, err := client.CreateAPIKey(context.Background(), CreateAPIKeyInput{UserID: 7, Name: "alice", Quota: 1000})
	if err != nil {
		t.Fatalf("CreateAPIKey() error = %v", err)
	}
	if got.ID != 99 {
		t.Fatalf("ID = %d, want 99 (resolved via fallback list)", got.ID)
	}
	// key 字段允许为空（上层走 yaml 全局 token），不再断言 key 非空
	if getCalled != 1 {
		t.Fatalf("expected 1 GetAPIKey call to enrich token metadata, got %d", getCalled)
	}
}

// int64Of 把 json 数字（float64 默认）转 int64，方便测试比对
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
