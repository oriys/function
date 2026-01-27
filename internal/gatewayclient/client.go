// Package gatewayclient 提供访问 Function Gateway HTTP API 的 Go 客户端封装。
// 该包将常用的函数管理接口（创建/查询/更新/删除）封装为结构化方法，便于在程序中复用。
package gatewayclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client 是 Function Gateway HTTP API 客户端。
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New 创建一个新的客户端。
// baseURL 为空时默认使用 http://localhost:8080。
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Function 表示函数对象（与网关 API 的 JSON 字段对应）。
type Function struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Runtime     string            `json:"runtime"`
	Handler     string            `json:"handler"`
	Code        string            `json:"code,omitempty"`
	CodeHash    string            `json:"code_hash,omitempty"`
	MemoryMB    int               `json:"memory_mb"`
	TimeoutSec  int               `json:"timeout_sec"`
	EnvVars     map[string]string `json:"env_vars,omitempty"`
	Status      string            `json:"status"`
	Version     int               `json:"version"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// CreateFunctionRequest 表示创建函数的请求体。
type CreateFunctionRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Runtime     string            `json:"runtime"`
	Handler     string            `json:"handler"`
	Code        string            `json:"code"`
	MemoryMB    int               `json:"memory_mb,omitempty"`
	TimeoutSec  int               `json:"timeout_sec,omitempty"`
	EnvVars     map[string]string `json:"env_vars,omitempty"`
}

// UpdateFunctionRequest 表示更新函数的请求体（使用指针字段表示“是否更新该字段”）。
type UpdateFunctionRequest struct {
	Description *string            `json:"description,omitempty"`
	Code        *string            `json:"code,omitempty"`
	Handler     *string            `json:"handler,omitempty"`
	MemoryMB    *int               `json:"memory_mb,omitempty"`
	TimeoutSec  *int               `json:"timeout_sec,omitempty"`
	EnvVars     *map[string]string `json:"env_vars,omitempty"`
}

// ListFunctionsResponse 表示函数列表查询响应。
type ListFunctionsResponse struct {
	Functions []Function `json:"functions"`
	Total     int        `json:"total"`
	Offset    int        `json:"offset"`
	Limit     int        `json:"limit"`
}

// apiError 是网关返回的标准错误结构。
type apiError struct {
	Message string `json:"error"`
}

func (e *apiError) Error() string {
	if e == nil || e.Message == "" {
		return "api error"
	}
	return e.Message
}

// do 是内部通用请求方法，负责：
// - 拼接 URL 与 query
// - JSON 编码请求体
// - 发起 HTTP 请求并解析 JSON 响应
// - 将 4xx/5xx 转换为可读错误
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, result any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var apiErr apiError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Message != "" {
			return &apiErr
		}
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	if result == nil {
		return nil
	}
	if len(respBody) == 0 {
		return errors.New("empty response body")
	}
	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

// CreateFunction 创建函数。
func (c *Client) CreateFunction(ctx context.Context, req *CreateFunctionRequest) (*Function, error) {
	var fn Function
	if err := c.do(ctx, http.MethodPost, "/api/v1/functions", nil, req, &fn); err != nil {
		return nil, err
	}
	return &fn, nil
}

// ListFunctions 获取函数列表（支持 offset/limit 分页）。
func (c *Client) ListFunctions(ctx context.Context, offset, limit int) (*ListFunctionsResponse, error) {
	q := url.Values{}
	if offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", offset))
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	var resp ListFunctionsResponse
	if err := c.do(ctx, http.MethodGet, "/api/v1/functions", q, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetFunction 根据 ID 或 name 获取函数详情。
func (c *Client) GetFunction(ctx context.Context, idOrName string) (*Function, error) {
	var fn Function
	if err := c.do(ctx, http.MethodGet, "/api/v1/functions/"+url.PathEscape(idOrName), nil, nil, &fn); err != nil {
		return nil, err
	}
	return &fn, nil
}

// UpdateFunction 更新函数（按需更新请求体中提供的字段）。
func (c *Client) UpdateFunction(ctx context.Context, idOrName string, req *UpdateFunctionRequest) (*Function, error) {
	var fn Function
	if err := c.do(ctx, http.MethodPut, "/api/v1/functions/"+url.PathEscape(idOrName), nil, req, &fn); err != nil {
		return nil, err
	}
	return &fn, nil
}

// DeleteFunction 删除函数（按 ID 或 name）。
func (c *Client) DeleteFunction(ctx context.Context, idOrName string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/functions/"+url.PathEscape(idOrName), nil, nil, nil)
}
