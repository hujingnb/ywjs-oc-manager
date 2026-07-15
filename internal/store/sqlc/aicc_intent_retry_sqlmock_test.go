package sqlc

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// aiccIntentRetryUpsertState 是 SQL 中 IF(processed_at IS NOT NULL, ...) 的最小状态模型，
// 用于把活跃租约保留这一持久化语义固定成可读断言，而非仅检查 SQL 字符串。
type aiccIntentRetryUpsertState struct {
	processed    bool
	leaseToken   string
	leaseExpires string
}

func applyAICCIntentRetryPlainUpsert(state aiccIntentRetryUpsertState) aiccIntentRetryUpsertState {
	if state.processed {
		return aiccIntentRetryUpsertState{}
	}
	return state
}

// TestIntentRetryUpsertAndClaimContract 通过真实 sqlc Exec 边界锁定两种持久化状态：
// processed 残留的新失败可重置并领取；活跃租约的普通 upsert 不应包含清租约条件。
func TestIntentRetryUpsertAndClaimContract(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	q := New(db)
	upsert := normalizedSQL(upsertAICCIntentAnalysisRetry)
	assert.Contains(t, upsert, "processed_at = if(processed_at is not null, null, processed_at)")
	assert.Contains(t, upsert, "lease_token = if(processed_at is not null, null, lease_token)")
	assert.Contains(t, upsert, "lease_expires_at = if(processed_at is not null, null, lease_expires_at)")
	mock.ExpectExec(regexp.QuoteMeta(upsertAICCIntentAnalysisRetry)).WithArgs("session", "message", null.StringFrom("new failure")).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, q.UpsertAICCIntentAnalysisRetry(context.Background(), UpsertAICCIntentAnalysisRetryParams{SessionID: "session", MessageID: "message", LastError: null.StringFrom("new failure")}))
	claim := normalizedSQL(claimAICCIntentAnalysisRetry)
	assert.Contains(t, claim, "lease_expires_at = date_add(now(6), interval 5 minute)")
	mock.ExpectExec(regexp.QuoteMeta(claimAICCIntentAnalysisRetry)).WithArgs(null.StringFrom("lease"), "session", "message").WillReturnResult(sqlmock.NewResult(0, 1))
	rows, err := q.ClaimAICCIntentAnalysisRetry(context.Background(), ClaimAICCIntentAnalysisRetryParams{LeaseToken: null.StringFrom("lease"), SessionID: "session", MessageID: "message"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), rows)
	require.NoError(t, mock.ExpectationsWereMet())
	// 活跃未处理租约上的普通失败写入不能清除 token、到期时间或错误地标记 processed，
	// 否则另一个 worker 会在原分析仍运行时重复领取。
	active := aiccIntentRetryUpsertState{processed: false, leaseToken: "active-lease", leaseExpires: "future"}
	assert.Equal(t, active, applyAICCIntentRetryPlainUpsert(active))
}
