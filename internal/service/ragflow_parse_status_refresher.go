package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/integrations/ragflow"
	"oc-manager/internal/store/sqlc"
)

// RagflowParseStatusRefresherStore 描述后台轮询任务所需的数据访问能力。
// 单独定义而非复用 KnowledgeStore，是为了让任务依赖面更小、测试替身更易实现。
type RagflowParseStatusRefresherStore interface {
	// ListRAGFlowDocumentsNeedingRefresh 找出 queued / running 状态、且远端 dataset 已创建的文档。
	ListRAGFlowDocumentsNeedingRefresh(ctx context.Context, limit int32) ([]sqlc.ListRAGFlowDocumentsNeedingRefreshRow, error)
	// UpdateRAGFlowDocumentParseStatus 回写最新的 parse_status / progress / last_error（:exec）。
	UpdateRAGFlowDocumentParseStatus(ctx context.Context, arg sqlc.UpdateRAGFlowDocumentParseStatusParams) error
	// ListRAGFlowDocumentsDueForAutoReparse 找出已到冷却时间、可自动重解析的 failed 文档。
	ListRAGFlowDocumentsDueForAutoReparse(ctx context.Context, limit int32) ([]sqlc.ListRAGFlowDocumentsDueForAutoReparseRow, error)
	// MarkRAGFlowDocumentFailedWithAutoReparse 写入失败状态和下一次自动重试时间。
	MarkRAGFlowDocumentFailedWithAutoReparse(ctx context.Context, arg sqlc.MarkRAGFlowDocumentFailedWithAutoReparseParams) error
	// MarkRAGFlowDocumentAutoReparseQueued 在自动重解析提交成功后把文档重新置为 queued。
	MarkRAGFlowDocumentAutoReparseQueued(ctx context.Context, id string) error
}

// RagflowParseStatusRefreshClient 是后台任务所需的 RAGFlow 操作子集：
// ListDocuments 查询解析状态，ParseDocuments 触发到期失败文档的自动重解析；
// 仅暴露后台任务必需的最小操作面，不引入其它写操作。
type RagflowParseStatusRefreshClient interface {
	ListDocuments(ctx context.Context, datasetID string, page, pageSize int32, keywords, run string) ([]ragflow.Document, int32, error)
	// ParseDocuments 对指定远端 dataset 下的 document 触发 RAGFlow 重新解析。
	ParseDocuments(ctx context.Context, datasetID string, documentIDs []string) error
}

const (
	// ragflowRefresherDefaultBatchSize 是单次扫描的本地文档上限；
	// 选 100 是为了让单轮 tick 总开销可控，远未到 RAGFlow ListDocuments 的单页上限。
	ragflowRefresherDefaultBatchSize int32 = 100
	// ragflowRefresherDefaultPageSize 是向 RAGFlow 翻页拉取每个 dataset 文档列表的单页大小；
	// refresher 会按此页大小翻页直到取齐全量（见 listAllRemoteDocuments），因此 dataset
	// 文档数即便超过单页也能完整覆盖，不会因为只看第一页而漏页误判为「远端已删除」。
	ragflowRefresherDefaultPageSize int32 = 200
)

// RagflowParseStatusRefresher 周期扫描 queued / running 文档并把最新解析状态回写本地。
// 设计取舍：列表请求不再同步访问 RAGFlow，全部状态推进交由此后台任务，确保无人查看列表时状态也能收敛。
type RagflowParseStatusRefresher struct {
	store     RagflowParseStatusRefresherStore
	ragflow   RagflowParseStatusRefreshClient
	batchSize int32
	pageSize  int32
	// now 注入时间源便于测试控制冷却时间计算。
	now func() time.Time
}

// NewRagflowParseStatusRefresher 创建后台轮询任务实例。
func NewRagflowParseStatusRefresher(store RagflowParseStatusRefresherStore, client RagflowParseStatusRefreshClient) *RagflowParseStatusRefresher {
	return &RagflowParseStatusRefresher{
		store:     store,
		ragflow:   client,
		batchSize: ragflowRefresherDefaultBatchSize,
		pageSize:  ragflowRefresherDefaultPageSize,
		now:       time.Now,
	}
}

