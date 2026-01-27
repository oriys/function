// Package cmd 提供 nimbus 命令行工具的所有子命令实现。
// 本文件实现 API 客户端，用于与 Nimbus 平台的后端服务进行通信。
//
// Client 封装了所有与 API 服务器的交互逻辑，包括：
//   - 函数的 CRUD 操作（创建、读取、更新、删除）
//   - 函数调用（同步和异步）
//   - 调用记录查询
//   - 系统状态查询
//   - 工作流管理与执行
//   - 层（Layer）管理
//   - 环境（Environment）管理
//   - 系统统计与配额查询
//
// 客户端使用 HTTP/JSON 协议与服务器通信，支持错误处理和超时控制。
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Client 是 Nimbus 平台的 API 客户端。
// 封装了与后端服务通信的所有方法，使用 HTTP/JSON 协议。
type Client struct {
	baseURL    string       // API 服务器的基础 URL
	httpClient *http.Client // HTTP 客户端，用于发送请求
}

// NewClient 创建一个新的 API 客户端实例。
// 从 viper 配置中读取 api_url，如果未配置则使用默认值 http://localhost:8080。
// HTTP 客户端默认超时时间为 60 秒。
//
// 返回值：
//   - *Client: 新创建的客户端实例
func NewClient() *Client {
	baseURL := viper.GetString("api_url")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// ====== 领域模型定义 ====== 

// Function 表示一个 serverless 函数的完整信息。
type Function struct {
	ID             string            `json:"id"`                       // 函数唯一标识符
	Name           string            `json:"name"`                     // 函数名称
	Description    string            `json:"description,omitempty"`    // 函数描述
	Tags           []string          `json:"tags,omitempty"`           // 标签
	Pinned         bool              `json:"pinned"`                   // 是否置顶
	Runtime        string            `json:"runtime"`                  // 运行时类型
	Handler        string            `json:"handler"`                  // 处理函数入口点
	Code           string            `json:"code,omitempty"`           // 函数代码（可选）
	Binary         string            `json:"binary,omitempty"`         // 二进制内容
	CodeHash       string            `json:"code_hash,omitempty"`      // 代码哈希
	MemoryMB       int               `json:"memory_mb"`                // 内存限制（MB）
	TimeoutSec     int               `json:"timeout_sec"`              // 超时时间（秒）
	MaxConcurrency int               `json:"max_concurrency"`          // 最大并发
	EnvVars        map[string]string `json:"env_vars,omitempty"`       // 环境变量
	Status         string            `json:"status"`                   // 函数状态
	StatusMessage  string            `json:"status_message,omitempty"` // 状态消息
	TaskID         string            `json:"task_id,omitempty"`        // 异步任务ID
	Version        int               `json:"version"`                  // 版本号
	CronExpression string            `json:"cron_expression,omitempty"` // 定时任务表达式
	HTTPPath       string            `json:"http_path,omitempty"`      // HTTP 路径
	HTTPMethods    []string          `json:"http_methods,omitempty"`   // HTTP 方法
	WebhookEnabled bool              `json:"webhook_enabled"`          // Webhook 是否启用
	WebhookKey     string            `json:"webhook_key,omitempty"`     // Webhook 密钥
	LastDeployedAt *time.Time        `json:"last_deployed_at,omitempty"` // 最后部署时间
	CreatedAt      time.Time         `json:"created_at"`               // 创建时间
	UpdatedAt      time.Time         `json:"updated_at"`               // 更新时间
	Invocations    int64             `json:"invocations,omitempty"`     // 调用次数
}

// CreateFunctionRequest 表示创建函数的 API 请求体。
type CreateFunctionRequest struct {
	Name           string            `json:"name"`                      // 函数名称，需唯一
	Description    string            `json:"description,omitempty"`     // 描述
	Tags           []string          `json:"tags,omitempty"`            // 标签
	Runtime        string            `json:"runtime"`                   // 运行时类型
	Handler        string            `json:"handler"`                   // 处理函数入口点
	Code           string            `json:"code"`                      // 函数代码内容
	Binary         string            `json:"binary,omitempty"`          // 二进制内容
	MemoryMB       int               `json:"memory_mb,omitempty"`       // 内存限制（MB）
	TimeoutSec     int               `json:"timeout_sec,omitempty"`     // 超时时间（秒）
	EnvVars        map[string]string `json:"env_vars,omitempty"`        // 环境变量
	CronExpression string            `json:"cron_expression,omitempty"` // 定时任务表达式
	HTTPPath       string            `json:"http_path,omitempty"`       // HTTP 路径
	HTTPMethods    []string          `json:"http_methods,omitempty"`    // HTTP 方法
}

// UpdateFunctionRequest 表示更新函数的 API 请求体。
type UpdateFunctionRequest struct {
	Description    *string            `json:"description,omitempty"`
	Tags           *[]string          `json:"tags,omitempty"`
	Handler        *string            `json:"handler,omitempty"`
	Code           *string            `json:"code,omitempty"`
	MemoryMB       *int               `json:"memory_mb,omitempty"`
	TimeoutSec     *int               `json:"timeout_sec,omitempty"`
	MaxConcurrency *int               `json:"max_concurrency,omitempty"`
	EnvVars        *map[string]string `json:"env_vars,omitempty"`
	CronExpression *string            `json:"cron_expression,omitempty"`
	HTTPPath       *string            `json:"http_path,omitempty"`
	HTTPMethods    *[]string          `json:"http_methods,omitempty"`
}

// InvokeResponse 表示函数调用的响应结果。
type InvokeResponse struct {
	RequestID    string          `json:"request_id"`
	StatusCode   int             `json:"status_code"`
	Body         json.RawMessage `json:"body,omitempty"`
	Error        string          `json:"error,omitempty"`
	DurationMs   int64           `json:"duration_ms"`
	ColdStart    bool            `json:"cold_start"`
	BilledTimeMs int64           `json:"billed_time_ms"`
}

// AsyncInvokeResponse 表示异步调用的响应。
type AsyncInvokeResponse struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

// Invocation 表示一次函数调用的完整记录。
type Invocation struct {
	ID          string          `json:"id"`
	FunctionID  string          `json:"function_id"`
	Status      string          `json:"status"`
	Input       json.RawMessage `json:"input,omitempty"`
	Output      json.RawMessage `json:"output,omitempty"`
	Error       string          `json:"error,omitempty"`
	DurationMs  int64           `json:"duration_ms"`
	ColdStart   bool            `json:"cold_start"`
	StartedAt   time.Time       `json:"started_at"`
	CompletedAt time.Time       `json:"completed_at,omitempty"`
}

// Workflow 表示工作流定义。
type Workflow struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Version     int                `json:"version"`
	Status      string             `json:"status"`
	Definition  WorkflowDefinition `json:"definition"`
	TimeoutSec  int                `json:"timeout_sec"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

// WorkflowDefinition 工作流定义的 DAG 结构
type WorkflowDefinition struct {
	StartAt string           `json:"start_at"`
	States  map[string]State `json:"states"`
}

// State 单个状态定义
type State struct {
	Type       string          `json:"type"`
	Next       string          `json:"next,omitempty"`
	End        bool            `json:"end,omitempty"`
	FunctionID string          `json:"function_id,omitempty"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
	// 其他字段简化处理
}

