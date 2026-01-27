// Package api 提供了函数即服务(FaaS)平台的HTTP API处理程序。
// 该文件包含API处理器的单元测试，使用模拟对象来隔离测试环境。
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oriys/nimbus/internal/domain"
)

// MockStore 是用于测试的模拟存储实现。
// 它使用内存中的map来存储函数和调用记录，无需真实数据库。
//
// 字段说明：
//   - functions: 存储函数对象的map，key为函数ID
//   - invocations: 存储调用记录的map，key为调用ID
type MockStore struct {
	functions   map[string]*domain.Function   // 函数存储映射
	invocations map[string]*domain.Invocation // 调用记录存储映射
}

// NewMockStore 创建并返回一个新的MockStore实例。
//
// 返回值：
//   - *MockStore: 初始化完成的模拟存储实例
func NewMockStore() *MockStore {
	return &MockStore{
		functions:   make(map[string]*domain.Function),
		invocations: make(map[string]*domain.Invocation),
	}
}

// CreateFunction 在模拟存储中创建函数。
//
// 参数：
//   - fn: 要创建的函数对象
//
// 返回值：
//   - error: 始终返回nil（模拟实现不会失败）
func (m *MockStore) CreateFunction(fn *domain.Function) error {
	m.functions[fn.ID] = fn
	return nil
}

// GetFunctionByID 通过ID获取函数。
//
// 参数：
//   - id: 函数的唯一标识符
//
// 返回值：
//   - *domain.Function: 找到的函数对象
//   - error: 如果未找到返回ErrFunctionNotFound
func (m *MockStore) GetFunctionByID(id string) (*domain.Function, error) {
	if fn, ok := m.functions[id]; ok {
		return fn, nil
	}
	return nil, domain.ErrFunctionNotFound
}

// GetFunctionByName 通过名称获取函数。
// 遍历所有函数查找匹配的名称。
//
// 参数：
//   - name: 函数名称
//
// 返回值：
//   - *domain.Function: 找到的函数对象
//   - error: 如果未找到返回ErrFunctionNotFound
func (m *MockStore) GetFunctionByName(name string) (*domain.Function, error) {
	// 遍历所有函数，查找名称匹配的函数
	for _, fn := range m.functions {
		if fn.Name == name {
			return fn, nil
		}
	}
	return nil, domain.ErrFunctionNotFound
}

// ListFunctions 获取所有函数的列表。
// 模拟实现忽略分页参数，返回所有函数。
//
// 参数：
//   - offset: 偏移量（模拟实现中忽略）
//   - limit: 每页数量（模拟实现中忽略）
//
// 返回值：
//   - []*domain.Function: 函数列表
//   - int: 函数总数
//   - error: 始终返回nil
func (m *MockStore) ListFunctions(offset, limit int) ([]*domain.Function, int, error) {
	var fns []*domain.Function
	// 收集所有函数
	for _, fn := range m.functions {
		fns = append(fns, fn)
	}
	return fns, len(fns), nil
}

// UpdateFunction 更新模拟存储中的函数。
//
// 参数：
//   - fn: 要更新的函数对象
//
// 返回值：
//   - error: 如果函数不存在返回ErrFunctionNotFound
func (m *MockStore) UpdateFunction(fn *domain.Function) error {
	// 检查函数是否存在
	if _, ok := m.functions[fn.ID]; !ok {
		return domain.ErrFunctionNotFound
	}
	m.functions[fn.ID] = fn
	return nil
}

// DeleteFunction 从模拟存储中删除函数。
//
// 参数：
//   - id: 要删除的函数ID
//
// 返回值：
//   - error: 如果函数不存在返回ErrFunctionNotFound
func (m *MockStore) DeleteFunction(id string) error {
	// 检查函数是否存在
	if _, ok := m.functions[id]; !ok {
		return domain.ErrFunctionNotFound
	}
	delete(m.functions, id)
	return nil
}

// Ping 模拟数据库连接检查。
//
// 返回值：
//   - error: 始终返回nil（模拟实现总是可用）
func (m *MockStore) Ping() error {
	return nil
}

