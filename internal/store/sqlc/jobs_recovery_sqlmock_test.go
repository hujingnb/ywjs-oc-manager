package sqlc

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequeueExpiredRunningJobs 覆盖任务锁回收的过期、未过期与空锁时间三种数据库结果。
func TestRequeueExpiredRunningJobs(t *testing.T) {
	lockedBefore := null.TimeFrom(time.Date(2026, 7, 21, 0, 55, 0, 0, time.UTC))
	cases := []struct {
		name string
		rows int64
	}{
		{name: "超过租约的 running 锁被回收", rows: 1},      // 过期锁应回到 pending。
		{name: "仍在租约内的 running 锁不受影响", rows: 0},    // SQL 的 locked_at < threshold 不匹配新鲜锁。
		{name: "locked_at 为空的历史异常记录不受影响", rows: 0}, // NULL 与阈值比较不成立，不能被误回收。
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectExec(regexp.QuoteMeta(requeueExpiredRunningJobs)).
				WithArgs(lockedBefore).
				WillReturnResult(sqlmock.NewResult(0, testCase.rows))

			rows, err := New(db).RequeueExpiredRunningJobs(context.Background(), lockedBefore)

			require.NoError(t, err)
			assert.Equal(t, testCase.rows, rows)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
