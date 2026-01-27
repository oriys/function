// Package workflow 默认工作流定义
package workflow

import (
	"encoding/json"

	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/domain"
)

// DefaultWorkflowWithFunctions 带函数ID的工作流定义
type DefaultWorkflowWithFunctions struct {
	Name        string
	Description string
	TimeoutSec  int
	// BuildDefinition 构建工作流定义，接收函数ID映射
	BuildDefinition func(functionIDs map[string]string) domain.WorkflowDefinition
}

// DefaultWorkflows 返回默认工作流定义列表
func DefaultWorkflows() []DefaultWorkflowWithFunctions {
	return []DefaultWorkflowWithFunctions{
		helloWorldWorkflow(),
		dataProcessingWorkflow(),
		parallelProcessingWorkflow(),
		errorHandlingWorkflow(),
		orderProcessingWorkflow(),
	}
}

// helloWorldWorkflow 简单的 Hello World 工作流
func helloWorldWorkflow() DefaultWorkflowWithFunctions {
	return DefaultWorkflowWithFunctions{
		Name:        "hello-world",
		Description: "Hello World 示例：调用 echo 函数回显输入",
		TimeoutSec:  300,
		BuildDefinition: func(functionIDs map[string]string) domain.WorkflowDefinition {
			echoID := functionIDs["echo"]
			return domain.WorkflowDefinition{
				StartAt: "PrepareInput",
				States: map[string]domain.State{
					"PrepareInput": {
						Type:    domain.StateTypePass,
						Comment: "准备输入数据",
						Result:  json.RawMessage(`{"message": "Hello from Workflow!", "timestamp": "2024-01-01T00:00:00Z"}`),
						Next:    "CallEcho",
					},
					"CallEcho": {
						Type:       domain.StateTypeTask,
						Comment:    "调用 Echo 函数",
						FunctionID: echoID,
						TimeoutSec: 30,
						Retry: &domain.RetryPolicy{
							ErrorEquals:     []string{"States.Timeout", "States.TaskFailed"},
							MaxAttempts:     2,
							IntervalSeconds: 1,
							BackoffRate:     2.0,
						},
						Next: "Done",
					},
					"Done": {
						Type:    domain.StateTypeSucceed,
						Comment: "工作流成功完成",
					},
				},
			}
		},
	}
}

// dataProcessingWorkflow 数据处理工作流
func dataProcessingWorkflow() DefaultWorkflowWithFunctions {
	return DefaultWorkflowWithFunctions{
		Name:        "data-processing",
		Description: "数据处理流程：验证 -> 转换 -> 检查条件",
		TimeoutSec:  600,
		BuildDefinition: func(functionIDs map[string]string) domain.WorkflowDefinition {
			validateID := functionIDs["validate-data"]
			transformID := functionIDs["transform-data"]
			checkID := functionIDs["check-condition"]

			return domain.WorkflowDefinition{
				StartAt: "ValidateData",
				States: map[string]domain.State{
					"ValidateData": {
						Type:       domain.StateTypeTask,
						Comment:    "验证输入数据",
						FunctionID: validateID,
						TimeoutSec: 30,
						Catch: []domain.CatchConfig{
							{
								ErrorEquals: []string{"States.ALL"},
								Next:        "HandleValidationError",
							},
						},
						Next: "CheckValidation",
					},
					"CheckValidation": {
						Type:    domain.StateTypeChoice,
						Comment: "检查验证结果",
						Choices: []domain.ChoiceRule{
							{
								Variable:      "$.body.valid",
								BooleanEquals: boolPtr(true),
								Next:          "TransformData",
							},
						},
						Default: "HandleValidationError",
					},
					"TransformData": {
						Type:       domain.StateTypeTask,
						Comment:    "转换数据格式",
						FunctionID: transformID,
						TimeoutSec: 30,
						Next:       "CheckResult",
					},
					"CheckResult": {
						Type:       domain.StateTypeTask,
						Comment:    "检查处理结果",
						FunctionID: checkID,
						TimeoutSec: 30,
						Next:       "Complete",
					},
					"HandleValidationError": {
						Type:  domain.StateTypeFail,
						Error: "ValidationError",
						Cause: "数据验证失败",
					},
					"Complete": {
						Type:    domain.StateTypeSucceed,
						Comment: "数据处理完成",
					},
				},
			}
		},
	}
}

