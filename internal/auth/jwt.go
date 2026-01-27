// Package auth 提供身份认证和授权相关的功能。
// 该包实现了基于 JWT（JSON Web Token）和 API Key 的双重认证机制，
// 用于保护 API 接口的安全访问。
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 定义 JWT 相关的错误类型
var (
	// ErrInvalidToken 表示提供的令牌无效或格式错误
	ErrInvalidToken = errors.New("invalid token")
	// ErrExpiredToken 表示令牌已过期
	ErrExpiredToken = errors.New("token has expired")
)

// Claims 定义 JWT 令牌中的声明（Claims）结构。
// 它包含了用户身份信息和标准的 JWT 注册声明。
type Claims struct {
	// UserID 存储用户的唯一标识符
	UserID string `json:"user_id"`
	// Role 存储用户的角色信息，用于权限控制
	Role string `json:"role"`
	// RegisteredClaims 嵌入标准的 JWT 注册声明，包含过期时间、签发时间等
	jwt.RegisteredClaims
}

// JWTManager 是 JWT 令牌管理器，负责令牌的生成和验证。
// 它封装了 JWT 的密钥和过期时间配置。
type JWTManager struct {
	// secret 是用于签名和验证 JWT 的密钥
	secret []byte
	// expiration 定义令牌的有效期时长
	expiration time.Duration
}

// NewJWTManager 创建并返回一个新的 JWT 管理器实例。
// 参数:
//   - secret: JWT 签名密钥，应该是一个安全的随机字符串
//   - expiration: 令牌的有效期时长
//
// 返回:
//   - *JWTManager: 初始化后的 JWT 管理器
func NewJWTManager(secret string, expiration time.Duration) *JWTManager {
	return &JWTManager{
		secret:     []byte(secret),
		expiration: expiration,
	}
}

// Generate 为指定用户生成一个新的 JWT 令牌。
// 参数:
//   - userID: 用户的唯一标识符
//   - role: 用户的角色（如 "admin"、"user" 等）
//
// 返回:
//   - string: 生成的 JWT 令牌字符串
//   - error: 如果生成失败则返回错误
func (m *JWTManager) Generate(userID, role string) (string, error) {
	// 构建 JWT 声明，包含用户信息和过期时间
	claims := &Claims{
		UserID: userID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			// 设置令牌过期时间为当前时间加上配置的有效期
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(m.expiration)),
			// 记录令牌的签发时间
			IssuedAt: jwt.NewNumericDate(time.Now()),
		},
	}

	// 使用 HS256 算法创建带有声明的新令牌
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	// 使用密钥对令牌进行签名并返回字符串形式
	return token.SignedString(m.secret)
}

// Validate 验证 JWT 令牌的有效性并提取其中的声明信息。
// 参数:
//   - tokenStr: 需要验证的 JWT 令牌字符串
//
// 返回:
//   - *Claims: 如果验证成功，返回令牌中的声明信息
//   - error: 如果令牌无效或已过期，返回相应的错误
func (m *JWTManager) Validate(tokenStr string) (*Claims, error) {
	// 解析并验证令牌，使用密钥进行签名验证
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		// 返回用于验证签名的密钥
		return m.secret, nil
	})
	if err != nil {
		// 解析失败，令牌格式错误或签名无效
		return nil, ErrInvalidToken
	}

	// 尝试将令牌声明转换为 Claims 类型
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		// 类型转换失败或令牌无效（如已过期）
		return nil, ErrInvalidToken
	}

	// 验证成功，返回声明信息
	return claims, nil
}
