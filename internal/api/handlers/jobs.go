package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

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
	GetJob(ctx context.Context, id string) (sqlc.Job, error)
	GetApp(ctx context.Context, id string) (sqlc.App, error)
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
// @Description  按 job 关联应用的可见性鉴权：平台管理员跨企业放行；企业管理员可查本企业 app 的 job；企业成员可查自己拥有的 app 的 job。payload 无 app_id 的 job 仅平台管理员可查。
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
	jobID := c.Param("jobId")
	// 旧实现用 pgtype.UUID 解析路径参数，非法格式（含空串）直接 400；迁移到 string 后显式校验保持该行为。
	if _, err := uuid.Parse(jobID); err != nil {
		apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgJobInvalidID)
		return
	}
	job, err := h.store.GetJob(c.Request.Context(), jobID)
	if errors.Is(err, sql.ErrNoRows) {
		apierror.JSON(c, http.StatusNotFound, "NOT_FOUND", apierror.MsgJobNotFound)
		return
	}
	if err != nil {
		apierror.JSON(c, http.StatusInternalServerError, "INTERNAL", apierror.MsgJobQueryFailed)
		return
	}

	// 平台管理员跨组织放行——保留原行为；其他角色按 job 关联的 app 资源鉴权。
	if principal.Role != domain.UserRolePlatformAdmin {
		// 解 payload 取 app_id；payload 不含 app_id 的 job（目前不存在此类）保守拒绝。
		var ref jobPayloadAppRef
		if uerr := json.Unmarshal(job.PayloadJson, &ref); uerr != nil || ref.AppID == "" {
			apierror.JSON(c, http.StatusForbidden, "FORBIDDEN", apierror.MsgJobForbidden)
			return
		}
		// payload.app_id 非合法 UUID：按「关联应用不存在」处理（404），与旧 pgtype.UUID 解析失败路径一致，
		// 避免脏 payload 走到 GetApp 并因鉴权分支暴露 payload 结构细节给探测者。
		if _, perr := uuid.Parse(ref.AppID); perr != nil {
			apierror.JSON(c, http.StatusNotFound, "NOT_FOUND", apierror.MsgJobAppNotFound)
			return
		}
		app, aerr := h.store.GetApp(c.Request.Context(), ref.AppID)
		if errors.Is(aerr, sql.ErrNoRows) {
			apierror.JSON(c, http.StatusNotFound, "NOT_FOUND", apierror.MsgJobAppNotFound)
			return
		}
		if aerr != nil {
			apierror.JSON(c, http.StatusInternalServerError, "INTERNAL", apierror.MsgJobAppQueryFailed)
			return
		}
		if !auth.CanViewApp(principal, app.OrgID, app.OwnerUserID) {
			apierror.JSON(c, http.StatusForbidden, "FORBIDDEN", apierror.MsgJobForbidden)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"job": toJobView(job)})
}

func toJobView(job sqlc.Job) JobView {
	view := JobView{
		// job.ID 现在是 string，直接使用。
		ID:          job.ID,
		Type:        job.Type,
		Status:      job.Status,
		Attempts:    job.Attempts,
		MaxAttempts: job.MaxAttempts,
	}
	if job.LastError.Valid {
		view.LastError = job.LastError.String
	}
	// job.RunAfter 现在是 time.Time（非 nullable），直接赋值。
	t := job.RunAfter
	view.RunAfter = &t
	// job.CreatedAt 现在是 time.Time（非 nullable），直接赋值。
	ct := job.CreatedAt
	view.CreatedAt = &ct
	if job.FinishedAt.Valid {
		ft := job.FinishedAt.Time
		view.FinishedAt = &ft
	}
	return view
}
