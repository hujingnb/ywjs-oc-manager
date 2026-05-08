// Package newapi 封装与 new-api 网关的交互。
//
// 职责边界：
//   - 仅暴露 manager 当前实际使用的能力，避免泄漏 new-api 全部 API 表面；
//   - 错误统一映射成 sentinel error，便于 worker handler 区分"需重试 vs 立即失败"；
//   - 调用方必须传入超时 context，client 自身不内置长超时，避免阻塞 worker 线程。
//
// 鉴权身份分两层：
//   - 顶层 Client 用 admin access_token（manager.yaml 的 newapi.admin_token），
//     调度组织 / 用户 / 全局统计这类管理面接口；
//   - UserScopedClient 用业务 user 自己的 access_token，专门用于 token 增删改与
//     "拿完整 sk-"（new-api `POST /api/token/:id/key` 强制按调用者 user_id 过滤），
//     由 Client.AsUser(userID, accessToken) 派生。
package newapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
)

// 与 new-api 调用相关的错误。
var (
	ErrNotFound       = errors.New("new-api 资源不存在")
	ErrUnauthorized   = errors.New("new-api 鉴权失败")
	ErrConflict       = errors.New("new-api 资源冲突")
	ErrUpstream       = errors.New("new-api 网关异常")
	ErrPayloadInvalid = errors.New("new-api 返回体无法解析")
)

// User 描述 new-api 中的 user 实体（仅含 manager 真正用到的字段）。
type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        int    `json:"role"`
	Status      int    `json:"status"`
	Group       string `json:"group"`
	Quota       int64  `json:"quota"`
	UsedQuota   int64  `json:"used_quota"`
}

// CreateUserInput 是 admin 创建 user 的入参。
// new-api 的 POST /api/user/ 要求 username + password 必填，其它字段可选。
type CreateUserInput struct {
	Username    string
	Password    string
	DisplayName string
}

// APIKey 描述 new-api 中的 token 实体。
type APIKey struct {
	ID          int64    `json:"id"`
	UserID      int64    `json:"user_id"`
	Name        string   `json:"name"`
	Key         string   `json:"key,omitempty"`
	RemainQuota int64    `json:"remain_quota"`
	Models      []string `json:"models"`
	Status      int      `json:"status"`
}

