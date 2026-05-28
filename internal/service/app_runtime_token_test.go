package service

import (
	"context"
	"database/sql"
	"testing"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

// TestEnsureRuntimeTokenCreatesEncryptedToken 验证缺失 runtime token 的实例会生成 hash 和密文。
func TestEnsureRuntimeTokenCreatesEncryptedToken(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	store := &fakeAppRuntimeTokenStore{}
	app := sqlc.App{ID: mustParseUUID(testKnowledgeApp)}

	updated, token, err := EnsureAppRuntimeToken(context.Background(), store, cipher, app)
	require.NoError(t, err)

	assert.NotEmpty(t, token)
	require.Equal(t, 1, store.setCalls)
	assert.Equal(t, HashAppRuntimeToken(token), store.lastSet.RuntimeTokenHash.String)
	assert.NotEqual(t, token, store.lastSet.RuntimeTokenCiphertext.String)
	plain, err := cipher.Decrypt(store.lastSet.RuntimeTokenCiphertext.String)
	require.NoError(t, err)
	assert.Equal(t, token, string(plain))
	assert.Equal(t, store.lastSet.RuntimeTokenHash, updated.RuntimeTokenHash)
}

// TestEnsureRuntimeTokenReusesExistingToken 验证已有有效 runtime token 时复用明文，不重复写库。
func TestEnsureRuntimeTokenReusesExistingToken(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	token := "existing-runtime-token"
	ciphertext, err := cipher.Encrypt([]byte(token))
	require.NoError(t, err)
	store := &fakeAppRuntimeTokenStore{}
	app := sqlc.App{
		ID:                     mustParseUUID(testKnowledgeApp),
		RuntimeTokenHash:       null.StringFrom(HashAppRuntimeToken(token)),
		RuntimeTokenCiphertext: null.StringFrom(ciphertext),
	}

	updated, got, err := EnsureAppRuntimeToken(context.Background(), store, cipher, app)
	require.NoError(t, err)

	assert.Equal(t, token, got)
	assert.Equal(t, 0, store.setCalls)
	assert.Equal(t, app.RuntimeTokenHash, updated.RuntimeTokenHash)
}

// TestEnsureRuntimeTokenReusesWinnerAfterCASLost 验证并发初始化 CAS 失败时重新读取获胜者 token，而不是把错误暴露给调用方。
// 新实现中 SetAppRuntimeToken 为 :exec（始终返回 nil），service 在写入后始终调用 GetApp 读回最终落库值；
// getApp 中包含并发获胜者写入的 token 字段，模拟 CAS 丢失后读回的获胜值。
func TestEnsureRuntimeTokenReusesWinnerAfterCASLost(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	winnerToken := "winner-runtime-token"
	ciphertext, err := cipher.Encrypt([]byte(winnerToken))
	require.NoError(t, err)
	appID := mustParseUUID(testKnowledgeApp)
	store := &fakeAppRuntimeTokenStore{
		// :exec 写入后直接读回，getApp 模拟并发获胜者已写入的 token。
		getApp: sqlc.App{
			ID:                     appID,
			RuntimeTokenHash:       null.StringFrom(HashAppRuntimeToken(winnerToken)),
			RuntimeTokenCiphertext: null.StringFrom(ciphertext),
		},
	}

	updated, got, err := EnsureAppRuntimeToken(context.Background(), store, cipher, sqlc.App{ID: appID})
	require.NoError(t, err)

	assert.Equal(t, winnerToken, got)
	assert.Equal(t, 1, store.setCalls)
	assert.Equal(t, 1, store.getCalls)
	assert.Equal(t, store.getApp.RuntimeTokenHash, updated.RuntimeTokenHash)
}

type fakeAppRuntimeTokenStore struct {
	setCalls int
	getCalls int
	lastSet  sqlc.SetAppRuntimeTokenParams
	setErr   error
	getApp   sqlc.App
	getErr   error
}

func (s *fakeAppRuntimeTokenStore) GetApp(_ context.Context, id string) (sqlc.App, error) {
	s.getCalls++
	if s.getErr != nil {
		return sqlc.App{}, s.getErr
	}
	if s.getApp.ID == "" {
		return sqlc.App{}, sql.ErrNoRows
	}
	return s.getApp, nil
}

func (s *fakeAppRuntimeTokenStore) SetAppRuntimeToken(_ context.Context, arg sqlc.SetAppRuntimeTokenParams) error {
	s.setCalls++
	s.lastSet = arg
	if s.setErr != nil {
		return s.setErr
	}
	// 写入成功时如果 getApp 未预置，则把刚写入的字段回填，供后续 GetApp 读回。
	if s.getApp.ID == "" {
		s.getApp = sqlc.App{
			ID:                     arg.ID,
			RuntimeTokenHash:       arg.RuntimeTokenHash,
			RuntimeTokenCiphertext: arg.RuntimeTokenCiphertext,
		}
	}
	return nil
}

func newRuntimeTokenTestCipher(t *testing.T) *auth.Cipher {
	t.Helper()
	cipher, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)
	return cipher
}
