package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

// AppRuntimeTokenStore 是实例 runtime token 生成和复用所需的最小数据库能力。
type AppRuntimeTokenStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	SetAppRuntimeToken(ctx context.Context, arg sqlc.SetAppRuntimeTokenParams) (sqlc.App, error)
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
	updated, err := store.SetAppRuntimeToken(ctx, sqlc.SetAppRuntimeTokenParams{
		ID:                     app.ID,
		RuntimeTokenHash:       pgtype.Text{String: HashAppRuntimeToken(token), Valid: true},
		RuntimeTokenCiphertext: pgtype.Text{String: ciphertext, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 并发初始化中其它 worker 已成功写入 token，本 worker 复用获胜者的密文，避免 manifest 写入失效 token。
			winner, getErr := store.GetApp(ctx, app.ID)
			if getErr != nil {
				return sqlc.App{}, "", fmt.Errorf("读取并发写入的 runtime token 失败: %w", getErr)
			}
			winnerToken, ok, decryptErr := decryptAppRuntimeToken(cipher, winner)
			if decryptErr != nil {
				return sqlc.App{}, "", decryptErr
			}
			if !ok {
				return sqlc.App{}, "", fmt.Errorf("并发写入的 runtime token 不完整")
			}
			return winner, winnerToken, nil
		}
		return sqlc.App{}, "", fmt.Errorf("保存 runtime token 失败: %w", err)
	}
	return updated, token, nil
}

func decryptAppRuntimeToken(cipher *auth.Cipher, app sqlc.App) (string, bool, error) {
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
