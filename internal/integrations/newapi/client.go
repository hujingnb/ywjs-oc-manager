// Package newapi 封装与 new-api 网关的交互。
//
// 设计要点：
//   - 仅暴露 manager 当前实际使用的能力，避免泄漏 new-api 全部 API 表面；
//   - 错误统一映射成 sentinel error，便于 worker handler 区分“需重试 vs 立即失败”；
//   - 调用方必须传入超时 context，client 自身不内置长超时，避免阻塞 worker 线程。
package newapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 与 new-api 调用相关的错误。
var (
	ErrNotFound       = errors.New("new-api 资源不存在")
	ErrUnauthorized   = errors.New("new-api 鉴权失败")
	ErrConflict       = errors.New("new-api 资源冲突")
	ErrUpstream       = errors.New("new-api 网关异常")
	ErrPayloadInvalid = errors.New("new-api 返回体无法解析")
)

// APIKey 描述 new-api 中的 token 实体。
type APIKey struct {
	ID         int64    `json:"id"`
	UserID     int64    `json:"user_id"`
	Name       string   `json:"name"`
	Key        string   `json:"key,omitempty"`
	RemainQuota int64   `json:"remain_quota"`
	Models     []string `json:"models"`
	Status     int      `json:"status"`
}

// CreateAPIKeyInput 是创建 token 的入参。
type CreateAPIKeyInput struct {
	UserID     int64
	Name       string
	Models     []string
	Quota      int64
	Group      string
	UnlimitedQ bool
}

// Client 是 new-api 的 HTTP 客户端。
//
// AdminToken：new-api「个人设置 → 安全设置 → 系统访问令牌」生成的 access_token。
// AdminUserID：access_token 所属的 new-api 用户 id，admin API 要求作为 New-Api-User header 同时携带；
// 二者缺一会被 new-api 拒绝（参考 https://www.newapi.ai/zh/docs/api/management/auth）。
type Client struct {
	BaseURL     string
	AdminToken  string
	AdminUserID int64
	HTTPClient  *http.Client
}

// NewClient 构造 new-api client，未提供 HTTPClient 时使用 http.DefaultClient。
// adminUserID 必须与 adminToken 所属用户匹配，否则 admin API 返回 "Unauthorized"。
func NewClient(baseURL, adminToken string, adminUserID int64) *Client {
	return &Client{BaseURL: baseURL, AdminToken: adminToken, AdminUserID: adminUserID}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// CreateAPIKey 调用 new-api 创建 token。
// 错误按状态码统一映射；2xx + ok=false 也按 ErrUpstream 处理，避免静默成功。
func (c *Client) CreateAPIKey(ctx context.Context, input CreateAPIKeyInput) (APIKey, error) {
	if c.BaseURL == "" {
		return APIKey{}, fmt.Errorf("new-api client 未配置 BaseURL")
	}
	body := map[string]any{
		"user_id":              input.UserID,
		"name":                 input.Name,
		"models":               input.Models,
		"remain_quota":         input.Quota,
		"unlimited_quota":      input.UnlimitedQ,
		"group":                input.Group,
		"expired_time":         -1,
		"status":               1,
	}
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    APIKey `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/token/", body, &response); err != nil {
		return APIKey{}, err
	}
	if !response.Success {
		return APIKey{}, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	// new-api v1 的 POST /api/token/ 响应不返回新 token 的 id 与 key 字段；
	// 后续禁用 / 恢复 / 删除都需要 id，所以这里立即按 token name 查列表回填。
	// 同 user 下不允许重名 token，按 name 精确匹配是稳定的。
	//
	// key 字段无法通过 new-api admin API 拿到（POST 不返回，GET 也只返回 truncated 18 字符）。
	// 上层 worker 不应使用这里返回的 Key 字段做 chat completions 鉴权——而是统一用 yaml
	// 配置的 cfg.OpenClaw.LLM.OpenAICompat.APIKey 全局 sk- token。这里仅保留 id / status
	// 等可观测字段，便于后续 disable / restore 操作。
	resolved := response.Data
	if resolved.ID == 0 {
		fallback, err := c.findTokenByName(ctx, input.Name)
		if err != nil {
			return APIKey{}, fmt.Errorf("%w: 创建 token 成功但无法回查 id: %v", ErrPayloadInvalid, err)
		}
		resolved = fallback
	}
	return resolved, nil
}

// findTokenByName 按 token name 在当前 admin user 的 token 列表里精确匹配并返回。
// 用于补 new-api v1 的 POST /api/token/ 不返回 id 的缺口。
//
// 注意：new-api 的 list endpoint 出于安全不返回 key 字段（明文 token 只在 GET 单条
// 才返回），所以拿到 id 后必须再调 GetAPIKey 拉取完整记录（含 key），否则上层会把
// 空字符串当成 api_key 注入容器，导致后续 chat completions 401 Invalid token。
func (c *Client) findTokenByName(ctx context.Context, name string) (APIKey, error) {
	var listResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			// new-api v1 列表分页字段名为 items；旧版可能用 records。两者都解析。
			Items   []APIKey `json:"items"`
			Records []APIKey `json:"records"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/token/?p=1&size=100", nil, &listResp); err != nil {
		return APIKey{}, err
	}
	if !listResp.Success {
		return APIKey{}, fmt.Errorf("%w: %s", ErrUpstream, listResp.Message)
	}
	candidates := listResp.Data.Items
	if len(candidates) == 0 {
		candidates = listResp.Data.Records
	}
	for _, k := range candidates {
		if k.Name == name {
			// list 返回的 k 仅 id / name 等基础字段；GetAPIKey 能补齐 user_id / status，
			// 但 key 字段始终是 truncated（new-api 安全策略，POST/GET 都不返回完整 key）。
			// 上层 worker 不依赖这里返回的 key 字段，而是用 yaml 全局 sk- token 注入容器。
			full, err := c.GetAPIKey(ctx, k.ID)
			if err != nil {
				return k, nil
			}
			return full, nil
		}
	}
	return APIKey{}, fmt.Errorf("token name=%q not found in list", name)
}

