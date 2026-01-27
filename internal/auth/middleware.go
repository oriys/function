// Package auth 提供身份认证和授权相关的功能。
// 该包实现了基于 JWT（JSON Web Token）和 API Key 的双重认证机制，
// 用于保护 API 接口的安全访问。
package auth

import (
	"context"
	"net/http"
	"strings"
)

// contextKey 是用于在 context 中存储值的自定义类型。
// 使用自定义类型可以避免与其他包的 context 键冲突。
type contextKey string

// 定义 context 键常量
const (
	// UserContextKey 是用于在请求上下文中存储用户信息的键
	UserContextKey contextKey = "user"
)

// UserContext 存储已认证用户的上下文信息。
// 在请求通过认证后，此结构体会被存储在请求的 context 中。
type UserContext struct {
	// UserID 用户的唯一标识符
	UserID string
	// Role 用户的角色，用于权限控制
	Role string
	// Method 认证方式，可能的值为 "jwt" 或 "apikey"
	Method string
}

// APIKeyValidator 定义了 API Key 验证器的接口。
// 任何实现此接口的类型都可以用于验证 API Key。
type APIKeyValidator interface {
	// ValidateAPIKey 验证给定的 API Key 是否有效。
	// 参数:
	//   - key: 需要验证的 API Key
	// 返回:
	//   - *UserContext: 如果验证成功，返回关联的用户上下文
	//   - error: 如果验证失败，返回错误
	ValidateAPIKey(key string) (*UserContext, error)
}

// Middleware 是认证中间件，用于验证 HTTP 请求的身份。
// 它支持 JWT 和 API Key 两种认证方式。
type Middleware struct {
	// jwt JWT 管理器，用于验证 JWT 令牌
	jwt *JWTManager
	// apiKeyHeader 存储 API Key 的 HTTP 头名称
	apiKeyHeader string
	// keyValidator API Key 验证器
	keyValidator APIKeyValidator
	// enabled 是否启用认证，为 false 时跳过所有认证检查
	enabled bool
}

// NewMiddleware 创建并返回一个新的认证中间件实例。
// 参数:
//   - jwt: JWT 管理器实例
//   - apiKeyHeader: 用于传递 API Key 的 HTTP 头名称（如 "X-API-Key"）
//   - keyValidator: API Key 验证器实现
//   - enabled: 是否启用认证功能
//
// 返回:
//   - *Middleware: 初始化后的中间件实例
func NewMiddleware(jwt *JWTManager, apiKeyHeader string, keyValidator APIKeyValidator, enabled bool) *Middleware {
	return &Middleware{
		jwt:          jwt,
		apiKeyHeader: apiKeyHeader,
		keyValidator: keyValidator,
		enabled:      enabled,
	}
}

// Authenticate 是一个 HTTP 中间件函数，用于验证请求的身份。
// 它首先尝试 API Key 认证，如果失败则尝试 JWT Bearer Token 认证。
// 认证成功后，用户信息会被存储在请求的 context 中。
// 参数:
//   - next: 下一个要执行的 HTTP 处理器
//
// 返回:
//   - http.Handler: 包装后的 HTTP 处理器
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 检查认证是否启用，如果未启用则直接放行
		if !m.enabled {
			next.ServeHTTP(w, r)
			return
		}

		// 首先尝试 API Key 认证
		// 从请求头中获取 API Key
		if apiKey := r.Header.Get(m.apiKeyHeader); apiKey != "" {
			// 检查是否配置了 API Key 验证器
			if m.keyValidator != nil {
				// 验证 API Key 的有效性
				if user, err := m.keyValidator.ValidateAPIKey(apiKey); err == nil {
					// API Key 验证成功，将用户信息存入 context
					ctx := context.WithValue(r.Context(), UserContextKey, user)
					// 使用新的 context 继续处理请求
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}
		}

		// API Key 认证失败或未提供，尝试 JWT Bearer Token 认证
		// 获取 Authorization 头
		authHeader := r.Header.Get("Authorization")
		// 检查是否为 Bearer Token 格式
		if strings.HasPrefix(authHeader, "Bearer ") {
			// 提取令牌字符串（移除 "Bearer " 前缀）
			token := strings.TrimPrefix(authHeader, "Bearer ")
			// 验证 JWT 令牌
			if claims, err := m.jwt.Validate(token); err == nil {
				// JWT 验证成功，构建用户上下文
				user := &UserContext{
					UserID: claims.UserID,
					Role:   claims.Role,
					Method: "jwt",
				}
				// 将用户信息存入 context
				ctx := context.WithValue(r.Context(), UserContextKey, user)
				// 使用新的 context 继续处理请求
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		// 所有认证方式都失败，返回 401 未授权错误
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	})
}

// GetUser 从请求上下文中提取已认证的用户信息。
// 此函数通常在已通过认证的处理器中调用，用于获取当前用户信息。
// 参数:
//   - ctx: 请求的上下文
//
// 返回:
//   - *UserContext: 如果找到用户信息则返回，否则返回 nil
func GetUser(ctx context.Context) *UserContext {
	// 尝试从 context 中获取用户信息
	if user, ok := ctx.Value(UserContextKey).(*UserContext); ok {
		return user
	}
	// 未找到用户信息，返回 nil
	return nil
}