// CreateAPIKeyInput 是创建 token 的入参。
//
// 用 UserScopedClient.CreateAPIKey 调用时不需要传 UserID；token 自动归属调用 user。
// 保留 UserID 字段是为了兼容 new-api 的 admin 直接创 token 并显式指定 owner 的场景，
// 但本仓库当前不走这条路（admin token 调 POST /api/token/:id/key 拿不到别 user 的完整 sk-）。
type CreateAPIKeyInput struct {
	UserID     int64
	Name       string
	Models     []string
	Quota      int64
	Group      string
	UnlimitedQ bool
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

// LogsQuery 控制 GetTokenLogs 的过滤条件；零值字段表示不过滤。
//
// 时间范围 Since / Until 是 unix 秒；new-api 端字段名为 start_timestamp / end_timestamp。
// PageSize 缺省 20，对应 new-api `p=1&page_size=20`。
type LogsQuery struct {
	TokenID   int64
	UserID    int64
	Username  string
	ModelName string
	Since     int64
	Until     int64
	Page      int
	PageSize  int
}

// LogEntry 描述一条调用日志。字段名对齐 new-api `controller.GetAllLogs` 的响应。
type LogEntry struct {
	ID               int64  `json:"id"`
	UserID           int64  `json:"user_id"`
	Username         string `json:"username"`
	TokenID          int64  `json:"token_id"`
	TokenName        string `json:"token_name"`
	ModelName        string `json:"model_name"`
	Quota            int64  `json:"quota"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	UseTime          int    `json:"use_time"`
	CreatedAt        int64  `json:"created_at"`
}

// LogsPage 是 LogsQuery 的分页响应。
type LogsPage struct {
	Items []LogEntry
	Total int
}

// QuotaDate 是 new-api `controller.GetAllQuotaDates / GetQuotaDatesByUser` 的单条记录。
type QuotaDate struct {
	Date      string `json:"date"`
	ModelName string `json:"model_name"`
	Count     int    `json:"count"`
	Quota     int64  `json:"quota"`
	Tokens    int    `json:"token_used"`
}

// Client 是 new-api 的 HTTP 客户端（admin 身份）。
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

// CreateUser 调 admin POST /api/user/ 创建普通业务 user。
//
// new-api 响应不返回新建 user 的 id（实测 v1.0.0-alpha.1：仅 {success:true,message:""}），
// 调用方应紧跟 FindUserByUsername 回查 id。username 必须全局唯一，重名时 new-api 返回 success=false。
func (c *Client) CreateUser(ctx context.Context, input CreateUserInput) (User, error) {
	if c.BaseURL == "" {
		return User{}, fmt.Errorf("new-api client 未配置 BaseURL")
	}
	if input.Username == "" || input.Password == "" {
		return User{}, fmt.Errorf("new-api CreateUser: username / password 必填")
	}
	display := input.DisplayName
	if display == "" {
		display = input.Username
	}
	body := map[string]any{
		"username":     input.Username,
		"password":     input.Password,
		"display_name": display,
		// role=1 对应 new-api common.RoleCommonUser；role=0 在 controller 内不通过 validator。
		"role": 1,
	}
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/user/", body, &response); err != nil {
		return User{}, err
	}
	if !response.Success {
		return User{}, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	// 回查 id：new-api POST /api/user/ 不返回 data，必须通过 search 再查一次。
	user, err := c.FindUserByUsername(ctx, input.Username)
	if err != nil {
		return User{}, fmt.Errorf("%w: 创建 user 成功但无法回查 id: %v", ErrPayloadInvalid, err)
	}
	return user, nil
}

// FindUserByUsername 用 admin GET /api/user/search?keyword=<username> 精确匹配 user。
//
// new-api 的 search 是模糊查询，可能返回多条；这里取 username 完全相等的那条，
// 没匹配返回 ErrNotFound。同 username 在 new-api 是唯一的，理论上至多 1 条精确匹配。
func (c *Client) FindUserByUsername(ctx context.Context, username string) (User, error) {
	if username == "" {
		return User{}, fmt.Errorf("FindUserByUsername: username 不能为空")
	}
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Items   []User `json:"items"`
			Records []User `json:"records"`
		} `json:"data"`
	}
	path := "/api/user/search?keyword=" + url.QueryEscape(username)
	if err := c.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return User{}, err
	}
	if !response.Success {
		return User{}, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	candidates := response.Data.Items
	if len(candidates) == 0 {
		candidates = response.Data.Records
	}
	for _, u := range candidates {
		if u.Username == username {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

// BootstrapUserAccessToken 用业务 user 凭据走 login → 拿 cookie → GET /api/user/token 取 access_token。
//
// 仅在组织创建时调用一次：
//  1. POST /api/user/login 验证 username/password，session 写入 cookie；
//  2. 带该 cookie 调 GET /api/user/token，new-api 给 user.access_token 字段赋一个新随机串并返回。
//
// 返回的 access_token 永久有效（除非有人在 new-api UI 主动重置），加密落库后所有 user-scoped
// 调用都用它做 Bearer，避免每次都 login。
//
// 前置约束：new-api 后台 turnstile_check 必须为 false（POST /api/user/login 路由挂了
// middleware.TurnstileCheck()），否则 server-to-server login 会被拦。
func (c *Client) BootstrapUserAccessToken(ctx context.Context, username, password string) (string, error) {
	if c.BaseURL == "" {
		return "", fmt.Errorf("new-api client 未配置 BaseURL")
	}
	if username == "" || password == "" {
		return "", fmt.Errorf("BootstrapUserAccessToken: username / password 必填")
	}

	// 用独立 jar 暂存 session cookie，调完即丢；不污染外部 HTTPClient。
	jar, err := cookiejar.New(nil)
	if err != nil {
		return "", fmt.Errorf("初始化 cookie jar 失败: %w", err)
	}
	httpClient := *c.httpClient()
	httpClient.Jar = jar

	// 1. login —— 响应 data.id 是新会话所属 user_id，下一步 GET /api/user/token 必须用它
	//    填 New-Api-User header（new-api 即使在 session 鉴权下也要求该 header 显式标识 user）。
	loginBody := map[string]string{"username": username, "password": password}
	loginReq, err := c.newRequest(ctx, http.MethodPost, "/api/user/login", loginBody)
	if err != nil {
		return "", err
	}
	var loginResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := executeRequest(&httpClient, loginReq, &loginResp); err != nil {
		return "", fmt.Errorf("login 阶段失败: %w", err)
	}
	if !loginResp.Success {
		return "", fmt.Errorf("%w: login: %s", ErrUpstream, loginResp.Message)
	}
	if loginResp.Data.ID == 0 {
		return "", fmt.Errorf("%w: login 响应未携带 data.id", ErrPayloadInvalid)
	}

	// 2. GET /api/user/token —— 走 UserAuth：session cookie + New-Api-User 必须同时携带。
	tokenReq, err := c.newRequest(ctx, http.MethodGet, "/api/user/token", nil)
	if err != nil {
		return "", err
	}
	tokenReq.Header.Set("New-Api-User", strconv.FormatInt(loginResp.Data.ID, 10))
	var tokenResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    string `json:"data"`
	}
	if err := executeRequest(&httpClient, tokenReq, &tokenResp); err != nil {
		return "", fmt.Errorf("拿 access_token 阶段失败: %w", err)
	}
	if !tokenResp.Success {
		return "", fmt.Errorf("%w: get-token: %s", ErrUpstream, tokenResp.Message)
	}
	if tokenResp.Data == "" {
		return "", fmt.Errorf("%w: GET /api/user/token 返回空 access_token", ErrPayloadInvalid)
	}
	return tokenResp.Data, nil
}

// CredentialsRefresher 抽象 access_token 失效时的自愈能力。
//
// OOS-2 设计：UserScopedClient.do() 收到 401 时调一次 RefreshAccessToken，
// 拿到新 token 后更新内部 accessToken 字段并重试一次。第二次仍 401 直接 propagate。
//
// refresher 的实现通常需要：
//  1. 在 SELECT ... FOR UPDATE 锁住组织行（避免并发自愈互踩）；
//  2. 解密密文 → password；
//  3. 调 BootstrapUserAccessToken 拿新 access_token；
//  4. 重新加密 → UpdateOrganizationCredentialsCiphertext 写回；
//  5. 返回新 access_token。
type CredentialsRefresher interface {
	RefreshAccessToken(ctx context.Context) (string, error)
}

// UserScopedClient 用业务 user access_token 调 user-scoped 接口。
//
// 与顶层 Client 共享 BaseURL / HTTPClient / 错误映射逻辑，区别仅在请求头：
//   - Authorization: Bearer <user.access_token>
//   - New-Api-User: <user.id>
//
// `POST /api/token/:id/key` 在 new-api 内部按 c.GetInt("id") 过滤 token，所以
// admin token 拿不到别 user 的完整 sk-，必须由 UserScopedClient 调用。
type UserScopedClient struct {
	base        *Client
	userID      int64
	accessToken string
	refresher   CredentialsRefresher // 可选；nil 时不自愈，401 直接 propagate
}

// AsUser 返回一个 user-scoped client view，后续 token 操作通过它走业务 user 鉴权。
func (c *Client) AsUser(userID int64, accessToken string) *UserScopedClient {
	return &UserScopedClient{base: c, userID: userID, accessToken: accessToken}
}

// AsUserWithRefresh 构造带自愈能力的 user-scoped client。
//
// refresher 用于在 do() 收到 401 时一次性自愈。同一个 client 实例的
// 401 → refresh → retry 至多触发 1 次，避免无限循环。
func (c *Client) AsUserWithRefresh(userID int64, accessToken string, refresher CredentialsRefresher) *UserScopedClient {
	return &UserScopedClient{base: c, userID: userID, accessToken: accessToken, refresher: refresher}
}

// CreateAPIKey 以 user 身份调 POST /api/token/ 创建 token。
//
// new-api v1 该接口的响应不返回新 token 的 id 与完整 key，需要回查：
//   - id：通过 list 接口按 token name 精确匹配；
//   - 完整 key：必须再调 POST /api/token/:id/key（即 GetTokenFullKey）。
func (u *UserScopedClient) CreateAPIKey(ctx context.Context, input CreateAPIKeyInput) (APIKey, error) {
	if u.base.BaseURL == "" {
		return APIKey{}, fmt.Errorf("new-api client 未配置 BaseURL")
	}
	body := map[string]any{
		"name":            input.Name,
		"models":          input.Models,
		"remain_quota":    input.Quota,
		"unlimited_quota": input.UnlimitedQ,
		"group":           input.Group,
		"expired_time":    -1,
		"status":          1,
	}
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    APIKey `json:"data"`
	}
	if err := u.do(ctx, http.MethodPost, "/api/token/", body, &response); err != nil {
		return APIKey{}, err
	}
	if !response.Success {
		return APIKey{}, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	resolved := response.Data
	if resolved.ID == 0 {
		fallback, err := u.findTokenByName(ctx, input.Name)
		if err != nil {
			return APIKey{}, fmt.Errorf("%w: 创建 token 成功但无法回查 id: %v", ErrPayloadInvalid, err)
		}
		resolved = fallback
	}
	return resolved, nil
}

// findTokenByName 在当前 user 的 token 列表里按 name 精确匹配并返回。
// 用于补 POST /api/token/ 不返回 id 的缺口。
func (u *UserScopedClient) findTokenByName(ctx context.Context, name string) (APIKey, error) {
	var listResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Items   []APIKey `json:"items"`
			Records []APIKey `json:"records"`
		} `json:"data"`
	}
	if err := u.do(ctx, http.MethodGet, "/api/token/?p=1&size=100", nil, &listResp); err != nil {
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
			return k, nil
		}
	}
	return APIKey{}, fmt.Errorf("token name=%q not found in list", name)
}

// GetTokenFullKey 以 user 身份调 POST /api/token/:id/key 取该 token 的完整 sk- 字符串。
//
// new-api 把"完整 key"当成敏感操作，专门给了独立端点（CriticalRateLimit + DisableCache 中间件），
// list / GET 单条都只返回 truncated 18 字符前缀。manager 创建 token 后调一次本方法把 sk-
// 加密落 apps.newapi_key_ciphertext 并注入容器 OPENAI_API_KEY；不要把 sk- 写入持久日志。
func (u *UserScopedClient) GetTokenFullKey(ctx context.Context, tokenID int64) (string, error) {
	if u.base.BaseURL == "" {
		return "", fmt.Errorf("new-api client 未配置 BaseURL")
	}
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	path := fmt.Sprintf("/api/token/%d/key", tokenID)
	if err := u.do(ctx, http.MethodPost, path, nil, &response); err != nil {
		return "", err
	}
	if !response.Success {
		if strings.Contains(strings.ToLower(response.Message), "not found") {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	if response.Data.Key == "" {
		return "", fmt.Errorf("%w: GET token key 返回空字符串", ErrPayloadInvalid)
	}
	return response.Data.Key, nil
}

// SetAPIKeyStatus 以 user 身份启用 / 禁用 token。status: 1 启用、2 禁用。
func (u *UserScopedClient) SetAPIKeyStatus(ctx context.Context, id int64, status int) error {
	body := map[string]any{"id": id, "status": status}
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := u.do(ctx, http.MethodPut, "/api/token/?status_only=true", body, &response); err != nil {
		return err
	}
	if !response.Success {
		return fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	return nil
}

// RechargeUser 给指定 new-api 用户增加点数（admin POST /api/user/manage action=add_quota）。
//
// new-api 的 ManageUser action=add_quota,mode=add 是服务端原子加，比之前的「GET 整 user → 改 quota →
// PUT 整 user」干净得多：
//   - 服务端原子操作，避免并发覆盖；
//   - 自带审计（new-api 写 LogTypeManage）；
//   - 调用方只传 user_id + amount。
//
// 返回值的 RemainQuota 是 add 之后的最新余额，本 client 紧跟一次 GET /api/user/{id} 拿到。
// new-api 自身没有 ref_id 概念，本函数生成 manager 端对账 ID 写入 RechargeResult.RefID。
func (c *Client) RechargeUser(ctx context.Context, input RechargeInput) (RechargeResult, error) {
	if input.CreditAmount <= 0 {
		return RechargeResult{}, fmt.Errorf("credit_amount 必须为正")
	}
	if input.NewAPIUserID == 0 {
		return RechargeResult{}, fmt.Errorf("newapi_user_id 不能为 0")
	}

	body := map[string]any{
		"id":     input.NewAPIUserID,
		"action": "add_quota",
		"mode":   "add",
		"value":  input.CreditAmount,
	}
	var manageResp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/user/manage", body, &manageResp); err != nil {
		return RechargeResult{}, err
	}
	if !manageResp.Success {
		return RechargeResult{}, fmt.Errorf("%w: %s", ErrUpstream, manageResp.Message)
	}

	// 紧跟一次 GET 把"加完后的 quota"拿出来；GET 失败不影响充值已落地的事实，回退到 0。
	balance, err := c.GetUserBalance(ctx, input.NewAPIUserID)
	remain := int64(0)
	if err == nil {
		remain = balance.RemainQuota
	}
	refID := fmt.Sprintf("manager-%d", input.NewAPIUserID)
	return RechargeResult{RefID: refID, RemainQuota: remain}, nil
}

// DeleteUser 调 admin DELETE /api/user/:id 删除业务 user。
//
// OOS-1 孤儿清理用：CreateOrganization 任一步失败时 best-effort 调一次本方法。
// 失败映射沿用 do() 的统一映射（404 → ErrNotFound），调用方按需吞掉错误。
//
// new-api v1.0.0-alpha.1 该路由要求 AdminAuth；删除自身或其他 admin 会被拒。
// 业务 user 必须先解绑 token / 子账号关系，由 new-api 自身约束保证。
func (c *Client) DeleteUser(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return fmt.Errorf("DeleteUser: userID 必须 > 0")
	}
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/user/%d", userID), nil, nil)
}

// GetUserBalance 查询单个 new-api 用户的余额。
func (c *Client) GetUserBalance(ctx context.Context, newapiUserID int64) (BalanceResult, error) {
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Quota     int64 `json:"quota"`
			UsedQuota int64 `json:"used_quota"`
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
		RemainQuota:  response.Data.Quota,
		UsedQuota:    response.Data.UsedQuota,
	}, nil
}

// GetTokenLogs 调 admin GET /api/log/ 拉取调用日志。
//
// 用途：app / member 维度的用量明细查询。manager 不再缓存，直接透传 new-api。
func (c *Client) GetTokenLogs(ctx context.Context, q LogsQuery) (LogsPage, error) {
	page := q.Page
	if page <= 0 {
		page = 1
	}
	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	values := url.Values{}
	values.Set("p", strconv.Itoa(page))
	values.Set("page_size", strconv.Itoa(pageSize))
	if q.TokenID > 0 {
		values.Set("token_id", strconv.FormatInt(q.TokenID, 10))
	}
	if q.UserID > 0 {
		values.Set("user_id", strconv.FormatInt(q.UserID, 10))
	}
	if q.Username != "" {
		values.Set("username", q.Username)
	}
	if q.ModelName != "" {
		values.Set("model_name", q.ModelName)
	}
	if q.Since > 0 {
		values.Set("start_timestamp", strconv.FormatInt(q.Since, 10))
	}
	if q.Until > 0 {
		values.Set("end_timestamp", strconv.FormatInt(q.Until, 10))
	}
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Items   []LogEntry `json:"items"`
			Records []LogEntry `json:"records"`
			Total   int        `json:"total"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/log/?"+values.Encode(), nil, &response); err != nil {
		return LogsPage{}, err
	}
	if !response.Success {
		return LogsPage{}, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	items := response.Data.Items
	if len(items) == 0 {
		items = response.Data.Records
	}
	return LogsPage{Items: items, Total: response.Data.Total}, nil
}

// GetUserQuotaDates 调 admin GET /api/data/users 拿指定 user 在时间窗内的按天 quota 汇总。
func (c *Client) GetUserQuotaDates(ctx context.Context, userID, since, until int64) ([]QuotaDate, error) {
	if userID == 0 {
		return nil, fmt.Errorf("GetUserQuotaDates: userID 不能为 0")
	}
	values := url.Values{}
	values.Set("id", strconv.FormatInt(userID, 10))
	if since > 0 {
		values.Set("start_timestamp", strconv.FormatInt(since, 10))
	}
	if until > 0 {
		values.Set("end_timestamp", strconv.FormatInt(until, 10))
	}
	return c.fetchQuotaDates(ctx, "/api/data/users?"+values.Encode())
}

// GetAllQuotaDates 调 admin GET /api/data/ 拿全平台时间窗内的按天 quota 汇总。
func (c *Client) GetAllQuotaDates(ctx context.Context, since, until int64) ([]QuotaDate, error) {
	values := url.Values{}
	if since > 0 {
		values.Set("start_timestamp", strconv.FormatInt(since, 10))
	}
	if until > 0 {
		values.Set("end_timestamp", strconv.FormatInt(until, 10))
	}
	suffix := ""
	if encoded := values.Encode(); encoded != "" {
		suffix = "?" + encoded
	}
	return c.fetchQuotaDates(ctx, "/api/data/"+suffix)
}

// fetchQuotaDates 把 /api/data/ 与 /api/data/users 的响应统一解析为 []QuotaDate。
// new-api 偶有把数组直接放到 data 字段、有时包在 data.items 里的两种写法，这里两种都吃。
func (c *Client) fetchQuotaDates(ctx context.Context, path string) ([]QuotaDate, error) {
	var response struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	if len(response.Data) == 0 || string(response.Data) == "null" {
		return []QuotaDate{}, nil
	}
	// 先按数组试一次
	var direct []QuotaDate
	if err := json.Unmarshal(response.Data, &direct); err == nil {
		return direct, nil
	}
	// 退化到对象包裹
	var wrapped struct {
		Items   []QuotaDate `json:"items"`
		Records []QuotaDate `json:"records"`
	}
	if err := json.Unmarshal(response.Data, &wrapped); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrPayloadInvalid, err.Error())
	}
	if len(wrapped.Items) > 0 {
		return wrapped.Items, nil
	}
	return wrapped.Records, nil
}

