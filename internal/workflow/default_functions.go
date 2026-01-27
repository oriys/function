// Package workflow 默认函数定义
package workflow

import (
	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/domain"
)

// DefaultFunctionDefinition 默认函数定义
type DefaultFunctionDefinition struct {
	Name        string
	Description string
	Runtime     domain.Runtime
	Handler     string
	Code        string
	MemoryMB    int
	TimeoutSec  int
	Tags        []string
}

// DefaultFunctions 返回默认函数定义列表
func DefaultFunctions() []DefaultFunctionDefinition {
	return []DefaultFunctionDefinition{
		// 1. Echo 函数 - 回显输入
		{
			Name:        "echo",
			Description: "回显输入数据，用于测试和调试",
			Runtime:     domain.RuntimePython311,
			Handler:     "main.handler",
			MemoryMB:    128,
			TimeoutSec:  30,
			Tags:        []string{"utility", "demo"},
			Code: `import json

def handler(event, context):
    """Echo handler - returns the input as output"""
    return {
        "statusCode": 200,
        "body": {
            "message": "Echo successful",
            "input": event,
            "request_id": context.get("request_id", "unknown")
        }
    }
`,
		},

		// 2. 数据验证函数
		{
			Name:        "validate-data",
			Description: "验证输入数据格式和字段",
			Runtime:     domain.RuntimePython311,
			Handler:     "main.handler",
			MemoryMB:    128,
			TimeoutSec:  30,
			Tags:        []string{"validation", "workflow"},
			Code: `import json

def handler(event, context):
    """Validate data handler"""
    errors = []

    # Check required fields
    required_fields = event.get("required_fields", ["id", "name"])
    data = event.get("data", {})

    for field in required_fields:
        if field not in data:
            errors.append(f"Missing required field: {field}")

    # Check data types if specified
    type_checks = event.get("type_checks", {})
    for field, expected_type in type_checks.items():
        if field in data:
            actual_type = type(data[field]).__name__
            if actual_type != expected_type:
                errors.append(f"Field '{field}' expected {expected_type}, got {actual_type}")

    is_valid = len(errors) == 0

    return {
        "statusCode": 200 if is_valid else 400,
        "body": {
            "valid": is_valid,
            "errors": errors,
            "data": data
        }
    }
`,
		},

		// 3. 数据转换函数
		{
			Name:        "transform-data",
			Description: "转换数据格式",
			Runtime:     domain.RuntimeNodeJS20,
			Handler:     "index.handler",
			MemoryMB:    128,
			TimeoutSec:  30,
			Tags:        []string{"transform", "workflow"},
			Code: `exports.handler = async (event, context) => {
    const data = event.data || event;
    const transformations = event.transformations || [];

    let result = { ...data };

    for (const transform of transformations) {
        switch (transform.type) {
            case 'uppercase':
                if (result[transform.field]) {
                    result[transform.field] = String(result[transform.field]).toUpperCase();
                }
                break;
            case 'lowercase':
                if (result[transform.field]) {
                    result[transform.field] = String(result[transform.field]).toLowerCase();
                }
                break;
            case 'trim':
                if (result[transform.field]) {
                    result[transform.field] = String(result[transform.field]).trim();
                }
                break;
            case 'default':
                if (!result[transform.field]) {
                    result[transform.field] = transform.value;
                }
                break;
            case 'rename':
                if (result[transform.from]) {
                    result[transform.to] = result[transform.from];
                    delete result[transform.from];
                }
                break;
        }
    }

    return {
        statusCode: 200,
        body: {
            transformed: true,
            data: result,
            appliedTransformations: transformations.length
        }
    };
};
`,
		},

		// 4. 通知发送函数
		{
			Name:        "send-notification",
			Description: "发送通知消息（模拟）",
			Runtime:     domain.RuntimePython311,
			Handler:     "main.handler",
			MemoryMB:    128,
			TimeoutSec:  30,
			Tags:        []string{"notification", "workflow"},
			Code: `import json
from datetime import datetime

def handler(event, context):
    """Send notification handler (simulated)"""
    channel = event.get("channel", "email")
    recipient = event.get("recipient", "admin@example.com")
    subject = event.get("subject", "Notification")
    message = event.get("message", "No message provided")

    # Simulate sending notification
    notification_id = f"notif-{context.get('request_id', 'unknown')[:8]}"

    return {
        "statusCode": 200,
        "body": {
            "success": True,
            "notification_id": notification_id,
            "channel": channel,
            "recipient": recipient,
            "subject": subject,
            "sent_at": datetime.now().isoformat(),
            "message": f"Notification sent via {channel}"
        }
    }
`,
		},

		// 5. 订单处理函数
		{
			Name:        "process-order",
			Description: "处理订单信息",
			Runtime:     domain.RuntimePython311,
			Handler:     "main.handler",
			MemoryMB:    256,
			TimeoutSec:  60,
			Tags:        []string{"order", "workflow", "business"},
			Code: `import json
from datetime import datetime
import random
import string

def generate_order_id():
    return "ORD-" + ''.join(random.choices(string.ascii_uppercase + string.digits, k=8))

def handler(event, context):
    """Process order handler"""
    order_type = event.get("type", "standard")
    items = event.get("items", [])
    customer = event.get("customer", {})

    # Calculate totals
    subtotal = sum(item.get("price", 0) * item.get("quantity", 1) for item in items)

    # Apply discounts based on order type
    discount = 0
    if order_type == "vip":
        discount = subtotal * 0.1
    elif order_type == "express":
        discount = subtotal * 0.05

    # Calculate shipping
    shipping_cost = 0
    if order_type == "express":
        shipping_cost = 15
    elif order_type == "standard":
        shipping_cost = 5 if subtotal < 100 else 0
    # VIP gets free shipping

    total = subtotal - discount + shipping_cost

    return {
        "statusCode": 200,
        "body": {
            "order_id": generate_order_id(),
            "status": "processed",
            "order_type": order_type,
            "items_count": len(items),
            "subtotal": round(subtotal, 2),
            "discount": round(discount, 2),
            "shipping_cost": round(shipping_cost, 2),
            "total": round(total, 2),
            "customer_id": customer.get("id", "guest"),
            "processed_at": datetime.now().isoformat()
        }
    }
`,
		},

		// 6. 数据聚合函数
		{
			Name:        "aggregate-results",
			Description: "聚合多个并行分支的结果",
			Runtime:     domain.RuntimePython311,
			Handler:     "main.handler",
			MemoryMB:    128,
			TimeoutSec:  30,
			Tags:        []string{"aggregate", "workflow"},
			Code: `import json

def handler(event, context):
    """Aggregate results from parallel branches"""
    branches = event.get("branches", [])

    # Collect all results
    aggregated = {
        "total_branches": len(branches),
        "successful": 0,
        "failed": 0,
        "results": []
    }

    for i, branch in enumerate(branches):
        if isinstance(branch, dict):
            status = branch.get("status", "unknown")
            if status in ["done", "success", "completed"]:
                aggregated["successful"] += 1
            else:
                aggregated["failed"] += 1
            aggregated["results"].append({
                "branch_index": i,
                "data": branch
            })
        else:
            aggregated["results"].append({
                "branch_index": i,
                "data": branch
            })
            aggregated["successful"] += 1

    return {
        "statusCode": 200,
        "body": {
            "aggregated": True,
            "summary": aggregated,
            "all_successful": aggregated["failed"] == 0
        }
    }
`,
		},

		// 7. 条件检查函数
		{
			Name:        "check-condition",
			Description: "检查条件并返回布尔结果",
			Runtime:     domain.RuntimePython311,
			Handler:     "main.handler",
			MemoryMB:    128,
			TimeoutSec:  30,
			Tags:        []string{"condition", "workflow"},
			Code: `import json

def handler(event, context):
    """Check condition handler"""
    condition_type = event.get("condition", "equals")
    field = event.get("field", "value")
    expected = event.get("expected")
    actual = event.get("data", {}).get(field)

    result = False

    if condition_type == "equals":
        result = actual == expected
    elif condition_type == "not_equals":
        result = actual != expected
    elif condition_type == "greater_than":
        result = actual is not None and actual > expected
    elif condition_type == "less_than":
        result = actual is not None and actual < expected
    elif condition_type == "contains":
        result = expected in str(actual) if actual else False
    elif condition_type == "exists":
        result = actual is not None
    elif condition_type == "is_empty":
        result = actual is None or actual == "" or actual == [] or actual == {}

    return {
        "statusCode": 200,
        "body": {
            "condition": condition_type,
            "field": field,
            "expected": expected,
            "actual": actual,
            "result": result
        }
    }
`,
		},

		// 8. 错误处理函数
		{
			Name:        "handle-error",
			Description: "处理工作流中的错误",
			Runtime:     domain.RuntimePython311,
			Handler:     "main.handler",
			MemoryMB:    128,
			TimeoutSec:  30,
			Tags:        []string{"error", "workflow"},
			Code: `import json
from datetime import datetime

def handler(event, context):
    """Error handler"""
    error_code = event.get("Error", "UnknownError")
    error_cause = event.get("Cause", "Unknown cause")
    original_input = event.get("original_input", {})

    # Log the error (in real scenario, would send to monitoring system)
    error_record = {
        "error_code": error_code,
        "error_cause": error_cause,
        "timestamp": datetime.now().isoformat(),
        "request_id": context.get("request_id", "unknown"),
        "original_input": original_input
    }

    # Determine recovery action
    recovery_action = "log_and_continue"
    if "Timeout" in error_code:
        recovery_action = "retry_with_backoff"
    elif "Validation" in error_code:
        recovery_action = "reject_input"
    elif "NotFound" in error_code:
        recovery_action = "skip_and_continue"

    return {
        "statusCode": 200,
        "body": {
            "handled": True,
            "error_record": error_record,
            "recovery_action": recovery_action,
            "message": f"Error '{error_code}' has been handled"
        }
    }
`,
		},
	}
}

