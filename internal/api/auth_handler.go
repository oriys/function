// Package api 提供了函数即服务(FaaS)平台的HTTP API处理程序。
// 该文件实现了认证相关的HTTP处理器，包括API密钥管理和用户登录功能。
package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/auth"
)

// AuthHandler 是认证相关请求的处理器结构体。
// 负责处理API密钥的创建和用户登录等认证操作。
//
// 字段说明：
//   - jwt: JWT令牌管理器，用于生成和验证JWT令牌
//   - store: API密钥存储接口，用于持久化API密钥信息
type AuthHandler struct {
	jwt   *auth.JWTManager
	store APIKeyStore
}

// APIKeyStore 定义了API密钥存储的接口。
// 实现该接口的存储层负责API密钥的持久化操作。
//
// 方法说明：
//   - CreateAPIKey: 创建新的API密钥记录
//   - GetAPIKeyByHash: 通过哈希值查询API密钥
//   - DeleteAPIKey: 删除指定的API密钥
//   - ListAPIKeysByUser: 列出用户的所有API密钥
//   - DeleteAPIKeyByUser: 删除用户的指定API密钥
type APIKeyStore interface {
	// CreateAPIKey 创建新的API密钥
	// 参数：
	//   - id: 密钥的唯一标识符
	//   - name: 密钥的名称（用于标识用途）
	//   - keyHash: 密钥的哈希值（安全存储）
	//   - userID: 所属用户的ID
	//   - role: 用户角色
	// 返回值: 可能的错误
	CreateAPIKey(id, name, keyHash, userID, role string) error

	// GetAPIKeyByHash 通过哈希值获取API密钥信息
	// 参数：
	//   - keyHash: 密钥的哈希值
	// 返回值: 用户ID、角色、密钥ID和可能的错误
	GetAPIKeyByHash(keyHash string) (string, string, string, error)

	// DeleteAPIKey 删除指定的API密钥
	// 参数：
	//   - id: 要删除的密钥ID
	// 返回值: 可能的错误
	DeleteAPIKey(id string) error

	// ListAPIKeysByUser 列出用户的所有API密钥
	ListAPIKeysByUser(userID string) ([]APIKeyInfo, error)

	// DeleteAPIKeyByUser 删除用户的指定API密钥
	DeleteAPIKeyByUser(id, userID string) error
}

