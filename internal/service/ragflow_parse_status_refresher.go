package service

import (
	"context"
	"fmt"

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
}

// RagflowParseStatusRefreshClient 是任务所需的 RAGFlow 子集；
// 限制为只读 ListDocuments 即可，避免无意中通过 refresher 触发写入。
type RagflowParseStatusRefreshClient interface {
	ListDocuments(ctx context.Context, datasetID string, page, pageSize int32, keywords, run string) ([]ragflow.Document, int32, error)
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
}

// NewRagflowParseStatusRefresher 创建后台轮询任务实例。
func NewRagflowParseStatusRefresher(store RagflowParseStatusRefresherStore, client RagflowParseStatusRefreshClient) *RagflowParseStatusRefresher {
	return &RagflowParseStatusRefresher{
		store:     store,
		ragflow:   client,
		batchSize: ragflowRefresherDefaultBatchSize,
		pageSize:  ragflowRefresherDefaultPageSize,
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

// Tick 执行单轮刷新；由 PeriodicReconciler 驱动调度。
// 返回的错误仅用于 reconciler 日志输出；任何单一 dataset 的失败不会阻断其他 dataset。
func (r *RagflowParseStatusRefresher) Tick(ctx context.Context) error {
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
	_ = r.store.UpdateRAGFlowDocumentParseStatus(ctx, sqlc.UpdateRAGFlowDocumentParseStatusParams{
		ID:          row.ID,
		ParseStatus: status,
		Progress:    progress,
		LastError:   null.String{},
	})
}
