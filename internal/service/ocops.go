// ocops.go —— service 侧消费 oc-ops HTTP 客户端的窄接口、app 坐标解析与错误映射。
//
// 设计要点：
//   - 用三个窄接口（cronOps/kanbanOps/channelOps）替代单个巨型 OcOps 接口，
//     让各 service（Task 19/20/21）只依赖所需方法，单测假实现更小，
//     沿用现有 cronExecer/kanbanExecer 的窄接口 + 假实现风格。
//   - 三个接口的方法签名逐字镜像 *ocops.Client 对应方法（首参 ctx、次参
//     ocops.Endpoint），并由 *ocops.Client 满足（见文件末编译期断言）。
//   - OcOpsResolver 把 appID 解析为 oc-ops 调用坐标；真实 k8s Service DNS
//     寻址与 per-app token 生成/注入是 spec-A，spec-E 仅由 store 最小实现。
//   - mapOcOpsCronErr/mapOcOpsKanbanErr 把 ocops 哨兵错误翻译回 service 既有
//     哨兵错误，保持语义不变。
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/store/sqlc"
)

// cronOps 抽象 oc-ops 的 11 个 cron 方法，供 HermesCronService 注入假实现。
// 方法签名镜像 *ocops.Client；由 *ocops.Client 满足。
type cronOps interface {
	CronCapabilities(ctx context.Context, ep ocops.Endpoint) (ocops.CronCapabilities, error)
	CronStatus(ctx context.Context, ep ocops.Endpoint) (ocops.CronStatus, error)
	CronList(ctx context.Context, ep ocops.Endpoint, all bool) ([]ocops.CronJob, error)
	CronShow(ctx context.Context, ep ocops.Endpoint, id string) (ocops.CronJob, error)
	CronCreate(ctx context.Context, ep ocops.Endpoint, req ocops.CronCreateReq) (ocops.CronJob, error)
	CronUpdate(ctx context.Context, ep ocops.Endpoint, id string, req ocops.CronUpdateReq) (ocops.CronJob, error)
	CronToggle(ctx context.Context, ep ocops.Endpoint, id string, enabled bool) (ocops.CronJob, error)
	CronRun(ctx context.Context, ep ocops.Endpoint, id string) (ocops.CronJob, error)
	CronDelete(ctx context.Context, ep ocops.Endpoint, id string) error
	CronHistory(ctx context.Context, ep ocops.Endpoint, id string) ([]ocops.CronRunEntry, error)
	CronOutput(ctx context.Context, ep ocops.Endpoint, id, file string) (ocops.CronRunOutput, error)
}

// kanbanOps 抽象 oc-ops 的 14 个非流式 kanban 方法 + WatchKanban（SSE），
// 供 HermesKanbanService 注入假实现。方法签名镜像 *ocops.Client；由其满足。
type kanbanOps interface {
	KanbanCapabilities(ctx context.Context, ep ocops.Endpoint) (ocops.KanbanCapabilities, error)
	KanbanBoards(ctx context.Context, ep ocops.Endpoint) ([]ocops.KanbanBoard, error)
	KanbanList(ctx context.Context, ep ocops.Endpoint, board, status, assignee string) ([]ocops.KanbanTask, error)
	KanbanShow(ctx context.Context, ep ocops.Endpoint, board, id string) (ocops.KanbanTaskDetail, error)
	KanbanRuns(ctx context.Context, ep ocops.Endpoint, board, id string) ([]ocops.KanbanTaskRun, error)
	KanbanStats(ctx context.Context, ep ocops.Endpoint, board string) (ocops.KanbanStats, error)
	KanbanCreate(ctx context.Context, ep ocops.Endpoint, req ocops.KanbanCreateReq) (ocops.KanbanTaskDetail, error)
	KanbanComment(ctx context.Context, ep ocops.Endpoint, board, id, body string) (ocops.KanbanTaskDetail, error)
	KanbanComplete(ctx context.Context, ep ocops.Endpoint, board, id, result string) (ocops.KanbanTaskDetail, error)
	KanbanBlock(ctx context.Context, ep ocops.Endpoint, board, id, reason string) (ocops.KanbanTaskDetail, error)
	KanbanUnblock(ctx context.Context, ep ocops.Endpoint, board, id string) (ocops.KanbanTaskDetail, error)
	KanbanArchive(ctx context.Context, ep ocops.Endpoint, board, id string) (ocops.KanbanTaskDetail, error)
	KanbanReassign(ctx context.Context, ep ocops.Endpoint, board, id, to string) (ocops.KanbanTaskDetail, error)
	KanbanReclaim(ctx context.Context, ep ocops.Endpoint, board, id string) (ocops.KanbanTaskDetail, error)
	WatchKanban(ctx context.Context, ep ocops.Endpoint, board string) (<-chan ocops.KanbanEvent, error)
}

