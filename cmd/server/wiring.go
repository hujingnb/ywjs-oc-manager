// 装配 manager 进程所需的所有跨包依赖。
// 这里只放"接口适配 + 多节点路由"等胶水逻辑；具体业务规则由 service 层负责。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	null "github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/clawhub"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/service"
	"oc-manager/internal/store/sqlc"
	workerhandlers "oc-manager/internal/worker/handlers"
)


// ocopsEndpointResolver 把 service.OcOpsResolver 适配为 workerhandlers.ChannelEndpointResolver：
// 仅取出 OcOpsAppLocation.Endpoint，让 channel_start_login worker 不直接依赖 service 类型。
type ocopsEndpointResolver struct {
	resolver service.OcOpsResolver
}

// ResolveEndpoint 解析 appID 对应的 oc-ops 调用坐标。
func (r ocopsEndpointResolver) ResolveEndpoint(ctx context.Context, appID string) (ocops.Endpoint, error) {
	loc, err := r.resolver.Resolve(ctx, appID)
	if err != nil {
		return ocops.Endpoint{}, err
	}
	return loc.Endpoint, nil
}

// ocopsBindingLocationResolver 把 service.OcOpsResolver 适配为 channel.OcOpsLocationResolver：
// 仅取出 OcOpsAppLocation.Endpoint 和 Supported，隔离 channel 包对 service 包的直接依赖
// （channel→service 会形成循环依赖，因此在 main 包做适配）。
type ocopsBindingLocationResolver struct {
	inner service.OcOpsResolver
}

// Resolve 实现 channel.OcOpsLocationResolver，把 OcOpsAppLocation 拆成 Endpoint+Supported 返回。
func (a ocopsBindingLocationResolver) Resolve(ctx context.Context, appID string) (ocops.Endpoint, bool, error) {
	loc, err := a.inner.Resolve(ctx, appID)
	if err != nil {
		return ocops.Endpoint{}, false, err
	}
	return loc.Endpoint, loc.Supported, nil
}

// appInputRefresherQueries 是 appInputRefresher 需要的最小 DB 查询子集。
// 抽接口便于单测注入内存桩, 不必引入完整 *sqlc.Queries 依赖。
// k8s 下 pod 配置由 bootstrap 在启动时交付，restart 不再向节点写 manifest，
// 因此只保留 GetAssistantVersion 供版本镜像解析使用。
type appInputRefresherQueries interface {
	GetAssistantVersion(ctx context.Context, id string) (sqlc.AssistantVersion, error)
}

// appInputRefresher 实现 workerhandlers.AppInputRefresher：k8s 下 pod 配置由 bootstrap 在
// 启动时交付，restart 不再向节点写 manifest；这里只解析「当前绑定版本的镜像 ref 与 revision」，
// 供 restart handler 做镜像变更检测与记录 applied 版本。
type appInputRefresher struct {
	// queries 用于 GetAssistantVersion，取绑定版本的 image_id 与 revision。
	queries appInputRefresherQueries
	// resolveImage 把版本 image_id 解析为完整 imageRef（含 tag）。
	// nil 时 RefreshAppInput 直接报错，无法确定运行时镜像 ref。
	resolveImage func(imageID string) (string, bool)
}

// newAppInputRefresher 构造生产装配用的 refresher。
// 只依赖 queries 与 resolveImage 两个依赖，其余 uploader/cipher/skillBlobs/opts 已全部移除。
func newAppInputRefresher(queries appInputRefresherQueries, resolveImage func(string) (string, bool)) *appInputRefresher {
	return &appInputRefresher{queries: queries, resolveImage: resolveImage}
}

// RefreshAppInput 只解析当前绑定版本的镜像 ref 与 revision（不再写节点 manifest）。
// nodeID 参数保留以匹配 workerhandlers.AppInputRefresher 接口，但 k8s 下忽略。
func (r *appInputRefresher) RefreshAppInput(ctx context.Context, _ string, app sqlc.App) (workerhandlers.AppInputRefreshResult, error) {
	if r.queries == nil || r.resolveImage == nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("appInputRefresher 依赖未注入")
	}
	if !app.VersionID.Valid {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("应用未绑定助手版本, 无法解析镜像")
	}
	version, err := r.queries.GetAssistantVersion(ctx, app.VersionID.String)
	if err != nil {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("加载助手版本失败: %w", err)
	}
	imageRef, ok := r.resolveImage(version.ImageID)
	if !ok {
		return workerhandlers.AppInputRefreshResult{}, fmt.Errorf("版本镜像 %s 未在配置中", version.ImageID)
	}
	return workerhandlers.AppInputRefreshResult{VersionRevision: version.Revision, ImageRef: imageRef}, nil
}

// orgCredentialsRefresher 是 newapi.CredentialsRefresher 的实现。
//
// 一个 refresher 实例绑定单个组织 + cipher + base client。RefreshAccessToken：
//  1. SELECT ... FOR UPDATE 锁住该组织行；
//  2. 解密密文取 password；
//  3. 调 BootstrapUserAccessToken 拿新 access_token；
//  4. 加密 {username, password, new_access_token} → UpdateOrganizationCredentialsCiphertext；
//  5. 返回新 access_token。
//
// 第一版没有事务包装：FOR UPDATE 在隐式自动提交场景下退化为普通 SELECT。
type orgCredentialsRefresher struct {
	// store 用于读取/写回组织凭据密文。
	store *sqlc.Queries
	// cipher 用于解密旧凭据和加密新 access_token。
	cipher *auth.Cipher
	// client 是 admin/base 视角 new-api client，用于重新换取组织用户 access_token。
	client *newapi.Client
	// orgID 标识当前刷新器绑定的组织（string UUID）。
	orgID string
	// username/password 是组织在 new-api 中的登录凭据，只保留在内存中用于刷新 access_token。
	username string
	password string
}