// GetAPIKey 查询 token 详情。
// new-api 对不存在的 token id 返回 200 + {success:false, message:"record not found"}，
// 这里把它显式映射成 ErrNotFound，避免 usage service 把"已被回收 token"当成 5xx 错误吞掉整个聚合。
func (c *Client) GetAPIKey(ctx context.Context, id int64) (APIKey, error) {
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    APIKey `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/token/%d", id), nil, &response); err != nil {
		return APIKey{}, err
	}
	if !response.Success {
		if strings.Contains(strings.ToLower(response.Message), "not found") {
			return APIKey{}, ErrNotFound
		}
		return APIKey{}, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	return response.Data, nil
}

// RechargeInput 是组织充值的入参。
// CreditAmount 必须为正数；Remark 由调用方按业务策略组装（操作员 + 业务说明）。
type RechargeInput struct {
	NewAPIUserID int64
	CreditAmount int64
	Remark       string
}

// RechargeResult 描述 new-api 返回的充值结果。
// RefID 用于 manager 端写入 recharge_records.newapi_ref_id，便于跨系统对账。
type RechargeResult struct {
	RefID       string
	RemainQuota int64
}

// BalanceResult 描述某个 new-api 用户的当前余额视图。
type BalanceResult struct {
	NewAPIUserID int64
	RemainQuota  int64
	UsedQuota    int64
}

// RechargeUser 给指定 new-api 用户增加点数。
//
// new-api v1 没有 /api/user/recharge endpoint，admin 给指定 user 加 quota 必须走：
//  1. GET /api/user/{id} 取整个 user 对象
//  2. quota 字段累加充值额
//  3. PUT /api/user/ 把整个 user 对象写回
//
// PUT 必须携带完整 user 对象（含 username / group / role / status 等）；
// 若仅传 quota 字段，new-api 会把其余字段当成 zero-value 覆盖。
//
// new-api 自身没有 ref_id 概念，本函数生成 manager 端对账 ID 写入 RechargeResult.RefID，
// 上层把它存入 recharge_records.newapi_ref_id 用于审计。
func (c *Client) RechargeUser(ctx context.Context, input RechargeInput) (RechargeResult, error) {
	if input.CreditAmount <= 0 {
		return RechargeResult{}, fmt.Errorf("credit_amount 必须为正")
	}

	// 1. GET 拿当前 user 对象，用 map 接收避免漏字段
	var getResp struct {
		Success bool           `json:"success"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/user/%d", input.NewAPIUserID), nil, &getResp); err != nil {
		return RechargeResult{}, err
	}
	if !getResp.Success {
		return RechargeResult{}, fmt.Errorf("%w: %s", ErrUpstream, getResp.Message)
	}
	if getResp.Data == nil {
		return RechargeResult{}, fmt.Errorf("%w: GET /api/user/%d 返回空 data", ErrPayloadInvalid, input.NewAPIUserID)
	}

	// 2. quota 字段累加；new-api 默认用 json.Number → 退化到 float64，转 int64
	currentQuota := jsonNumberToInt64(getResp.Data["quota"])
	newQuota := currentQuota + input.CreditAmount
	getResp.Data["quota"] = newQuota

	// 3. PUT 整个对象写回
	var putResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := c.do(ctx, http.MethodPut, "/api/user/", getResp.Data, &putResp); err != nil {
		return RechargeResult{}, err
	}
	if !putResp.Success {
		return RechargeResult{}, fmt.Errorf("%w: %s", ErrUpstream, putResp.Message)
	}

	// new-api 没 ref_id；自己合成一个，便于 audit 对账
	refID := fmt.Sprintf("manager-%d-%d", input.NewAPIUserID, time.Now().UnixNano())
	return RechargeResult{RefID: refID, RemainQuota: newQuota}, nil
}