// APIKeyInfo API密钥信息（不包含敏感数据）
type APIKeyInfo struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	UserID    string  `json:"user_id"`
	Role      string  `json:"role"`
	CreatedAt string  `json:"created_at"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}

// NewAuthHandler 创建并返回一个新的AuthHandler实例。
//
// 参数：
//   - jwt: JWT管理器实例，用于令牌操作
//   - store: API密钥存储实例，用于密钥持久化
//
// 返回值：
//   - *AuthHandler: 初始化完成的认证处理器实例
func NewAuthHandler(jwt *auth.JWTManager, store APIKeyStore) *AuthHandler {
	return &AuthHandler{jwt: jwt, store: store}
}

// CreateAPIKeyRequest 定义了创建API密钥的请求结构。
//
// 字段说明：
//   - Name: API密钥的名称，用于标识该密钥的用途（如"production-key"、"test-key"）
type CreateAPIKeyRequest struct {
	Name string `json:"name"` // API密钥的名称
}

// CreateAPIKeyResponse 定义了创建API密钥的响应结构。
//
// 字段说明：
//   - ID: 新创建的API密钥的唯一标识符
//   - Name: API密钥的名称
//   - APIKey: 生成的API密钥值（仅在创建时返回一次，后续无法获取）
type CreateAPIKeyResponse struct {
	ID     string `json:"id"`      // API密钥的唯一标识符
	Name   string `json:"name"`    // API密钥的名称
	APIKey string `json:"api_key"` // 生成的API密钥（仅返回一次）
}

// CreateAPIKey 处理创建API密钥的请求。
// HTTP端点: POST /api/v1/auth/apikeys
//
// 功能说明：
//   - 为当前已认证的用户创建新的API密钥
//   - 生成的API密钥只会在响应中返回一次，请妥善保存
//   - 存储时只保存密钥的哈希值，确保安全性
//
// 请求体格式: CreateAPIKeyRequest (JSON)
//
// 返回值：
//   - 201: 创建成功，返回API密钥信息
//   - 400: 请求无效（如缺少名称）
//   - 401: 未认证
//   - 500: 服务器内部错误
func (h *AuthHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	// 从请求上下文中获取当前用户信息
	user := auth.GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// 解析请求体
	var req CreateAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	// 验证名称字段
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	// 生成新的API密钥和对应的哈希值
	key, hash, err := auth.GenerateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate key")
		return
	}

	// 生成唯一ID并保存API密钥记录
	id := uuid.New().String()
	if err := h.store.CreateAPIKey(id, req.Name, hash, user.UserID, user.Role); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create key")
		return
	}

	// 返回创建成功的响应（包含明文API密钥，仅此一次）
	writeJSON(w, http.StatusCreated, CreateAPIKeyResponse{
		ID:     id,
		Name:   req.Name,
		APIKey: key,
	})
}

// Login 处理用户登录请求。
// HTTP端点: POST /api/v1/auth/login
//
// 功能说明：
//   - 这是一个简化版本的登录接口
//   - 生产环境中应当增加密码验证等安全措施
//   - 成功登录后返回JWT令牌
//
// 请求体格式:
//   - user_id: 用户ID（必填）
//   - role: 用户角色（可选，默认为"user"）
//
// 返回值：
//   - 200: 登录成功，返回JWT令牌
//   - 400: 请求无效（如缺少user_id）
//   - 500: 令牌生成失败
//
// 安全警告：
//
//	当前实现为简化版本，生产环境需要增加：
//	- 密码验证
//	- 账户锁定机制
//	- 登录频率限制
//	- 多因素认证等
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	// 定义并解析登录请求结构
	var req struct {
		UserID string `json:"user_id"` // 用户ID
		Role   string `json:"role"`    // 用户角色
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	// 验证必填字段
	if req.UserID == "" {
		writeError(w, http.StatusBadRequest, "user_id required")
		return
	}

	// 设置默认角色
	if req.Role == "" {
		req.Role = "user"
	}

	// 生成JWT令牌
	token, err := h.jwt.Generate(req.UserID, req.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}

	// 返回JWT令牌
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// ListAPIKeys 列出当前用户的所有API密钥。
// HTTP端点: GET /api/v1/auth/apikeys
//
// 返回值：
//   - 200: 返回API密钥列表
//   - 401: 未认证
//   - 500: 服务器内部错误
func (h *AuthHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	keys, err := h.store.ListAPIKeysByUser(user.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list keys")
		return
	}

	// 转换为响应格式
	result := make([]map[string]interface{}, len(keys))
	for i, key := range keys {
		result[i] = map[string]interface{}{
			"id":         key.ID,
			"name":       key.Name,
			"created_at": key.CreatedAt,
		}
		if key.ExpiresAt != nil {
			result[i]["expires_at"] = key.ExpiresAt
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"api_keys": result})
}

// DeleteAPIKey 删除指定的API密钥。
// HTTP端点: DELETE /api/v1/auth/apikeys/{id}
//
// 返回值：
//   - 204: 删除成功
//   - 401: 未认证
//   - 404: 密钥不存在
//   - 500: 服务器内部错误
func (h *AuthHandler) DeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// 从URL路径获取密钥ID
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	err := h.store.DeleteAPIKeyByUser(id, user.UserID)
	if err != nil {
		if err.Error() == "api key not found or not owned by user" {
			writeError(w, http.StatusNotFound, "api key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete key")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
