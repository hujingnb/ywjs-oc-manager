package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// PasswordParams 描述 Argon2id 哈希参数。
// 默认参数偏向安全；测试可使用更小参数避免单元测试过慢。
type PasswordParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultPasswordParams 是后台账号密码的默认哈希参数。
var DefaultPasswordParams = PasswordParams{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 2,
	SaltLength:  16,
	KeyLength:   32,
}

// HashPassword 使用 Argon2id 生成 PHC 字符串格式的密码 hash。
func HashPassword(password string, params PasswordParams) (string, error) {
	if password == "" {
		return "", errors.New("密码不能为空")
	}
	if err := params.validate(); err != nil {
		return "", err
	}

	salt := make([]byte, params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成密码盐失败: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)
	encodedSalt := base64.RawStdEncoding.EncodeToString(salt)
	encodedKey := base64.RawStdEncoding.EncodeToString(key)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", params.Memory, params.Iterations, params.Parallelism, encodedSalt, encodedKey), nil
}

// VerifyPassword 校验明文密码是否匹配 PHC 格式 hash。
// 解析失败或算法不支持时返回 false，不向登录接口泄露具体失败原因。
func VerifyPassword(password, encodedHash string) bool {
	params, salt, expectedKey, err := parsePasswordHash(encodedHash)
	if err != nil {
		return false
	}
	actualKey := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)
	return subtle.ConstantTimeCompare(actualKey, expectedKey) == 1
}

func (p PasswordParams) validate() error {
	if p.Memory == 0 || p.Iterations == 0 || p.Parallelism == 0 || p.SaltLength == 0 || p.KeyLength == 0 {
		return errors.New("Argon2id 参数必须全部大于 0")
	}
	return nil
}

func (p PasswordParams) validateCost() error {
	if p.Memory == 0 || p.Iterations == 0 || p.Parallelism == 0 {
		return errors.New("Argon2id 计算参数必须全部大于 0")
	}
	return nil
}

func parsePasswordHash(encodedHash string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return PasswordParams{}, nil, nil, errors.New("密码 hash 格式不支持")
	}

	params, err := parsePasswordParams(parts[3])
	if err != nil {
		return PasswordParams{}, nil, nil, err
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return PasswordParams{}, nil, nil, err
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return PasswordParams{}, nil, nil, err
	}
	params.SaltLength = uint32(len(salt))
	params.KeyLength = uint32(len(key))
	return params, salt, key, nil
}

func parsePasswordParams(input string) (PasswordParams, error) {
	var params PasswordParams
	for _, item := range strings.Split(input, ",") {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			return PasswordParams{}, errors.New("密码 hash 参数格式错误")
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return PasswordParams{}, err
		}
		switch key {
		case "m":
			params.Memory = uint32(parsed)
		case "t":
			params.Iterations = uint32(parsed)
		case "p":
			params.Parallelism = uint8(parsed)
		default:
			return PasswordParams{}, fmt.Errorf("未知密码 hash 参数: %s", key)
		}
	}
	return params, params.validateCost()
}