// parallelProcessingWorkflow 并行处理工作流
func parallelProcessingWorkflow() DefaultWorkflowWithFunctions {
	return DefaultWorkflowWithFunctions{
		Name:        "parallel-processing",
		Description: "并行处理示例：同时执行多个任务，然后聚合结果",
		TimeoutSec:  900,
		BuildDefinition: func(functionIDs map[string]string) domain.WorkflowDefinition {
			echoID := functionIDs["echo"]
			aggregateID := functionIDs["aggregate-results"]

			return domain.WorkflowDefinition{
				StartAt: "PrepareData",
				States: map[string]domain.State{
					"PrepareData": {
						Type:    domain.StateTypePass,
						Comment: "准备并行处理数据",
						Result:  json.RawMessage(`{"items": ["task1", "task2", "task3"], "batch_id": "batch-001"}`),
						Next:    "ParallelProcess",
					},
					"ParallelProcess": {
						Type:    domain.StateTypeParallel,
						Comment: "并行执行三个处理分支",
						Branches: []domain.Branch{
							{
								StartAt: "Branch1Task",
								States: map[string]domain.State{
									"Branch1Task": {
										Type:       domain.StateTypeTask,
										Comment:    "分支1：处理任务A",
										FunctionID: echoID,
										TimeoutSec: 30,
										End:        true,
									},
								},
							},
							{
								StartAt: "Branch2Task",
								States: map[string]domain.State{
									"Branch2Task": {
										Type:       domain.StateTypeTask,
										Comment:    "分支2：处理任务B",
										FunctionID: echoID,
										TimeoutSec: 30,
										End:        true,
									},
								},
							},
							{
								StartAt: "Branch3Task",
								States: map[string]domain.State{
									"Branch3Task": {
										Type:       domain.StateTypeTask,
										Comment:    "分支3：处理任务C",
										FunctionID: echoID,
										TimeoutSec: 30,
										End:        true,
									},
								},
							},
						},
						Next: "AggregateResults",
					},
					"AggregateResults": {
						Type:       domain.StateTypeTask,
						Comment:    "聚合并行处理结果",
						FunctionID: aggregateID,
						TimeoutSec: 30,
						Next:       "Complete",
					},
					"Complete": {
						Type:    domain.StateTypeSucceed,
						Comment: "并行处理完成",
					},
				},
			}
		},
	}
}

