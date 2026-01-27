// Package auth 提供身份认证和授权相关的功能。
// 该包实现了基于 JWT（JSON Web Token）和 API Key 的双重认证机制，
// 用于保护 API 接口的安全访问。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

// ErrAPIKeyNotFound 表示请求的 API Key 在系统中不存在
var ErrAPIKeyNotFound = errors.New("api key not found")

// APIKey 表示一个 API Key 实体，包含密钥的元数据信息。
// 注意：出于安全考虑，我们不存储原始密钥，只存储其哈希值。
type APIKey struct {
	// ID API Key 的唯一标识符
	ID string
	// Name API Key 的名称，用于标识和管理
	Name string
	// KeyHash API Key 的 SHA-256 哈希值，用于验证而非存储原始密钥
	KeyHash string
	// UserID 关联的用户 ID，表示此 API Key 属于哪个用户
	UserID string
	// Role 此 API Key 的权限角色
	Role string
	// CreatedAt API Key 的创建时间
	CreatedAt time.Time
	// ExpiresAt API Key 的过期时间，nil 表示永不过期
	ExpiresAt *time.Time
}

// GenerateAPIKey 生成一个新的 API Key。
// 该函数使用加密安全的随机数生成器创建密钥，并计算其哈希值用于存储。
// 返回:
//   - string: 原始 API Key（以 "fn_" 为前缀，应安全地发送给用户）
//   - string: API Key 的 SHA-256 哈希值（应存储在数据库中）
//   - error: 如果随机数生成失败则返回错误
func GenerateAPIKey() (string, string, error) {
	// 创建 32 字节的缓冲区用于存储随机数据
	bytes := make([]byte, 32)
	// 使用加密安全的随机数生成器填充缓冲区
	if _, err := rand.Read(bytes); err != nil {
		// 随机数生成失败，返回错误
		return "", "", err
	}
	// 构建 API Key：使用 "fn_" 前缀 + 十六进制编码的随机数据
	// 前缀 "fn_" 用于标识这是本系统（function）的 API Key
	key := "fn_" + hex.EncodeToString(bytes)
	// 计算 API Key 的哈希值用于安全存储
	hash := HashAPIKey(key)
	// 返回原始密钥和哈希值
	return key, hash, nil
}

// HashAPIKey 计算 API Key 的 SHA-256 哈希值。
// 此哈希值用于在数据库中安全存储 API Key，
// 验证时将用户提供的 Key 哈希后与存储的哈希值比较。
// 参数:
//   - key: 原始 API Key 字符串
//
// 返回:
//   - string: API Key 的 SHA-256 哈希值（十六进制编码）
func HashAPIKey(key string) string {
	// 计算 SHA-256 哈希值
	h := sha256.Sum256([]byte(key))
	// 将哈希值转换为十六进制字符串返回
	return hex.EncodeToString(h[:])
}
