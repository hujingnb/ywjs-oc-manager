// Package handlers 维护 worker 调度时根据 job_type 查找处理函数的注册表。
// 拆出独立包是为了让具体 handler（app_initialize、channel_start_login 等）按业务模块分文件，
// worker 包只依赖通用的注册和派发能力。
package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"oc-manager/internal/store/sqlc"
)

// HandlerFunc 是 worker 调度时统一签名。
// payload 直接来自 jobs.payload_json；handler 自行反序列化为业务结构体。
type HandlerFunc func(ctx context.Context, job sqlc.Job) error

// Registry 是 job_type 到 HandlerFunc 的映射。
type Registry struct {
	handlers      map[string]HandlerFunc
	beforeSuccess map[string]HandlerFunc
}

// NewRegistry 创建空的注册表。
func NewRegistry() *Registry {
	return &Registry{handlers: map[string]HandlerFunc{}, beforeSuccess: map[string]HandlerFunc{}}
}

// RegisterBeforeSuccess 登记成功落库前的可重试回调；失败会保留当前 job 重试，防止后继调度丢失。
func (r *Registry) RegisterBeforeSuccess(jobType string, fn HandlerFunc) error {
	if fn == nil {
		return fmt.Errorf("job 类型 %q 的成功前回调不能为空", jobType)
	}
	if _, exists := r.handlers[jobType]; !exists {
		return fmt.Errorf("job 类型 %q 尚未注册 handler", jobType)
	}
	if _, exists := r.beforeSuccess[jobType]; exists {
		return fmt.Errorf("job 类型 %q 已注册成功前回调", jobType)
	}
	r.beforeSuccess[jobType] = fn
	return nil
}

// LookupBeforeSuccess 返回成功落库前回调；未登记时返回 nil，保持既有 job 行为不变。
func (r *Registry) LookupBeforeSuccess(jobType string) HandlerFunc { return r.beforeSuccess[jobType] }

// Register 注册一个 job_type 的处理函数。
// 重复注册同一类型会返回错误，避免 worker 启动时静默覆盖。
func (r *Registry) Register(jobType string, fn HandlerFunc) error {
	if _, exists := r.handlers[jobType]; exists {
		return fmt.Errorf("job 类型 %q 已注册", jobType)
	}
	r.handlers[jobType] = fn
	return nil
}

// MustRegister 在重复注册时直接 panic，仅用于程序启动期一次性初始化。
func (r *Registry) MustRegister(jobType string, fn HandlerFunc) {
	if err := r.Register(jobType, fn); err != nil {
		panic(err)
	}
}

// Lookup 根据 job_type 取出 handler，未找到返回 ErrHandlerNotFound。
func (r *Registry) Lookup(jobType string) (HandlerFunc, error) {
	fn, ok := r.handlers[jobType]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrHandlerNotFound, jobType)
	}
	return fn, nil
}

// ErrHandlerNotFound 表示当前 worker 未注册该 job_type 的 handler。
// worker 在 dispatch 时遇到该错误会标记 job 失败但不重试。
var ErrHandlerNotFound = errors.New("未注册的 job 类型")

// DeferredJobError 表示任务因业务互斥暂不执行，应无损退回 pending 而非计作一次失败。
type DeferredJobError struct {
	// Delay 是再次允许 scheduler 入队前的短延迟。
	Delay time.Duration
	// Reason 仅用于进程内诊断，不写入 last_error，避免把正常互斥展示成失败。
	Reason string
}

// Error 实现 error；worker 通过 errors.As 识别该控制流错误。
func (e *DeferredJobError) Error() string {
	return fmt.Sprintf("任务延后 %s: %s", e.Delay, e.Reason)
}
