// Package domain 定义了函数计算平台的核心领域模型。
// 本文件定义了函数模板相关的实体和请求/响应结构体。
package domain

import (
	"encoding/json"
	"time"
)

// TemplateCategory 表示模板分类类型
type TemplateCategory string

// 模板分类常量定义
const (
	// TemplateCategoryWebAPI Web API 处理模板
	TemplateCategoryWebAPI TemplateCategory = "web-api"
	// TemplateCategoryDataProcessing 数据处理模板
	TemplateCategoryDataProcessing TemplateCategory = "data-processing"
	// TemplateCategoryScheduled 定时任务模板
	TemplateCategoryScheduled TemplateCategory = "scheduled"
	// TemplateCategoryWebhook Webhook 处理模板
	TemplateCategoryWebhook TemplateCategory = "webhook"
	// TemplateCategoryStarter 入门示例模板
	TemplateCategoryStarter TemplateCategory = "starter"
)

// IsValid 检查模板分类是否有效
func (c TemplateCategory) IsValid() bool {
	switch c {
	case TemplateCategoryWebAPI, TemplateCategoryDataProcessing,
		TemplateCategoryScheduled, TemplateCategoryWebhook, TemplateCategoryStarter:
		return true
	default:
		return false
	}
}

// TemplateVariableType 表示模板变量类型
type TemplateVariableType string

// 模板变量类型常量定义
const (
	// TemplateVariableTypeString 字符串类型
	TemplateVariableTypeString TemplateVariableType = "string"
	// TemplateVariableTypeNumber 数字类型
	TemplateVariableTypeNumber TemplateVariableType = "number"
	// TemplateVariableTypeBoolean 布尔类型
	TemplateVariableTypeBoolean TemplateVariableType = "boolean"
)

// TemplateVariable 表示模板中的可替换变量
type TemplateVariable struct {
	// Name 变量名 (如 "TABLE_NAME")
	Name string `json:"name"`
	// Label 显示标签
	Label string `json:"label"`
	// Description 变量描述
	Description string `json:"description,omitempty"`
	// Type 变量类型 (string, number, boolean)
	Type TemplateVariableType `json:"type"`
	// Required 是否必填
	Required bool `json:"required"`
	// Default 默认值
	Default string `json:"default,omitempty"`
}

