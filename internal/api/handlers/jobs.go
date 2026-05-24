package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// JobsHandler 对外暴露 job 状态查询接口。
// 鉴权按 job 关联应用的可见性走：平台管理员跨组织放行；组织管理员可查本组织 app 的 job；
// 组织成员可查自己拥有的 app 的 job；payload 无 app_id 的 job 仅平台管理员可查。
type JobsHandler struct {
	store JobsStore
}

// JobsStore 是 job handler 依赖的最小存储接口。
// 暴露给 router 装配，使 cmd/server 不需要直接耦合 sqlc.Queries 类型。
// 鉴权需要按 job.payload.app_id 反查 app 的可见性，因此除 GetJob 外还需要 GetApp。
type JobsStore interface {
	GetJob(ctx context.Context, id pgtype.UUID) (sqlc.Job, error)
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
}

// NewJobsHandler 创建 jobs handler。
func NewJobsHandler(store JobsStore) *JobsHandler {
	return &JobsHandler{store: store}
}

// RegisterJobsRoutes 注册 job 路由。
func RegisterJobsRoutes(router gin.IRouter, handler *JobsHandler) {
	group := router.Group("/api/v1/jobs")
	group.GET("/:jobId", handler.Get)
}

// JobView 是对外的 job 视图，剥离锁字段等内部信息。
type JobView struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	Attempts    int32      `json:"attempts"`
	MaxAttempts int32      `json:"max_attempts"`
	LastError   string     `json:"last_error,omitempty"`
	RunAfter    *time.Time `json:"run_after,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// jobPayloadAppRef 是 job.payload_json 里 handler 鉴权用到的最小字段集。
// 仅取 app_id；其余 payload 字段由各 job 类型自行使用，handler 不解释。
type jobPayloadAppRef struct {
	AppID string `json:"app_id"`
}

// Get 查询 job 详情。
//
// @Summary      查询异步任务详情
// @Description  按 job 关联应用的可见性鉴权：平台管理员跨组织放行；组织管理员可查本组织 app 的 job；组织成员可查自己拥有的 app 的 job。payload 无 app_id 的 job 仅平台管理员可查。
// @Tags         jobs
// @Produce      json
// @Security     BearerAuth
// @Param        jobId  path      string  true  "job UUID"
// @Success      200    {object}  map[string]JobView
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /jobs/{jobId} [get]
func (h *JobsHandler) Get(c *gin.Context) {
	principal := principalFromCtx(c)
	var id pgtype.UUID
	if err := id.Scan(c.Param("jobId")); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "非法 job id"))
		return
	}
	job, err := h.store.GetJob(c.Request.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "job 不存在"))
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "查询 job 失败"))
		return
	}

	// 平台管理员跨组织放行——保留原行为；其他角色按 job 关联的 app 资源鉴权。
	if principal.Role != domain.UserRolePlatformAdmin {
		// 解 payload 取 app_id；payload 不含 app_id 的 job（目前不存在此类）保守拒绝。
		var ref jobPayloadAppRef
		if uerr := json.Unmarshal(job.PayloadJson, &ref); uerr != nil || ref.AppID == "" {
			c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权查看 job"))
			return
		}
		var appID pgtype.UUID
		if perr := appID.Scan(ref.AppID); perr != nil {
			// payload 里的 app_id 不合法是脏数据；按未找到处理避免泄漏 payload 结构细节。
			c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "job 关联应用不存在"))
			return
		}
		app, aerr := h.store.GetApp(c.Request.Context(), appID)
		if errors.Is(aerr, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "job 关联应用不存在"))
			return
		}
		if aerr != nil {
			c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "查询 job 关联应用失败"))
			return
		}
		if !auth.CanViewApp(principal, formatUUID(app.OrgID), formatUUID(app.OwnerUserID)) {
			c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权查看 job"))
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"job": toJobView(job)})
}

func toJobView(job sqlc.Job) JobView {
	view := JobView{
		ID:          formatUUID(job.ID),
		Type:        job.Type,
		Status:      job.Status,
		Attempts:    job.Attempts,
		MaxAttempts: job.MaxAttempts,
	}
	if job.LastError.Valid {
		view.LastError = job.LastError.String
	}
	if job.RunAfter.Valid {
		t := job.RunAfter.Time
		view.RunAfter = &t
	}
	if job.CreatedAt.Valid {
		t := job.CreatedAt.Time
		view.CreatedAt = &t
	}
	if job.FinishedAt.Valid {
		t := job.FinishedAt.Time
		view.FinishedAt = &t
	}
	return view
}

// formatUUID 把 pgtype.UUID 转成 8-4-4-4-12 横线分组的小写 hex 字符串。
// 对任意 pgtype.UUID 通用，handler 包内 job/app/org 等 UUID 输出统一走这里。
func formatUUID(value pgtype.UUID) string {
	bytes := value.Bytes
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, b := range bytes[:] {
		out = append(out, digits[b>>4], digits[b&0x0f])
		if i == 3 || i == 5 || i == 7 || i == 9 {
			out = append(out, '-')
		}
	}
	return string(out)
}
