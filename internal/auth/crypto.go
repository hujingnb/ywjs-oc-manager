package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// Cipher 提供基于 AES-256-GCM 的对称加解密能力。
//
// 使用约束：
//   - 必须用 32 字节 master_key 构造，否则 NewCipher 直接报错；
//   - 每次 Encrypt 自动生成随机 nonce，保证同一明文每次输出不同；
//   - 输出格式固定为 base64(nonce ‖ ciphertext ‖ tag)，便于直接落库；
//   - Decrypt 会校验 GCM 认证标签，篡改密文或换 key 都会拒绝。
//
// 该原语仅用于 manager 进程内：app 的 newapi key、agent token 等敏感字段加密入库。
// 不要把同一份 master_key 复用到外部组件，避免横向影响面扩大。
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher 用 32 字节 master_key 构造 cipher。
// 长度不符直接 fail-fast，避免使用方传入非 AES-256 长度后默默退化为弱加密。
func NewCipher(masterKey []byte) (*Cipher, error) {
	if len(masterKey) != 32 {
		return nil, fmt.Errorf("master key 必须是 32 字节，实际 %d", len(masterKey))
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES cipher 失败: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 GCM 模式失败: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 对明文加密并返回 base64 编码的 (nonce ‖ ciphertext ‖ tag)。
// 长度为 0 的明文也支持，输出仍包含 nonce 和 tag，避免调用方误以为空字符串可以跳过加密。
func (c *Cipher) Encrypt(plaintext []byte) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成 nonce 失败: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解 base64 后校验 GCM 认证标签并返回明文。
// 任何长度异常、base64 错误或认证失败都会返回错误，调用方禁止把错误细节回显给终端用户。
func (c *Cipher) Decrypt(token string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("解析 base64 密文失败: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(raw) < nonceSize+c.aead.Overhead() {
		return nil, fmt.Errorf("密文长度不足，至少需要 %d 字节", nonceSize+c.aead.Overhead())
	}
	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("解密失败: %w", err)
	}
	return plaintext, nil
}