// errorHandlingWorkflow 错误处理工作流
func errorHandlingWorkflow() DefaultWorkflowWithFunctions {
	return DefaultWorkflowWithFunctions{
		Name:        "error-handling",
		Description: "错误处理示例：演示重试、错误捕获和恢复",
		TimeoutSec:  600,
		BuildDefinition: func(functionIDs map[string]string) domain.WorkflowDefinition {
			validateID := functionIDs["validate-data"]
			handleErrorID := functionIDs["handle-error"]
			notifyID := functionIDs["send-notification"]

			return domain.WorkflowDefinition{
				StartAt: "AttemptTask",
				States: map[string]domain.State{
					"AttemptTask": {
						Type:       domain.StateTypeTask,
						Comment:    "尝试执行任务（可能失败）",
						FunctionID: validateID,
						TimeoutSec: 30,
						Retry: &domain.RetryPolicy{
							ErrorEquals:     []string{"States.Timeout"},
							MaxAttempts:     3,
							IntervalSeconds: 2,
							BackoffRate:     2.0,
						},
						Catch: []domain.CatchConfig{
							{
								ErrorEquals: []string{"States.ALL"},
								Next:        "HandleError",
								ResultPath:  "$.error_info",
							},
						},
						Next: "CheckSuccess",
					},
					"CheckSuccess": {
						Type:    domain.StateTypeChoice,
						Comment: "检查任务是否成功",
						Choices: []domain.ChoiceRule{
							{
								Variable:      "$.body.valid",
								BooleanEquals: boolPtr(true),
								Next:          "ProcessSuccess",
							},
						},
						Default: "HandleError",
					},
					"ProcessSuccess": {
						Type:    domain.StateTypePass,
						Comment: "处理成功结果",
						Result:  json.RawMessage(`{"status": "success", "message": "Task completed successfully"}`),
						Next:    "Complete",
					},
					"HandleError": {
						Type:       domain.StateTypeTask,
						Comment:    "处理错误",
						FunctionID: handleErrorID,
						TimeoutSec: 30,
						Next:       "NotifyAdmin",
					},
					"NotifyAdmin": {
						Type:       domain.StateTypeTask,
						Comment:    "通知管理员",
						FunctionID: notifyID,
						TimeoutSec: 30,
						Next:       "FailGracefully",
					},
					"FailGracefully": {
						Type:  domain.StateTypeFail,
						Error: "TaskFailed",
						Cause: "任务执行失败，已通知管理员",
					},
					"Complete": {
						Type:    domain.StateTypeSucceed,
						Comment: "工作流成功完成",
					},
				},
			}
		},
	}
}

// orderProcessingWorkflow 订单处理工作流
func orderProcessingWorkflow() DefaultWorkflowWithFunctions {
	return DefaultWorkflowWithFunctions{
		Name:        "order-processing",
		Description: "订单处理流程：接收订单 -> 验证 -> 处理 -> 通知",
		TimeoutSec:  900,
		BuildDefinition: func(functionIDs map[string]string) domain.WorkflowDefinition {
			validateID := functionIDs["validate-data"]
			processOrderID := functionIDs["process-order"]
			notifyID := functionIDs["send-notification"]
			handleErrorID := functionIDs["handle-error"]

			return domain.WorkflowDefinition{
				StartAt: "ReceiveOrder",
				States: map[string]domain.State{
					"ReceiveOrder": {
						Type:    domain.StateTypePass,
						Comment: "接收订单",
						Result: json.RawMessage(`{
							"order_id": "ORD-DEMO-001",
							"type": "express",
							"customer": {"id": "CUST-001", "name": "Demo Customer", "email": "demo@example.com"},
							"items": [
								{"name": "Product A", "price": 99.99, "quantity": 2},
								{"name": "Product B", "price": 49.99, "quantity": 1}
							]
						}`),
						Next: "ValidateOrder",
					},
					"ValidateOrder": {
						Type:       domain.StateTypeTask,
						Comment:    "验证订单信息",
						FunctionID: validateID,
						TimeoutSec: 30,
						Catch: []domain.CatchConfig{
							{
								ErrorEquals: []string{"States.ALL"},
								Next:        "HandleOrderError",
							},
						},
						Next: "CheckOrderType",
					},
					"CheckOrderType": {
						Type:    domain.StateTypeChoice,
						Comment: "根据订单类型路由",
						Choices: []domain.ChoiceRule{
							{
								Variable:     "$.type",
								StringEquals: "express",
								Next:         "ProcessExpressOrder",
							},
							{
								Variable:     "$.type",
								StringEquals: "vip",
								Next:         "ProcessVIPOrder",
							},
						},
						Default: "ProcessStandardOrder",
					},
					"ProcessExpressOrder": {
						Type:    domain.StateTypePass,
						Comment: "标记为加急订单",
						Result:  json.RawMessage(`{"priority": "high", "processing_mode": "express"}`),
						Next:    "ProcessOrder",
					},
					"ProcessVIPOrder": {
						Type:    domain.StateTypePass,
						Comment: "标记为VIP订单",
						Result:  json.RawMessage(`{"priority": "vip", "processing_mode": "vip", "discount": 0.1}`),
						Next:    "ProcessOrder",
					},
					"ProcessStandardOrder": {
						Type:    domain.StateTypePass,
						Comment: "标记为标准订单",
						Result:  json.RawMessage(`{"priority": "normal", "processing_mode": "standard"}`),
						Next:    "ProcessOrder",
					},
					"ProcessOrder": {
						Type:       domain.StateTypeTask,
						Comment:    "处理订单",
						FunctionID: processOrderID,
						TimeoutSec: 60,
						Retry: &domain.RetryPolicy{
							ErrorEquals:     []string{"States.Timeout", "States.TaskFailed"},
							MaxAttempts:     2,
							IntervalSeconds: 5,
							BackoffRate:     2.0,
						},
						Catch: []domain.CatchConfig{
							{
								ErrorEquals: []string{"States.ALL"},
								Next:        "HandleOrderError",
							},
						},
						Next: "WaitForProcessing",
					},
					"WaitForProcessing": {
						Type:    domain.StateTypeWait,
						Comment: "等待处理完成（模拟）",
						Seconds: 2,
						Next:    "SendConfirmation",
					},
					"SendConfirmation": {
						Type:       domain.StateTypeTask,
						Comment:    "发送订单确认通知",
						FunctionID: notifyID,
						TimeoutSec: 30,
						Next:       "OrderComplete",
					},
					"HandleOrderError": {
						Type:       domain.StateTypeTask,
						Comment:    "处理订单错误",
						FunctionID: handleErrorID,
						TimeoutSec: 30,
						Next:       "NotifyError",
					},
					"NotifyError": {
						Type:       domain.StateTypeTask,
						Comment:    "发送错误通知",
						FunctionID: notifyID,
						TimeoutSec: 30,
						Next:       "OrderFailed",
					},
					"OrderFailed": {
						Type:  domain.StateTypeFail,
						Error: "OrderProcessingFailed",
						Cause: "订单处理失败",
					},
					"OrderComplete": {
						Type:    domain.StateTypeSucceed,
						Comment: "订单处理完成",
					},
				},
			}
		},
	}
}