// SeedDefaultFunctions 在数据库中创建默认函数（如果不存在）
// 返回函数名称到ID的映射
func (e *Engine) SeedDefaultFunctions() (map[string]string, error) {
	defaults := DefaultFunctions()
	functionIDs := make(map[string]string)

	for _, def := range defaults {
		// 检查函数是否已存在
		existing, err := e.store.GetFunctionByName(def.Name)
		if err == nil && existing != nil {
			functionIDs[def.Name] = existing.ID
			e.logger.WithField("function", def.Name).Debug("Default function already exists, skipping")
			continue
		}

		// 创建函数
		fn := &domain.Function{
			ID:          uuid.New().String(),
			Name:        def.Name,
			Description: def.Description,
			Runtime:     def.Runtime,
			Handler:     def.Handler,
			Code:        def.Code,
			MemoryMB:    def.MemoryMB,
			TimeoutSec:  def.TimeoutSec,
			Tags:        def.Tags,
			Status:      domain.FunctionStatusActive,
			Version:     1,
		}

		if err := e.store.CreateFunction(fn); err != nil {
			e.logger.WithError(err).WithField("function", def.Name).Error("Failed to create default function")
			continue
		}

		functionIDs[def.Name] = fn.ID
		e.logger.WithField("function", def.Name).Info("Created default function")
	}

	return functionIDs, nil
}