// CountFunctions 返回存储的函数数量。
//
// 返回值：
//   - int: 函数总数
//   - error: 始终返回nil
func (m *MockStore) CountFunctions() (int, error) {
	return len(m.functions), nil
}

// CountInvocations 返回存储的调用记录数量。
//
// 返回值：
//   - int: 调用记录总数
//   - error: 始终返回nil
func (m *MockStore) CountInvocations() (int, error) {
	return len(m.invocations), nil
}

// GetInvocationByID 通过ID获取调用记录。
//
// 参数：
//   - id: 调用记录的唯一标识符
//
// 返回值：
//   - *domain.Invocation: 找到的调用记录
//   - error: 如果未找到返回ErrInvocationNotFound
func (m *MockStore) GetInvocationByID(id string) (*domain.Invocation, error) {
	if inv, ok := m.invocations[id]; ok {
		return inv, nil
	}
	return nil, domain.ErrInvocationNotFound
}

// ListInvocationsByFunction 获取指定函数的调用记录列表。
// 模拟实现忽略分页参数。
//
// 参数：
//   - functionID: 函数的唯一标识符
//   - offset: 偏移量（模拟实现中忽略）
//   - limit: 每页数量（模拟实现中忽略）
//
// 返回值：
//   - []*domain.Invocation: 调用记录列表
//   - int: 记录总数
//   - error: 始终返回nil
func (m *MockStore) ListInvocationsByFunction(functionID string, offset, limit int) ([]*domain.Invocation, int, error) {
	var invs []*domain.Invocation
	// 筛选出指定函数的调用记录
	for _, inv := range m.invocations {
		if inv.FunctionID == functionID {
			invs = append(invs, inv)
		}
	}
	return invs, len(invs), nil
}

// MockScheduler 是用于测试的模拟调度器实现。
// 它返回预设的响应，不执行真实的函数调用。
type MockScheduler struct{}

// Invoke 模拟同步函数调用。
// 返回预设的成功响应，不执行实际函数。
//
// 参数：
//   - req: 调用请求
//
// 返回值：
//   - *domain.InvokeResponse: 预设的成功响应
//   - error: 始终返回nil
func (m *MockScheduler) Invoke(req *domain.InvokeRequest) (*domain.InvokeResponse, error) {
	return &domain.InvokeResponse{
		RequestID:  "test-inv-id",
		StatusCode: 200,
		Body:       json.RawMessage(`{"result": "success"}`),
	}, nil
}

// InvokeAsync 模拟异步函数调用。
// 返回预设的请求ID，不执行实际函数。
//
// 参数：
//   - req: 调用请求
//
// 返回值：
//   - string: 预设的请求ID "test-request-id"
//   - error: 始终返回nil
func (m *MockScheduler) InvokeAsync(req *domain.InvokeRequest) (string, error) {
	return "test-request-id", nil
}

// TestHealth 测试健康检查端点。
//
// 测试内容：
//   - 验证GET /health请求返回200状态码
//   - 验证响应体包含{"status": "healthy"}
func TestHealth(t *testing.T) {
	// 创建测试请求
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	// 创建处理器并执行请求
	h := &Handler{}
	h.Health(w, req)

	// 验证HTTP状态码
	if w.Code != http.StatusOK {
		t.Errorf("Health() status = %d, want %d", w.Code, http.StatusOK)
	}

	// 验证响应体内容
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "healthy" {
		t.Errorf("Health() status = %s, want healthy", resp["status"])
	}
}

// TestLive 测试存活探针端点。
//
// 测试内容：
//   - 验证GET /health/live请求返回200状态码
//   - 验证响应体包含{"status": "alive"}
func TestLive(t *testing.T) {
	// 创建测试请求
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	w := httptest.NewRecorder()

	// 创建处理器并执行请求
	h := &Handler{}
	h.Live(w, req)

	// 验证HTTP状态码
	if w.Code != http.StatusOK {
		t.Errorf("Live() status = %d, want %d", w.Code, http.StatusOK)
	}

	// 验证响应体内容
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "alive" {
		t.Errorf("Live() status = %s, want alive", resp["status"])
	}
}