// boolPtr 返回 bool 指针的辅助函数
func boolPtr(v bool) *bool {
	return &v
}

// float64Ptr 返回 float64 指针的辅助函数
func float64Ptr(v float64) *float64 {
	return &v
}

// SeedDefaultWorkflows 在数据库中创建默认工作流（如果不存在）
func (e *Engine) SeedDefaultWorkflows() error {
	// 先创建默认函数
	functionIDs, err := e.SeedDefaultFunctions()
	if err != nil {
		e.logger.WithError(err).Warn("Failed to seed default functions")
	}

	// 创建默认工作流
	defaults := DefaultWorkflows()

	for _, def := range defaults {
		// 检查工作流是否已存在
		existing, err := e.store.GetWorkflowByName(def.Name)
		if err == nil && existing != nil {
			e.logger.WithField("workflow", def.Name).Debug("Default workflow already exists, skipping")
			continue
		}

		// 构建工作流定义
		definition := def.BuildDefinition(functionIDs)

		// 创建工作流
		workflow := &domain.Workflow{
			ID:          uuid.New().String(),
			Name:        def.Name,
			Description: def.Description,
			Version:     1,
			Status:      domain.WorkflowStatusActive,
			Definition:  definition,
			TimeoutSec:  def.TimeoutSec,
		}

		if err := e.store.CreateWorkflow(workflow); err != nil {
			e.logger.WithError(err).WithField("workflow", def.Name).Error("Failed to create default workflow")
			continue
		}

		e.logger.WithField("workflow", def.Name).Info("Created default workflow")
	}

	return nil
}
