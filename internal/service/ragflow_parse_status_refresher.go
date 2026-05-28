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
	// ragflowRefresherDefaultPageSize 是单次向 RAGFlow 拉取每个 dataset 文档列表的页大小；
	// 设为 200 与 batchSize 配合可在最坏情况下一次性覆盖整组待刷新文档。
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
		remoteDocs, _, listErr := r.ragflow.ListDocuments(ctx, datasetID, 1, r.pageSize, "", "")
		if listErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("拉取 dataset %s 文档状态失败: %w", datasetID, listErr)
			}
			r.markGroupListFailure(ctx, group, listErr)
			continue
		}
		remoteByID := make(map[string]ragflow.Document, len(remoteDocs))
		for _, rd := range remoteDocs {
			remoteByID[rd.ID] = rd
		}
		for _, row := range group {
			r.applyRemoteStatus(ctx, row, remoteByID)
		}
	}
	return firstErr
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
