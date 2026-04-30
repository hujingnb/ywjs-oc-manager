package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// JobsHandler 对外暴露 job 状态查询接口。
// 当前仅支持平台管理员按 ID 查询；按组织或资源筛选放在后续 task。
type JobsHandler struct {
	store  JobsStore
	tokens *auth.TokenManager
}

// JobsStore 是 job handler 依赖的最小存储接口。
// 暴露给 router 装配，使 cmd/server 不需要直接耦合 sqlc.Queries 类型。
type JobsStore interface {
	GetJob(ctx context.Context, id pgtype.UUID) (sqlc.Job, error)
}

// NewJobsHandler 创建 jobs handler。
func NewJobsHandler(store JobsStore, tokens *auth.TokenManager) *JobsHandler {
	return &JobsHandler{store: store, tokens: tokens}
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

// Get 查询 job 详情。
func (h *JobsHandler) Get(c *gin.Context) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return
	}
	if principal.Role != domain.UserRolePlatformAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权查看 job"})
		return
	}
	var id pgtype.UUID
	if err := id.Scan(c.Param("jobId")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "非法 job id"})
		return
	}
	job, err := h.store.GetJob(c.Request.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "job 不存在"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询 job 失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": toJobView(job)})
}

func toJobView(job sqlc.Job) JobView {
	view := JobView{
		ID:          formatJobUUID(job.ID),
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

func formatJobUUID(value pgtype.UUID) string {
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