// WorkflowExecution 工作流执行实例
type WorkflowExecution struct {
	ID              string          `json:"id"`
	WorkflowID      string          `json:"workflow_id"`
	WorkflowName    string          `json:"workflow_name"`
	WorkflowVersion int             `json:"workflow_version"`
	Status          string          `json:"status"`
	Input           json.RawMessage `json:"input,omitempty"`
	Output          json.RawMessage `json:"output,omitempty"`
	Error           string          `json:"error,omitempty"`
	CurrentState    string          `json:"current_state,omitempty"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

// Layer 表示共享依赖层。
type Layer struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Description        string    `json:"description,omitempty"`
	CompatibleRuntimes []string  `json:"compatible_runtimes"`
	LatestVersion      int       `json:"latest_version"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// LayerVersion 表示层的一个不可变版本。
type LayerVersion struct {
	ID          string    `json:"id"`
	LayerID     string    `json:"layer_id"`
	Version     int       `json:"version"`
	ContentHash string    `json:"content_hash"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

// FunctionLayer 表示函数与层的关联关系。
type FunctionLayer struct {
	LayerID      string `json:"layer_id"`
	LayerName    string `json:"layer_name"`
	LayerVersion int    `json:"layer_version"`
	Order        int    `json:"order"`
}

// Environment 表示部署环境。
type Environment struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	IsDefault   bool      `json:"is_default"`
	CreatedAt   time.Time `json:"created_at"`
}

// FunctionEnvConfig 表示函数在特定环境下的配置。
type FunctionEnvConfig struct {
	FunctionID      string            `json:"function_id"`
	EnvironmentID   string            `json:"environment_id"`
	EnvironmentName string            `json:"environment_name,omitempty"`
	EnvVars         map[string]string `json:"env_vars,omitempty"`
	MemoryMB        *int              `json:"memory_mb,omitempty"`
	TimeoutSec      *int              `json:"timeout_sec,omitempty"`
	ActiveAlias     string            `json:"active_alias,omitempty"`
}

// QuotaUsage 表示配额使用情况。
type QuotaUsage struct {
	FunctionCount          int     `json:"function_count"`
	TotalMemoryMB           int     `json:"total_memory_mb"`
	TodayInvocations       int64   `json:"today_invocations"`
	TotalCodeSizeKB        int64   `json:"total_code_size_kb"`
	MaxFunctions           int     `json:"max_functions"`
	MaxMemoryMB            int     `json:"max_memory_mb"`
	MaxInvocationsPerDay   int64   `json:"max_invocations_per_day"`
	MaxCodeSizeKB          int64   `json:"max_code_size_kb"`
	FunctionUsagePercent   float64 `json:"function_usage_percent"`
	MemoryUsagePercent     float64 `json:"memory_usage_percent"`
	InvocationUsagePercent float64 `json:"invocation_usage_percent"`
	CodeUsagePercent       float64 `json:"code_usage_percent"`
}

// PoolStats 表示虚拟机池的统计信息。
type PoolStats struct {
	Runtime  string `json:"runtime"`
	WarmVMs  int    `json:"warm_vms"`
	BusyVMs  int    `json:"busy_vms"`
	TotalVMs int    `json:"total_vms"`
	MaxVMs   int    `json:"max_vms"`
}

// SystemStatus 表示系统整体状态信息。
type SystemStatus struct {
	Status    string      `json:"status"`
	Version   string      `json:"version"`
	Uptime    string      `json:"uptime"`
	PoolStats []PoolStats `json:"pool_stats"`
}

// Stats 表示简单统计信息。
type Stats struct {
	Functions   int64 `json:"functions"`
	Invocations int64 `json:"invocations"`
}

// APIError 表示 API 返回的错误响应。
type APIError struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Error_    string `json:"error"`
	Stack     string `json:"stack,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = e.Error_
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("API error %d: %s", e.Code, msg))

	if e.RequestID != "" {
		sb.WriteString(fmt.Sprintf("\n  Request ID: %s", e.RequestID))
	}
	if e.TraceID != "" {
		sb.WriteString(fmt.Sprintf("\n  Trace ID: %s", e.TraceID))
	}
	if e.Stack != "" {
		sb.WriteString(fmt.Sprintf("\n  Stack trace:\n%s", indentStack(e.Stack)))
	}

	return sb.String()
}