// SetBatchSize / SetPageSize 仅供测试或将来运行期调优使用，正常配置可省略。
func (r *RagflowParseStatusRefresher) SetBatchSize(n int32) {
	if n > 0 {
		r.batchSize = n
	}
}

func (r *RagflowParseStatusRefresher) SetPageSize(n int32) {
	if n > 0 {
		r.pageSize = n
	}
}

// SetNowFunc 注入自定义时间源，仅供测试控制冷却时间计算。
func (r *RagflowParseStatusRefresher) SetNowFunc(fn func() time.Time) {
	if fn != nil {
		r.now = fn
	}
}

// Tick 执行单轮刷新，分两个阶段：先回刷 queued/running 文档的最新状态，
// 再对已到冷却时间的模型过载失败文档触发自动重解析。
// 返回的错误仅用于 reconciler 日志输出；任一阶段失败不阻断另一阶段。
func (r *RagflowParseStatusRefresher) Tick(ctx context.Context) error {
	firstErr := r.refreshQueuedAndRunningDocuments(ctx)
	if err := r.autoReparseDueFailedDocuments(ctx); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// refreshQueuedAndRunningDocuments 回刷 queued/running 文档的最新解析状态（原 Tick 主体逻辑）。
func (r *RagflowParseStatusRefresher) refreshQueuedAndRunningDocuments(ctx context.Context) error {
	rows, err := r.store.ListRAGFlowDocumentsNeedingRefresh(ctx, r.batchSize)
	if err != nil {
		return fmt.Errorf("扫描待刷新文档失败: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	// 按远端 dataset 分组，避免对同一 dataset 重复调用 RAGFlow ListDocuments。
	// RemoteDatasetID 是 null.String，取其字符串值用于分组（查询已过滤 NULL）。
	byDataset := make(map[string][]sqlc.ListRAGFlowDocumentsNeedingRefreshRow, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		remoteID := row.RemoteDatasetID.String
		if _, ok := byDataset[remoteID]; !ok {
			order = append(order, remoteID)
		}
		byDataset[remoteID] = append(byDataset[remoteID], row)
	}

	var firstErr error
	for _, datasetID := range order {
		group := byDataset[datasetID]
		remoteByID, listErr := r.listAllRemoteDocuments(ctx, datasetID)
		if listErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("拉取 dataset %s 文档状态失败: %w", datasetID, listErr)
			}
			r.markGroupListFailure(ctx, group, listErr)
			continue
		}
		for _, row := range group {
			r.applyRemoteStatus(ctx, row, remoteByID)
		}
	}
	return firstErr
}

// listAllRemoteDocuments 翻页拉取指定 dataset 在 RAGFlow 的全部文档，建立 id->Document 索引。
//
// 必须枚举全量：此前实现只取第 1 页（page=1, pageSize=200），当某个 dataset 的文档数超过
// 单页上限时，落在后续页的文档不会进入索引，会被 applyRemoteStatus 误判为「远端已删除」而
// 错误标记 failed（线上一次性误杀 1000+ 文档即此原因）。只有拿到该 dataset 的全量列表，
// 「未找到即已删除」的判断才成立。任一页拉取失败直接向上返回，交由调用方保留状态下轮重试。
func (r *RagflowParseStatusRefresher) listAllRemoteDocuments(ctx context.Context, datasetID string) (map[string]ragflow.Document, error) {
	remoteByID := make(map[string]ragflow.Document)
	for page := int32(1); ; page++ {
		docs, total, err := r.ragflow.ListDocuments(ctx, datasetID, page, r.pageSize, "", "")
		if err != nil {
			return nil, err
		}
		for _, rd := range docs {
			remoteByID[rd.ID] = rd
		}
		// 终止条件（任一满足即停，三重保护防止漏页或死循环）：
		//   1. 本页为空：后面再无数据；
		//   2. 本页不足一页：已到最后一页；
		//   3. 已累计到 RAGFlow 报告的 total：全量取齐（兼容后端忽略分页、一次返回全部的情况）。
		if len(docs) == 0 || int32(len(docs)) < r.pageSize || (total > 0 && int32(len(remoteByID)) >= total) {
			break
		}
	}
	return remoteByID, nil
}

// markGroupListFailure 在 dataset 拉取失败时为组内每条文档写入 last_error，
// 但保留原 parse_status / progress，下一轮 tick 会重试。
func (r *RagflowParseStatusRefresher) markGroupListFailure(ctx context.Context, group []sqlc.ListRAGFlowDocumentsNeedingRefreshRow, listErr error) {
	for _, row := range group {
		_ = r.store.UpdateRAGFlowDocumentParseStatus(ctx, sqlc.UpdateRAGFlowDocumentParseStatusParams{
			ID:          row.ID,
			ParseStatus: row.ParseStatus,
			Progress:    row.Progress,
			LastError:   null.StringFrom(listErr.Error()),
		})
	}
}

// applyRemoteStatus 根据远端 ListDocuments 结果回写单条文档；
// 远端缺失视为外部已删除该 document，本地标记 failed 但保留映射用于审计 / 排障。
func (r *RagflowParseStatusRefresher) applyRemoteStatus(ctx context.Context, row sqlc.ListRAGFlowDocumentsNeedingRefreshRow, remoteByID map[string]ragflow.Document) {
	remote, ok := remoteByID[row.RagflowDocumentID]
	if !ok {
		_ = r.store.UpdateRAGFlowDocumentParseStatus(ctx, sqlc.UpdateRAGFlowDocumentParseStatusParams{
			ID:          row.ID,
			ParseStatus: "failed",
			Progress:    row.Progress,
			LastError:   null.StringFrom("RAGFlow 未找到对应 document，可能在远端已被删除"),
		})
		return
	}
	status := normalizeRAGFlowRun(remote.Run)
	progress := progressForStatus(status)
	// 状态无变化时跳过写库，避免无意义的 updated_at 抖动让 ORDER BY updated_at 失效。
	if status == row.ParseStatus && progress == row.Progress {
		return
	}
	// 解析失败时，把 RAGFlow 返回的真实失败原因（progress_msg 尾部错误行，如 embedding 报错）
	// 写入 last_error 供前端在「解析失败」时展示；其它状态清空 last_error，避免历史错误残留。
	lastErr := null.String{}
	if status == "failed" {
		lastErr = null.StringFrom(extractRAGFlowError(remote.ProgressMsg))
		// 模型服务临时过载属于可自动恢复的上游故障：记录失败的同时安排下一次自动重解析，
		// 由 autoReparseDueFailedDocuments 在冷却到期后重新提交，避免无谓占用人工排障。
		if isRAGFlowAutoReparseError(lastErr.String) {
			_ = r.store.MarkRAGFlowDocumentFailedWithAutoReparse(ctx, sqlc.MarkRAGFlowDocumentFailedWithAutoReparseParams{
				Progress:          progress,
				LastError:         lastErr,
				AutoReparseNextAt: autoReparseNextAt(row.AutoReparseAttempts, r.now()),
				ID:                row.ID,
			})
			return
		}
	}
	_ = r.store.UpdateRAGFlowDocumentParseStatus(ctx, sqlc.UpdateRAGFlowDocumentParseStatusParams{
		ID:          row.ID,
		ParseStatus: status,
		Progress:    progress,
		LastError:   lastErr,
	})
}

// ragflowAutoReparseMaxAttempts 是单个文档允许的自动重试次数上限：达到后不再安排自动重试，
// 需人工介入。该上限与 sqlc 查询 MarkRAGFlowDocumentAutoReparseQueued / ListRAGFlowDocumentsDueForAutoReparse
// 中的 `auto_reparse_attempts < 3` 条件保持一致。
const ragflowAutoReparseMaxAttempts int32 = 3

// isRAGFlowAutoReparseError 判断失败原因是否属于「模型服务临时过载」白名单。
// 仅这类临时上游故障适合自动重试；文件损坏 / 不支持等错误不在其列，需人工处理。
func isRAGFlowAutoReparseError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "model service overloaded") ||
		strings.Contains(lower, "error code: 503") ||
		strings.Contains(lower, "code: 50505")
}