// Template 表示一个函数模板实体。
// 模板用于快速创建预配置的函数，包含代码骨架和配置。
type Template struct {
	// ID 是模板的唯一标识符
	ID string `json:"id"`
	// Name 是模板的唯一标识名称
	Name string `json:"name"`
	// DisplayName 是模板的显示名称
	DisplayName string `json:"display_name"`
	// Description 是模板的描述信息
	Description string `json:"description,omitempty"`
	// Category 是模板的分类
	Category TemplateCategory `json:"category"`
	// Runtime 是模板的运行时环境
	Runtime Runtime `json:"runtime"`
	// Handler 是默认的函数入口点
	Handler string `json:"handler"`
	// Code 是模板代码内容
	Code string `json:"code"`
	// Variables 是模板中的可替换变量列表
	Variables []TemplateVariable `json:"variables,omitempty"`
	// DefaultMemory 是默认内存配置（MB）
	DefaultMemory int `json:"default_memory"`
	// DefaultTimeout 是默认超时配置（秒）
	DefaultTimeout int `json:"default_timeout"`
	// Tags 是模板标签列表
	Tags []string `json:"tags,omitempty"`
	// Icon 是模板图标名称
	Icon string `json:"icon,omitempty"`
	// Popular 表示是否为热门模板
	Popular bool `json:"popular"`
	// CreatedAt 是模板创建时间
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 是模板最后更新时间
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateTemplateRequest 表示创建模板的请求结构体
type CreateTemplateRequest struct {
	// Name 是模板的唯一标识名称，必填
	Name string `json:"name" validate:"required,min=1,max=64"`
	// DisplayName 是模板的显示名称，必填
	DisplayName string `json:"display_name" validate:"required,min=1,max=128"`
	// Description 是模板描述，可选
	Description string `json:"description,omitempty"`
	// Category 是模板分类，必填
	Category TemplateCategory `json:"category" validate:"required"`
	// Runtime 是运行时类型，必填
	Runtime Runtime `json:"runtime" validate:"required"`
	// Handler 是函数入口点，必填
	Handler string `json:"handler" validate:"required"`
	// Code 是模板代码，必填
	Code string `json:"code" validate:"required"`
	// Variables 是模板变量列表，可选
	Variables []TemplateVariable `json:"variables,omitempty"`
	// DefaultMemory 是默认内存配置（MB），可选，默认 256
	DefaultMemory int `json:"default_memory,omitempty"`
	// DefaultTimeout 是默认超时配置（秒），可选，默认 30
	DefaultTimeout int `json:"default_timeout,omitempty"`
	// Tags 是模板标签，可选
	Tags []string `json:"tags,omitempty"`
	// Icon 是模板图标名称，可选
	Icon string `json:"icon,omitempty"`
	// Popular 是否为热门模板，可选
	Popular bool `json:"popular,omitempty"`
}

// Validate 验证创建模板请求的参数是否有效
func (r *CreateTemplateRequest) Validate() error {
	if r.Name == "" {
		return ErrInvalidTemplateName
	}
	if r.DisplayName == "" {
		return ErrInvalidTemplateDisplayName
	}
	if !r.Category.IsValid() {
		return ErrInvalidTemplateCategory
	}
	if !r.Runtime.IsValid() {
		return ErrInvalidRuntime
	}
	if r.Handler == "" {
		return ErrInvalidHandler
	}
	if r.Code == "" {
		return ErrInvalidCode
	}
	// 设置默认值
	if r.DefaultMemory == 0 {
		r.DefaultMemory = 256
	}
	if r.DefaultTimeout == 0 {
		r.DefaultTimeout = 30
	}
	return nil
}

// UpdateTemplateRequest 表示更新模板的请求结构体
type UpdateTemplateRequest struct {
	// DisplayName 是更新后的显示名称
	DisplayName *string `json:"display_name,omitempty"`
	// Description 是更新后的描述
	Description *string `json:"description,omitempty"`
	// Category 是更新后的分类
	Category *TemplateCategory `json:"category,omitempty"`
	// Handler 是更新后的函数入口点
	Handler *string `json:"handler,omitempty"`
	// Code 是更新后的模板代码
	Code *string `json:"code,omitempty"`
	// Variables 是更新后的变量列表
	Variables *[]TemplateVariable `json:"variables,omitempty"`
	// DefaultMemory 是更新后的默认内存
	DefaultMemory *int `json:"default_memory,omitempty"`
	// DefaultTimeout 是更新后的默认超时
	DefaultTimeout *int `json:"default_timeout,omitempty"`
	// Tags 是更新后的标签
	Tags *[]string `json:"tags,omitempty"`
	// Icon 是更新后的图标
	Icon *string `json:"icon,omitempty"`
	// Popular 是更新后的热门状态
	Popular *bool `json:"popular,omitempty"`
}

// CreateFunctionFromTemplateRequest 表示从模板创建函数的请求
type CreateFunctionFromTemplateRequest struct {
	// TemplateID 是模板 ID，必填
	TemplateID string `json:"template_id" validate:"required"`
	// FunctionName 是新函数的名称，必填
	FunctionName string `json:"function_name" validate:"required,min=1,max=64"`
	// Description 是函数描述，可选
	Description string `json:"description,omitempty"`
	// Variables 是模板变量值映射，可选
	Variables map[string]string `json:"variables,omitempty"`
	// EnvVars 是环境变量配置，可选
	EnvVars map[string]string `json:"env_vars,omitempty"`
	// MemoryMB 是内存配置（MB），可选，使用模板默认值
	MemoryMB int `json:"memory_mb,omitempty"`
	// TimeoutSec 是超时配置（秒），可选，使用模板默认值
	TimeoutSec int `json:"timeout_sec,omitempty"`
}

// Validate 验证从模板创建函数请求的参数
func (r *CreateFunctionFromTemplateRequest) Validate() error {
	if r.TemplateID == "" {
		return ErrInvalidTemplateID
	}
	if r.FunctionName == "" {
		return ErrInvalidFunctionName
	}
	return nil
}

// TemplateRepository 定义了模板存储的接口
type TemplateRepository interface {
	// Create 创建一个新的模板记录
	Create(template *Template) error
	// GetByID 根据 ID 获取模板
	GetByID(id string) (*Template, error)
	// GetByName 根据名称获取模板
	GetByName(name string) (*Template, error)
	// List 分页获取模板列表
	List(offset, limit int, category string) ([]*Template, int, error)
	// Update 更新模板信息
	Update(template *Template) error
	// Delete 根据 ID 删除模板
	Delete(id string) error
}

// TemplateFilter 用于模板列表的筛选条件
type TemplateFilter struct {
	// Category 模板分类（精确匹配）
	Category TemplateCategory `json:"category,omitempty"`
	// Runtime 运行时类型（精确匹配）
	Runtime Runtime `json:"runtime,omitempty"`
	// Popular 是否热门
	Popular *bool `json:"popular,omitempty"`
	// Search 搜索关键词（名称或描述模糊匹配）
	Search string `json:"search,omitempty"`
}

// MarshalVariables 将 Variables 转换为 JSON 字节
func (t *Template) MarshalVariables() ([]byte, error) {
	if t.Variables == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(t.Variables)
}

// UnmarshalVariables 从 JSON 字节解析 Variables
func (t *Template) UnmarshalVariables(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		t.Variables = nil
		return nil
	}
	return json.Unmarshal(data, &t.Variables)
}