func indentStack(stack string) string {
	lines := strings.Split(stack, "\n")
	var sb strings.Builder
	for _, line := range lines {
		if line != "" {
			sb.WriteString("    ")
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// do 执行 HTTP 请求并处理响应。
func (c *Client) do(method, path string, body interface{}, result interface{}) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
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
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var apiErr APIError
		if err := json.Unmarshal(respBody, &apiErr); err == nil && (apiErr.Message != "" || apiErr.Error_ != "") {
			apiErr.Code = resp.StatusCode
			return &apiErr
		}
		var simpleErr struct {
			Error     string `json:"error"`
			Stack     string `json:"stack,omitempty"`
			RequestID string `json:"request_id,omitempty"`
		}
		if err := json.Unmarshal(respBody, &simpleErr); err == nil && simpleErr.Error != "" {
			return &APIError{
				Code:      resp.StatusCode,
				Message:   simpleErr.Error,
				Stack:     simpleErr.Stack,
				RequestID: simpleErr.RequestID,
			}
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}

	return nil
}

// ====== 函数操作方法 ====== 

func (c *Client) CreateFunction(req *CreateFunctionRequest) (*Function, error) {
	var fn Function
	if err := c.do("POST", "/api/v1/functions", req, &fn); err != nil {
		return nil, err
	}
	return &fn, nil
}

func (c *Client) ListFunctions() ([]Function, error) {
	var result struct {
		Functions []Function `json:"functions"`
	}
	if err := c.do("GET", "/api/v1/functions", nil, &result); err != nil {
		return nil, err
	}
	return result.Functions, nil
}

func (c *Client) GetFunction(idOrName string) (*Function, error) {
	var fn Function
	if err := c.do("GET", "/api/v1/functions/"+idOrName, nil, &fn); err != nil {
		return nil, err
	}
	return &fn, nil
}

func (c *Client) UpdateFunction(idOrName string, req *UpdateFunctionRequest) (*Function, error) {
	var fn Function
	if err := c.do("PUT", "/api/v1/functions/"+idOrName, req, &fn); err != nil {
		return nil, err
	}
	return &fn, nil
}

func (c *Client) DeleteFunction(idOrName string) error {
	return c.do("DELETE", "/api/v1/functions/"+idOrName, nil, nil)
}

func (c *Client) InvokeFunction(idOrName string, payload json.RawMessage) (*InvokeResponse, error) {
	req, err := http.NewRequest("POST", c.baseURL+"/api/v1/functions/"+idOrName+"/invoke", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var invokeResp InvokeResponse
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &invokeResp); err == nil && invokeResp.RequestID != "" {
			return &invokeResp, nil
		}
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return &invokeResp, nil
}

func (c *Client) InvokeFunctionAsync(idOrName string, payload json.RawMessage) (*AsyncInvokeResponse, error) {
	var resp AsyncInvokeResponse
	if err := c.do("POST", "/api/v1/functions/"+idOrName+"/async", payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) GetInvocation(id string) (*Invocation, error) {
	var inv Invocation
	if err := c.do("GET", "/api/v1/invocations/"+id, nil, &inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

func (c *Client) ListInvocations(functionID string, limit int) ([]Invocation, error) {
	var result struct {
		Invocations []Invocation `json:"invocations"`
	}
	if limit <= 0 {
		limit = 20
	}
	path := fmt.Sprintf("/api/v1/functions/%s/invocations?limit=%d", functionID, limit)
	if err := c.do("GET", path, nil, &result); err != nil {
		return nil, err
	}
	return result.Invocations, nil
}

// ====== 工作流操作方法 ====== 

func (c *Client) ListWorkflows() ([]Workflow, error) {
	var result struct {
		Workflows []Workflow `json:"workflows"`
	}
	if err := c.do("GET", "/api/v1/workflows", nil, &result); err != nil {
		return nil, err
	}
	return result.Workflows, nil
}

func (c *Client) GetWorkflow(id string) (*Workflow, error) {
	var wf Workflow
	if err := c.do("GET", "/api/v1/workflows/"+id, nil, &wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

func (c *Client) CreateWorkflow(req interface{}) (*Workflow, error) {
	var wf Workflow
	if err := c.do("POST", "/api/v1/workflows", req, &wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

func (c *Client) UpdateWorkflow(id string, req interface{}) (*Workflow, error) {
	var wf Workflow
	if err := c.do("PUT", "/api/v1/workflows/"+id, req, &wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

func (c *Client) DeleteWorkflow(id string) error {
	return c.do("DELETE", "/api/v1/workflows/"+id, nil, nil)
}

func (c *Client) StartWorkflowExecution(id string, input json.RawMessage) (*WorkflowExecution, error) {
	var exec WorkflowExecution
	req := map[string]interface{}{"input": input}
	if err := c.do("POST", "/api/v1/workflows/"+id+"/executions", req, &exec); err != nil {
		return nil, err
	}
	return &exec, nil
}

func (c *Client) ListExecutions(workflowID string) ([]WorkflowExecution, error) {
	var result struct {
		Executions []WorkflowExecution `json:"executions"`
	}
	if err := c.do("GET", "/api/v1/workflows/"+workflowID+"/executions", nil, &result); err != nil {
		return nil, err
	}
	return result.Executions, nil
}

// ====== 层（Layer）操作方法 ====== 

func (c *Client) ListLayers() ([]Layer, error) {
	var result struct {
		Layers []Layer `json:"layers"`
	}
	if err := c.do("GET", "/api/v1/layers", nil, &result); err != nil {
		return nil, err
	}
	return result.Layers, nil
}

func (c *Client) CreateLayer(req interface{}) (*Layer, error) {
	var layer Layer
	if err := c.do("POST", "/api/v1/layers", req, &layer); err != nil {
		return nil, err
	}
	return &layer, nil
}

func (c *Client) GetLayer(id string) (*Layer, error) {
	var result struct {
		Layer Layer `json:"layer"`
	}
	if err := c.do("GET", "/api/v1/layers/"+id, nil, &result); err != nil {
		return nil, err
	}
	return &result.Layer, nil
}

func (c *Client) DeleteLayer(id string) error {
	return c.do("DELETE", "/api/v1/layers/"+id, nil, nil)
}

// ====== 环境（Environment）操作方法 ====== 

func (c *Client) ListEnvironments() ([]Environment, error) {
	var result struct {
		Environments []Environment `json:"environments"`
	}
	if err := c.do("GET", "/api/v1/environments", nil, &result); err != nil {
		return nil, err
	}
	return result.Environments, nil
}

func (c *Client) CreateEnvironment(req interface{}) (*Environment, error) {
	var env Environment
	if err := c.do("POST", "/api/v1/environments", req, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

func (c *Client) DeleteEnvironment(id string) error {
	return c.do("DELETE", "/api/v1/environments/"+id, nil, nil)
}

// ApiKeyInfo 表示 API 密钥的基本信息。
type ApiKeyInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateApiKeyResponse 包含新生成的 API 密钥。
type CreateApiKeyResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	ApiKey string `json:"api_key"`
}

// ====== 统计与系统状态方法 ======

func (c *Client) GetStatus() (*SystemStatus, error) {
	var status SystemStatus
	if err := c.do("GET", "/health", nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

func (c *Client) GetStats() (*Stats, error) {
	var stats Stats
	if err := c.do("GET", "/api/v1/stats", nil, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

func (c *Client) GetQuotaUsage() (*QuotaUsage, error) {
	var usage QuotaUsage
	if err := c.do("GET", "/api/v1/quota", nil, &usage); err != nil {
		return nil, err
	}
	return &usage, nil
}

// ====== API Key 操作方法 ======

func (c *Client) ListApiKeys() ([]ApiKeyInfo, error) {
	var result struct {
		ApiKeys []ApiKeyInfo `json:"api_keys"`
	}
	// Note: The web UI uses /api/console/apikeys, but /api/v1/auth/apikeys is also available.
	// Based on router.go, console API is at /api/console/apikeys.
	if err := c.do("GET", "/api/console/apikeys", nil, &result); err != nil {
		return nil, err
	}
	return result.ApiKeys, nil
}

func (c *Client) CreateApiKey(name string) (*CreateApiKeyResponse, error) {
	req := map[string]string{"name": name}
	var resp CreateApiKeyResponse
	if err := c.do("POST", "/api/console/apikeys", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteApiKey(id string) error {
	return c.do("DELETE", "/api/console/apikeys/"+id, nil, nil)
}

// ====== 模板操作方法 ======
type Template struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description"`
	Runtime     string   `json:"runtime"`
	Handler     string   `json:"handler"`
	Code        string   `json:"code"`
	Category    string   `json:"category"`
	Popular     bool     `json:"popular"`
	Tags        []string `json:"tags"`
}

func (c *Client) ListTemplates() ([]Template, error) {
	var result struct {
		Templates []Template `json:"templates"`
	}
	if err := c.do("GET", "/api/v1/templates", nil, &result); err != nil {
		return nil, err
	}
	return result.Templates, nil
}

func (c *Client) GetTemplate(idOrName string) (*Template, error) {
	var t Template
	if err := c.do("GET", "/api/v1/templates/"+idOrName, nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}