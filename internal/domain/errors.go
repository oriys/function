// Package domain 定义了函数计算平台的核心领域模型。
package domain

import "errors"

// 领域错误定义
// 这些错误用于在应用程序的不同层之间传递业务逻辑相关的错误信息。

var (
	// ========== 函数相关错误 ==========

	// ErrFunctionNotFound 表示请求的函数不存在
	ErrFunctionNotFound = errors.New("function not found")
	// ErrFunctionExists 表示尝试创建的函数已经存在（名称冲突）
	ErrFunctionExists = errors.New("function already exists")
	// ErrInvalidFunctionName 表示函数名称无效（为空或格式不正确）
	ErrInvalidFunctionName = errors.New("invalid function name")
	// ErrInvalidRuntime 表示指定的运行时不受支持
	ErrInvalidRuntime = errors.New("invalid runtime")
	// ErrInvalidHandler 表示函数入口点配置无效
	ErrInvalidHandler = errors.New("invalid handler")
	// ErrInvalidCode 表示函数代码无效（为空）
	ErrInvalidCode = errors.New("invalid code")
	// ErrCodeSizeExceeded 表示代码大小超出限制
	ErrCodeSizeExceeded = errors.New("code size exceeds maximum limit")
	// ErrBinarySizeExceeded 表示二进制大小超出限制
	ErrBinarySizeExceeded = errors.New("binary size exceeds maximum limit")
	// ErrInvalidMemory 表示内存配置超出有效范围（必须在 128MB 到 3072MB 之间）
	ErrInvalidMemory = errors.New("invalid memory: must be between 128MB and 3072MB")
	// ErrInvalidTimeout 表示超时配置超出有效范围（必须在 1 到 300 秒之间）
	ErrInvalidTimeout = errors.New("invalid timeout: must be between 1 and 300 seconds")
	// ErrInvalidCronExpression 表示定时任务表达式无效
	ErrInvalidCronExpression = errors.New("invalid cron expression")

	// ========== 调用相关错误 ==========

	// ErrInvocationNotFound 表示请求的调用记录不存在
	ErrInvocationNotFound = errors.New("invocation not found")
	// ErrInvocationTimeout 表示函数调用执行超时
	ErrInvocationTimeout = errors.New("invocation timed out")
	// ErrInvocationFailed 表示函数调用执行失败
	ErrInvocationFailed = errors.New("invocation failed")
	// ErrInvocationCancelled 表示函数调用被取消
	ErrInvocationCancelled = errors.New("invocation cancelled")

	// ========== 虚拟机相关错误 ==========

	// ErrVMNotFound 表示请求的虚拟机不存在
	ErrVMNotFound = errors.New("vm not found")
	// ErrVMNotReady 表示虚拟机尚未准备就绪，无法执行任务
	ErrVMNotReady = errors.New("vm not ready")
	// ErrVMStartFailed 表示虚拟机启动失败
	ErrVMStartFailed = errors.New("vm start failed")
	// ErrVMCommunication 表示与虚拟机通信时发生错误
	ErrVMCommunication = errors.New("vm communication error")
	// ErrNoAvailableVM 表示当前没有可用的虚拟机
	ErrNoAvailableVM = errors.New("no available vm")
	// ErrVMPoolExhausted 表示虚拟机池资源耗尽
	ErrVMPoolExhausted = errors.New("vm pool exhausted")
	// ErrSnapshotNotFound 表示请求的快照不存在
	ErrSnapshotNotFound = errors.New("snapshot not found")
	// ErrSnapshotFailed 表示快照创建失败
	ErrSnapshotFailed = errors.New("snapshot creation failed")

	// ========== 网络相关错误 ==========

	// ErrNetworkSetupFailed 表示网络配置失败
	ErrNetworkSetupFailed = errors.New("network setup failed")
	// ErrTAPCreateFailed 表示 TAP 网络设备创建失败
	ErrTAPCreateFailed = errors.New("tap device creation failed")

	// ========== 存储相关错误 ==========

	// ErrStorageConnection 表示存储连接错误（如数据库连接失败）
	ErrStorageConnection = errors.New("storage connection error")
	// ErrStorageQuery 表示存储查询错误（如 SQL 查询失败）
	ErrStorageQuery = errors.New("storage query error")

	// ========== 工作流相关错误 ==========

	// ErrWorkflowNotFound 表示请求的工作流不存在
	ErrWorkflowNotFound = errors.New("workflow not found")
	// ErrWorkflowExists 表示尝试创建的工作流已经存在（名称冲突）
	ErrWorkflowExists = errors.New("workflow already exists")
	// ErrInvalidWorkflowName 表示工作流名称无效
	ErrInvalidWorkflowName = errors.New("invalid workflow name")
	// ErrInvalidWorkflowDefinition 表示工作流定义无效
	ErrInvalidWorkflowDefinition = errors.New("invalid workflow definition")
	// ErrInvalidWorkflowTimeout 表示工作流超时配置无效
	ErrInvalidWorkflowTimeout = errors.New("invalid workflow timeout: must be between 1 and 86400 seconds")
	// ErrWorkflowInactive 表示工作流处于非活跃状态
	ErrWorkflowInactive = errors.New("workflow is inactive")

	// ========== 工作流执行相关错误 ==========

	// ErrExecutionNotFound 表示请求的执行不存在
	ErrExecutionNotFound = errors.New("execution not found")
	// ErrExecutionAlreadyComplete 表示执行已经完成
	ErrExecutionAlreadyComplete = errors.New("execution already complete")
	// ErrExecutionTimeout 表示执行超时
	ErrExecutionTimeout = errors.New("execution timed out")
	// ErrExecutionFailed 表示执行失败
	ErrExecutionFailed = errors.New("execution failed")
	// ErrStateFailed 表示状态执行失败
	ErrStateFailed = errors.New("state execution failed")
	// ErrNoChoiceMatched 表示没有匹配的 Choice 条件
	ErrNoChoiceMatched = errors.New("no choice matched")
	// ErrInvalidJSONPath 表示无效的 JSONPath 表达式
	ErrInvalidJSONPath = errors.New("invalid JSONPath expression")

	// ========== 模板相关错误 ==========

	// ErrTemplateNotFound 表示请求的模板不存在
	ErrTemplateNotFound = errors.New("template not found")
	// ErrTemplateExists 表示尝试创建的模板已经存在（名称冲突）
	ErrTemplateExists = errors.New("template already exists")
	// ErrInvalidTemplateName 表示模板名称无效
	ErrInvalidTemplateName = errors.New("invalid template name")
	// ErrInvalidTemplateDisplayName 表示模板显示名称无效
	ErrInvalidTemplateDisplayName = errors.New("invalid template display name")
	// ErrInvalidTemplateCategory 表示模板分类无效
	ErrInvalidTemplateCategory = errors.New("invalid template category")
	// ErrInvalidTemplateID 表示模板 ID 无效
	ErrInvalidTemplateID = errors.New("invalid template id")

	// ========== 版本管理相关错误 ==========

	// ErrVersionNotFound 表示请求的版本不存在
	ErrVersionNotFound = errors.New("version not found")
	// ErrVersionInUse 表示版本正在被别名使用，无法删除
	ErrVersionInUse = errors.New("version is in use by an alias")
	// ErrInvalidVersion 表示版本号无效
	ErrInvalidVersion = errors.New("invalid version number")

	// ========== 别名管理相关错误 ==========

	// ErrAliasNotFound 表示请求的别名不存在
	ErrAliasNotFound = errors.New("alias not found")
	// ErrAliasExists 表示别名已存在
	ErrAliasExists = errors.New("alias already exists")
	// ErrInvalidWeights 表示流量权重配置无效（权重总和必须为100）
	ErrInvalidWeights = errors.New("weights must sum to 100")
	// ErrCannotDeleteLatest 表示无法删除 latest 别名
	ErrCannotDeleteLatest = errors.New("cannot delete 'latest' alias")
)