// jsonNumberToInt64 把 JSON 数字字段安全转成 int64。
// encoding/json 反序列化数字到 interface{} 时默认得到 float64；
// 手动 Unmarshal 时也可能拿到 json.Number；本函数对两种都能正确处理。
func jsonNumberToInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	case json.Number:
		i, _ := x.Int64()
		return i
	default:
		return 0
	}
}

// GetUserBalance 查询单个 new-api 用户的余额。
func (c *Client) GetUserBalance(ctx context.Context, newapiUserID int64) (BalanceResult, error) {
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			RemainQuota int64 `json:"remain_quota"`
			UsedQuota   int64 `json:"used_quota"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/user/%d", newapiUserID), nil, &response); err != nil {
		return BalanceResult{}, err
	}
	if !response.Success {
		return BalanceResult{}, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	return BalanceResult{
		NewAPIUserID: newapiUserID,
		RemainQuota:  response.Data.RemainQuota,
		UsedQuota:    response.Data.UsedQuota,
	}, nil
}

// SetAPIKeyStatus 启用或禁用 token。
// status: 1 启用、2 禁用。
func (c *Client) SetAPIKeyStatus(ctx context.Context, id int64, status int) error {
	body := map[string]any{"id": id, "status": status}
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := c.do(ctx, http.MethodPut, "/api/token/?status_only=true", body, &response); err != nil {
		return err
	}
	if !response.Success {
		return fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, body any, target any) error {
	// path 可能含 query string（如 "/api/token/?status_only=true"）；url.JoinPath 会把 "?" 转义为 "%3F"，
	// 导致 new-api 收到的是带字面 "?" 的 path 而非真正 query，进而无法识别参数。
	// 把 path 拆成 raw_path + query，分别拼接。
	rawPath, query, _ := strings.Cut(path, "?")
	endpoint, err := url.JoinPath(c.BaseURL, rawPath)
	if err != nil {
		return err
	}
	if query != "" {
		endpoint += "?" + query
	}
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化请求失败: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.AdminToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AdminToken)
	}
	// new-api admin API 要求 New-Api-User header 标识当前调用者用户 id，
	// 且必须与 access_token 所属用户匹配；缺失时 new-api 返回
	// "Unauthorized, New-Api-User header not provided"。
	if c.AdminUserID > 0 {
		req.Header.Set("New-Api-User", fmt.Sprintf("%d", c.AdminUserID))
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("调用 new-api 失败: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusConflict:
		return ErrConflict
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: status=%d", ErrUpstream, resp.StatusCode)
	}
	if target == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("%w: %s", ErrPayloadInvalid, err.Error())
	}
	return nil
}
