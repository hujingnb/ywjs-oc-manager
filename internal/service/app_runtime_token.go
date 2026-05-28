package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

// AppRuntimeTokenStore 是实例 runtime token 生成和复用所需的最小数据库能力。
type AppRuntimeTokenStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
	SetAppRuntimeToken(ctx context.Context, arg sqlc.SetAppRuntimeTokenParams) error
}

// EnsureAppRuntimeToken 确保实例拥有可写入 Hermes manifest 的 manager runtime API token。
// 已存在 token 密文时优先解密复用，避免每次重启都让旧容器内 token 失效。
func EnsureAppRuntimeToken(ctx context.Context, store AppRuntimeTokenStore, cipher *auth.Cipher, app sqlc.App) (sqlc.App, string, error) {
	if store == nil {
		return sqlc.App{}, "", fmt.Errorf("app runtime token store 未配置")
	}
	if cipher == nil {
		return sqlc.App{}, "", fmt.Errorf("app runtime token cipher 未配置")
	}
	if token, ok, err := decryptAppRuntimeToken(cipher, app); err != nil {
		return sqlc.App{}, "", err
	} else if ok {
		return app, token, nil
	}
	token, err := generateAppRuntimeToken()
	if err != nil {
		return sqlc.App{}, "", err
	}
	ciphertext, err := cipher.Encrypt([]byte(token))
	if err != nil {
		return sqlc.App{}, "", fmt.Errorf("加密 runtime token 失败: %w", err)
	}
	// SetAppRuntimeToken 使用条件更新（仅在 runtime_token_hash 为 NULL 时写入），
	// 并发竞争时零行更新表示其它 worker 已抢先写入；直接读回已有 token 复用。
	err = store.SetAppRuntimeToken(ctx, sqlc.SetAppRuntimeTokenParams{
		ID:                     app.ID,
		RuntimeTokenHash:       null.StringFrom(HashAppRuntimeToken(token)),
		RuntimeTokenCiphertext: null.StringFrom(ciphertext),
	})
	if err != nil {
		return sqlc.App{}, "", fmt.Errorf("保存 runtime token 失败: %w", err)
	}
	// 重新读取最新行：并发时其它 worker 可能已写入不同 token，读回确保返回实际落库的值。
	updated, getErr := store.GetApp(ctx, app.ID)
	if getErr != nil {
		return sqlc.App{}, "", fmt.Errorf("读取写入后的 runtime token 失败: %w", getErr)
	}
	// 解密读回的 token（可能是本次写入的，也可能是并发写入的）。
	winnerToken, ok, decryptErr := decryptAppRuntimeToken(cipher, updated)
	if decryptErr != nil {
		return sqlc.App{}, "", decryptErr
	}
	if !ok {
		return sqlc.App{}, "", fmt.Errorf("写入后 runtime token 不完整")
	}
	return updated, winnerToken, nil
}

func decryptAppRuntimeToken(cipher *auth.Cipher, app sqlc.App) (string, bool, error) {
	// RuntimeTokenCiphertext 和 RuntimeTokenHash 均为 null.String；两者均有效时才尝试解密。
	if !app.RuntimeTokenCiphertext.Valid || !app.RuntimeTokenHash.Valid {
		return "", false, nil
	}
	plain, err := cipher.Decrypt(app.RuntimeTokenCiphertext.String)
	if err != nil {
		return "", false, fmt.Errorf("解密 runtime token 失败: %w", err)
	}
	token := string(plain)
	if HashAppRuntimeToken(token) != app.RuntimeTokenHash.String {
		return "", false, nil
	}
	return token, true, nil
}

func generateAppRuntimeToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("生成 runtime token 失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