// newRequest 把 method / path / body 组装成 *http.Request，但不写鉴权头。
// 调用方负责设置 Authorization / New-Api-User 或附带 cookie jar。
func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	// path 可能含 query string；url.JoinPath 会把 "?" 转义为 "%3F"，必须分开拼。
	rawPath, query, _ := strings.Cut(path, "?")
	endpoint, err := url.JoinPath(c.BaseURL, rawPath)
	if err != nil {
		return nil, err
	}
	if query != "" {
		endpoint += "?" + query
	}
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求失败: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// do 走 admin 鉴权头执行请求并解析响应到 target。
func (c *Client) do(ctx context.Context, method, path string, body any, target any) error {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if c.AdminToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AdminToken)
	}
	// new-api admin API 要求 New-Api-User header 标识当前调用者 user_id，
	// 且必须与 access_token 所属用户匹配；缺失时 new-api 返回
	// "Unauthorized, New-Api-User header not provided"。
	if c.AdminUserID > 0 {
		req.Header.Set("New-Api-User", strconv.FormatInt(c.AdminUserID, 10))
	}
	return executeRequest(c.httpClient(), req, target)
}

// do 走业务 user 的 access_token 鉴权头执行请求。
//
// OOS-2 自愈：收到 ErrUnauthorized 且绑了 refresher 时，调一次 refresher 拿新 token
// 并重试一次；第二次仍 401 直接 propagate，不再循环。
func (u *UserScopedClient) do(ctx context.Context, method, path string, body any, target any) error {
	err := u.doOnce(ctx, method, path, body, target)
	if !errors.Is(err, ErrUnauthorized) || u.refresher == nil {
		return err
	}
	// 401 + refresher 非 nil → 自愈一次
	newToken, refreshErr := u.refresher.RefreshAccessToken(ctx)
	if refreshErr != nil {
		// refresh 失败，回退到原 401 错误
		return ErrUnauthorized
	}
	u.accessToken = newToken
	// 重试一次；第二次仍 401 直接 propagate（不再调 refresher）
	return u.doOnce(ctx, method, path, body, target)
}

// doOnce 是无重试的单次请求，供 do() 复用。
func (u *UserScopedClient) doOnce(ctx context.Context, method, path string, body any, target any) error {
	req, err := u.base.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if u.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.accessToken)
	}
	if u.userID > 0 {
		req.Header.Set("New-Api-User", strconv.FormatInt(u.userID, 10))
	}
	return executeRequest(u.base.httpClient(), req, target)
}

// executeRequest 发起 HTTP 请求并按状态码做 sentinel error 映射；2xx 时反序列化 target。
//
// httpClient 显式传入是为了支持 BootstrapUserAccessToken 这种带 cookie jar 的临时 client。
func executeRequest(httpClient *http.Client, req *http.Request, target any) error {
	resp, err := httpClient.Do(req)
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