// channelOps 抽象 oc-ops 的 info/doctor/channel-status/unbind 与 channel 登录（SSE），
// 供 info/doctor/channel/微信登录相关 service 注入假实现。方法签名镜像 *ocops.Client。
type channelOps interface {
	Info(ctx context.Context, ep ocops.Endpoint) (ocops.Info, error)
	Doctor(ctx context.Context, ep ocops.Endpoint) (ocops.Doctor, error)
	ChannelStatus(ctx context.Context, ep ocops.Endpoint, channel string) (ocops.ChannelStatus, error)
	ChannelUnbind(ctx context.Context, ep ocops.Endpoint, channel string) (ocops.ChannelResult, error)
	ChannelLogin(ctx context.Context, ep ocops.Endpoint, channel string) (<-chan ocops.ChannelLoginEvent, error)
}

// 编译期断言：生产实现 *ocops.Client 必须同时满足三个窄接口；
// 任一方法签名漂移都会在编译期被这里捕获。
var (
	_ cronOps    = (*ocops.Client)(nil)
	_ kanbanOps  = (*ocops.Client)(nil)
	_ channelOps = (*ocops.Client)(nil)
)

// OcOpsAppLocation 是执行 oc-ops 调用所需的全部 app 信息（取代旧 CronAppLocation/KanbanAppLocation）。
type OcOpsAppLocation struct {
	OrgID       string         // 归属组织，用于权限判断
	OwnerUserID string         // 拥有者，用于 org_member 权限判断
	Endpoint    ocops.Endpoint // oc-ops 基址 + per-app token
	Supported   bool           // false 表示 dev stub / 不支持 → UNSUPPORTED
}

// OcOpsResolver 把 appID 解析为 oc-ops 调用坐标。
// 注意：真实 k8s Service DNS 寻址与 per-app token 的生成/存储/注入是 spec-A；
// 本接口在 spec-E 仅由 store 最小实现（见 OcOpsResolverFromStore），单测用假实现。
type OcOpsResolver interface {
	Resolve(ctx context.Context, appID string) (OcOpsAppLocation, error)
}

// ocOpsAppStore 是 OcOpsResolverFromStore 依赖的最小 app 查询能力，
// 与 cronAppStore/kanbanAppStore 一致，仅声明 GetApp 便于单测注入假实现。
type ocOpsAppStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
}

// OcOpsResolverFromStore 从 app store 解析 oc-ops 坐标。
// BaseURL 按约定模板拼装、Token 在 spec-E 留空占位；spec-A 会替换为 client-go
// 真实寻址 + per-app bootstrap token 注入。Supported 由镜像 ref 是否 -dev 判定
// （沿用旧 Stub 语义）。
type OcOpsResolverFromStore struct {
	store      ocOpsAppStore // 复用最小 GetApp 接口
	baseURLTpl string        // 如 "http://app-%s-ocops.oc-apps.svc:8080"（spec-A 调整）
}

// NewOcOpsResolverFromStore 构造从 store 解析坐标的 resolver。
// baseURLTpl 必须含一个 %s 占位符，Resolve 时以 appID 替换。
func NewOcOpsResolverFromStore(store ocOpsAppStore, baseURLTpl string) *OcOpsResolverFromStore {
	return &OcOpsResolverFromStore{store: store, baseURLTpl: baseURLTpl}
}

// Resolve 查询 app 并组装 oc-ops 调用坐标。
// app 不存在（sql.ErrNoRows）映射为 ErrNotFound；其它查询错误包装返回。
// Token 在 spec-E 留空（spec-A 注入 per-app token）。
func (r *OcOpsResolverFromStore) Resolve(ctx context.Context, appID string) (OcOpsAppLocation, error) {
	app, err := r.store.GetApp(ctx, appID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OcOpsAppLocation{}, ErrNotFound
		}
		return OcOpsAppLocation{}, fmt.Errorf("查询 app 失败: %w", err)
	}
	return OcOpsAppLocation{
		OrgID:       app.OrgID,
		OwnerUserID: app.OwnerUserID,
		Endpoint: ocops.Endpoint{
			BaseURL: fmt.Sprintf(r.baseURLTpl, appID),
			// Token: spec-A 注入 per-app token，spec-E 留空占位
		},
		// dev stub 镜像（-dev 后缀）不含真实 hermes，标记为不支持
		Supported: !strings.HasSuffix(app.RuntimeImageRef, "-dev"),
	}, nil
}

// mapOcOpsCronErr 把 ocops 哨兵错误翻译成 cron service 既有哨兵错误，保留语义不变。
// nil 透传 nil；未列举的错误兜底为 ErrCronCLI（与 502/未知上游错误语义一致）。
func mapOcOpsCronErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ocops.ErrBadRequest):
		return ErrCronBadRequest
	case errors.Is(err, ocops.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ocops.ErrUnsupported):
		return ErrCronNotSupported
	case errors.Is(err, ocops.ErrOutputInvalid):
		return ErrCronOutputInvalid
	default:
		return ErrCronCLI
	}
}

// mapOcOpsKanbanErr 把 ocops 哨兵错误翻译成 kanban service 既有哨兵错误，保留语义不变。
// nil 透传 nil；未列举的错误兜底为 ErrKanbanCLI。
func mapOcOpsKanbanErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ocops.ErrBadRequest):
		return ErrKanbanBadRequest
	case errors.Is(err, ocops.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, ocops.ErrUnsupported):
		return ErrKanbanNotSupported
	case errors.Is(err, ocops.ErrOutputInvalid):
		return ErrKanbanOutputInvalid
	default:
		return ErrKanbanCLI
	}
}