// autoReparseNextAt 按已成功提交的自动重试次数计算下一次允许重试的时间，采用递增退避；
// 达到次数上限后返回无效 null.Time 表示不再自动重试。
func autoReparseNextAt(attempts int32, now time.Time) null.Time {
	if attempts >= ragflowAutoReparseMaxAttempts {
		return null.Time{}
	}
	switch attempts {
	case 0:
		// 首次失败立即可重试：next_at = now，可能在同一轮或下一轮 tick 被自动重解析阶段提交。
		return null.TimeFrom(now)
	case 1:
		return null.TimeFrom(now.Add(10 * time.Minute))
	default: // attempts == 2：第三次（最后一次）重试前等待更久
		return null.TimeFrom(now.Add(30 * time.Minute))
	}
}

// autoReparseDueFailedDocuments 扫描已到冷却时间的模型过载失败文档，按远端 dataset 分组后
// 重新提交 RAGFlow 解析；仅在提交成功后累计自动重试次数并清空冷却时间。
// 单个 dataset 提交失败不阻断其它 dataset，错误冒泡给 reconciler 仅作日志。
func (r *RagflowParseStatusRefresher) autoReparseDueFailedDocuments(ctx context.Context) error {
	rows, err := r.store.ListRAGFlowDocumentsDueForAutoReparse(ctx, r.batchSize)
	if err != nil {
		return fmt.Errorf("扫描待自动重解析文档失败: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	// 按远端 dataset 分组，避免对同一 dataset 重复调用 RAGFlow ParseDocuments；保留首次出现顺序。
	type localDoc struct {
		id       string // manager 本地文档 ID
		remoteID string // RAGFlow 远端 document ID
	}
	byDataset := make(map[string][]localDoc, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		remoteDatasetID := row.RemoteDatasetID.String
		if _, ok := byDataset[remoteDatasetID]; !ok {
			order = append(order, remoteDatasetID)
		}
		byDataset[remoteDatasetID] = append(byDataset[remoteDatasetID], localDoc{id: row.ID, remoteID: row.RagflowDocumentID})
	}

	var firstErr error
	for _, datasetID := range order {
		group := byDataset[datasetID]
		remoteIDs := make([]string, 0, len(group))
		for _, doc := range group {
			remoteIDs = append(remoteIDs, doc.remoteID)
		}
		if err := r.ragflow.ParseDocuments(ctx, datasetID, remoteIDs); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("自动重解析 dataset %s 失败: %w", datasetID, err)
			}
			// 提交失败：不累计次数，保留冷却时间，下一轮 tick 再试。
			continue
		}
		for _, doc := range group {
			_ = r.store.MarkRAGFlowDocumentAutoReparseQueued(ctx, doc.id)
		}
	}
	return firstErr
}

// extractRAGFlowError 从 RAGFlow 的 progress_msg（多行进度日志）中提取最有价值的失败原因，
// 用于写入 last_error 在前端「解析失败」时展示。
// 策略：优先取最后一条包含 ERROR 的行；没有则取最后一条非空行；都没有时给通用兜底文案。
// 结果按 rune 截断到上限，避免超长日志撑爆列表单元格。
func extractRAGFlowError(progressMsg string) string {
	const maxLen = 500
	var lastNonEmpty, lastErrLine string
	for _, raw := range strings.Split(progressMsg, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lastNonEmpty = line
		if strings.Contains(strings.ToUpper(line), "ERROR") {
			lastErrLine = line
		}
	}
	msg := lastErrLine
	if msg == "" {
		msg = lastNonEmpty
	}
	if msg == "" {
		return "RAGFlow 解析失败（未返回具体原因）"
	}
	if r := []rune(msg); len(r) > maxLen {
		msg = string(r[:maxLen]) + "…"
	}
	return msg
}
