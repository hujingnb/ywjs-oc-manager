package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
		RuntimeTokenHash:       pgtype.Text{String: HashAppRuntimeToken(token), Valid: true},
		RuntimeTokenCiphertext: pgtype.Text{String: ciphertext, Valid: true},
	}

	updated, got, err := EnsureAppRuntimeToken(context.Background(), store, cipher, app)
	require.NoError(t, err)

	assert.Equal(t, token, got)
	assert.Equal(t, 0, store.setCalls)
	assert.Equal(t, app.RuntimeTokenHash, updated.RuntimeTokenHash)
}

// TestEnsureRuntimeTokenReusesWinnerAfterCASLost 验证并发初始化 CAS 失败时重新读取获胜者 token，而不是把 ErrNoRows 暴露给调用方。
func TestEnsureRuntimeTokenReusesWinnerAfterCASLost(t *testing.T) {
	cipher := newRuntimeTokenTestCipher(t)
	winnerToken := "winner-runtime-token"
	ciphertext, err := cipher.Encrypt([]byte(winnerToken))
	require.NoError(t, err)
	appID := mustParseUUID(testKnowledgeApp)
	store := &fakeAppRuntimeTokenStore{
		setErr: pgx.ErrNoRows,
		getApp: sqlc.App{
			ID:                     appID,
			RuntimeTokenHash:       pgtype.Text{String: HashAppRuntimeToken(winnerToken), Valid: true},
			RuntimeTokenCiphertext: pgtype.Text{String: ciphertext, Valid: true},
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

func (s *fakeAppRuntimeTokenStore) GetApp(context.Context, pgtype.UUID) (sqlc.App, error) {
	s.getCalls++
	if s.getErr != nil {
		return sqlc.App{}, s.getErr
	}
	return s.getApp, nil
}

func (s *fakeAppRuntimeTokenStore) SetAppRuntimeToken(_ context.Context, arg sqlc.SetAppRuntimeTokenParams) (sqlc.App, error) {
	s.setCalls++
	s.lastSet = arg
	if s.setErr != nil {
		return sqlc.App{}, s.setErr
	}
	return sqlc.App{ID: arg.ID, RuntimeTokenHash: arg.RuntimeTokenHash, RuntimeTokenCiphertext: arg.RuntimeTokenCiphertext}, nil
}

func (s *fakeAppRuntimeTokenStore) GetAppByRuntimeTokenHash(context.Context, pgtype.Text) (sqlc.App, error) {
	return sqlc.App{}, nil
}

func newRuntimeTokenTestCipher(t *testing.T) *auth.Cipher {
	t.Helper()
	cipher, err := auth.NewCipher(make([]byte, 32))
	require.NoError(t, err)
	return cipher
}