// RefreshAccessToken 刷新组织在 new-api 中的 access_token 并写回密文凭据。
func (r *orgCredentialsRefresher) RefreshAccessToken(ctx context.Context) (string, error) {
	// GetOrganizationForUpdate 现在接收 string。
	org, err := r.store.GetOrganizationForUpdate(ctx, r.orgID)
	if err != nil {
		return "", fmt.Errorf("RefreshAccessToken 锁组织失败: %w", err)
	}
	newToken, err := r.client.BootstrapUserAccessToken(ctx, r.username, r.password)
	if err != nil {
		return "", fmt.Errorf("RefreshAccessToken 重新登录失败: %w", err)
	}
	payload, err := json.Marshal(service.OrganizationCredentials{
		Username:    r.username,
		Password:    r.password,
		AccessToken: newToken,
	})
	if err != nil {
		return "", err
	}
	ciphertext, err := r.cipher.Encrypt(payload)
	if err != nil {
		return "", err
	}
	if err := r.store.UpdateOrganizationCredentialsCiphertext(ctx, sqlc.UpdateOrganizationCredentialsCiphertextParams{
		ID:                              org.ID,
		NewapiUserCredentialsCiphertext: null.StringFrom(ciphertext),
	}); err != nil {
		return "", fmt.Errorf("RefreshAccessToken 写回密文失败: %w", err)
	}
	return newToken, nil
}

// orgScopedClientFactory 把 sqlc 组织行 + manager cipher + newapi.Client 组合成
// handlers.NewAPIClientFactory：worker handler 在跑 job 时只需要把 sqlc.App 给到
// UserScopedFor，由 factory 反查组织凭据 → 解密 → 构造 user-scoped client，避免
// 每个 handler 都重复实现"读 organizations + 解 ciphertext"的样板。
type orgScopedClientFactory struct {
	client *newapi.Client
	store  *sqlc.Queries
	cipher *auth.Cipher
}

// UserScopedFor 解密组织凭据并返回以业务 user 身份调 token 操作的 client view。
//
// 调用前置条件：
//   - app.OrgID 必须已经存在；
//   - 该组织必须已经走过 OrganizationService.CreateOrganization 把 newapi_user_id
//     与 newapi_user_credentials_ciphertext 写齐；缺任意一项视作"未 provision"，立即报错。
func (f *orgScopedClientFactory) UserScopedFor(ctx context.Context, app sqlc.App) (workerhandlers.APIKeyClient, error) {
	if f.client == nil {
		return nil, fmt.Errorf("orgScopedClientFactory: newapi client 未配置")
	}
	if f.cipher == nil {
		return nil, fmt.Errorf("orgScopedClientFactory: cipher 未配置")
	}
	// app.OrgID 现在是 string，直接传入。
	org, err := f.store.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return nil, fmt.Errorf("查询组织失败: %w", err)
	}
	creds, err := service.DecryptOrganizationCredentials(org, f.cipher)
	if err != nil {
		return nil, err
	}
	if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
		return nil, fmt.Errorf("组织 %s 未持有 new-api 用户 id", org.ID)
	}
	userID, err := parseInt64ForWiring(org.NewapiUserID.String)
	if err != nil {
		return nil, fmt.Errorf("解析 newapi_user_id 失败: %w", err)
	}
	refresher := &orgCredentialsRefresher{
		store:    f.store,
		cipher:   f.cipher,
		client:   f.client,
		orgID:    org.ID,
		username: creds.Username,
		password: creds.Password,
	}
	return f.client.AsUserWithRefresh(userID, creds.AccessToken, refresher), nil
}

// parseInt64ForWiring 是 cmd/server 内部的小工具：把 string 解为 int64，error 直传。
// service 包里有同语义函数，但 wiring 层不便引入服务包内部 helper，复制一份避免循环依赖。
func parseInt64ForWiring(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// clawhubVersionListerAdapter 把 *clawhub.ClawHubClient 适配为 service.ClawHubVersionLister。
//
// 问题背景：clawhub.ClawHubClient.ListVersions 返回 []clawhub.SkillVersion，而
// service.ClawHubVersionLister 接口要求 []service.SkillVersion；两者结构相同（均只含 Version string 字段），
// 但 Go 类型系统要求类型完全匹配，*clawhub.ClawHubClient 无法直接满足 service.ClawHubVersionLister。
// 此适配器在 main 包完成转换，避免 integrations/clawhub 包直接依赖 internal/service 包造成循环依赖。
type clawhubVersionListerAdapter struct {
	// client 是真实的 ClawHub HTTP 客户端，BaseURL 非空时由 main 构造并传入。
	client *clawhub.ClawHubClient
}

// ListVersions 实现 service.ClawHubVersionLister：调用 ClawHub 客户端获取版本列表，
// 并把 []clawhub.SkillVersion 逐项转换为 []service.SkillVersion（字段一一对应）。
func (a clawhubVersionListerAdapter) ListVersions(ctx context.Context, slug string) ([]service.SkillVersion, error) {
	vers, err := a.client.ListVersions(ctx, slug)
	if err != nil {
		return nil, err
	}
	// 逐项把 clawhub.SkillVersion 转换为 service.SkillVersion（均只含 Version string 字段）。
	out := make([]service.SkillVersion, len(vers))
	for i, v := range vers {
		out[i] = service.SkillVersion{Version: v.Version}
	}
	return out, nil
}
