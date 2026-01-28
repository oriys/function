// Package api 模板种子数据
package api

import (
	"github.com/google/uuid"
	"github.com/oriys/nimbus/internal/domain"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/sirupsen/logrus"
)

// SeedDefaultTemplates 创建默认模板数据
func SeedDefaultTemplates(store *storage.PostgresStore, logger *logrus.Logger) error {
	templates := defaultTemplates()

	for _, t := range templates {
		// 检查是否已存在
		existing, _ := store.GetTemplateByName(t.Name)
		if existing != nil {
			continue
		}

		t.ID = uuid.New().String()
		if err := store.CreateTemplate(&t); err != nil {
			logger.WithError(err).WithField("template", t.Name).Warn("Failed to seed template")
			continue
		}
		logger.WithField("template", t.Name).Info("Seeded template")
	}

	return nil
}

// defaultTemplates 返回默认模板列表
func defaultTemplates() []domain.Template {
	return []domain.Template{
		// Python 入门示例
		{
			Name:        "python-hello-world",
			DisplayName: "Hello World (Python)",
			Description: "最简单的 Python 函数示例，返回 Hello World 消息",
			Category:    domain.TemplateCategoryStarter,
			Runtime:     domain.RuntimePython311,
			Handler:     "handler.handler",
			Code: `def handler(event, context):
    """Hello World 示例函数"""
    name = event.get("name", "World")
    return {
        "statusCode": 200,
        "body": {
            "message": f"Hello, {name}!",
            "request_id": getattr(context, "request_id", "unknown")
        }
    }
`,
			DefaultMemory:  128,
			DefaultTimeout: 30,
			Tags:           []string{"入门", "示例"},
			Popular:        true,
		},
		// Node.js 入门示例
		{
			Name:        "nodejs-hello-world",
			DisplayName: "Hello World (Node.js)",
			Description: "最简单的 Node.js 函数示例，返回 Hello World 消息",
			Category:    domain.TemplateCategoryStarter,
			Runtime:     domain.RuntimeNodeJS20,
			Handler:     "index.handler",
			Code: `exports.handler = async (event, context) => {
    const name = event.name || "World";
    return {
        statusCode: 200,
        body: {
            message: ` + "`Hello, ${name}!`" + `,
            requestId: context.requestId || "unknown"
        }
    };
};
`,
			DefaultMemory:  128,
			DefaultTimeout: 30,
			Tags:           []string{"入门", "示例"},
			Popular:        true,
		},
		// Go 入门示例
		{
			Name:        "go-hello-world",
			DisplayName: "Hello World (Go)",
			Description: "最简单的 Go 函数示例，返回 Hello World 消息",
			Category:    domain.TemplateCategoryStarter,
			Runtime:     domain.RuntimeGo124,
			Handler:     "main.Handler",
			Code: `package main

import (
	"encoding/json"
)

type Event struct {
	Name string ` + "`json:\"name\"`" + `
}

type Response struct {
	StatusCode int         ` + "`json:\"statusCode\"`" + `
	Body       interface{} ` + "`json:\"body\"`" + `
}

func Handler(event json.RawMessage) (*Response, error) {
	var e Event
	json.Unmarshal(event, &e)

	name := e.Name
	if name == "" {
		name = "World"
	}

	return &Response{
		StatusCode: 200,
		Body: map[string]interface{}{
			"message": "Hello, " + name + "!",
		},
	}, nil
}
`,
			DefaultMemory:  128,
			DefaultTimeout: 30,
			Tags:           []string{"入门", "示例"},
			Popular:        true,
		},
		// Python Web API 模板
		{
			Name:        "python-rest-api",
			DisplayName: "REST API (Python)",
			Description: "RESTful API 模板，支持 GET/POST/PUT/DELETE 操作",
			Category:    domain.TemplateCategoryWebAPI,
			Runtime:     domain.RuntimePython311,
			Handler:     "handler.handler",
			Code: `import json

def handler(event, context):
    """REST API 处理函数"""
    method = event.get("httpMethod", "GET")
    path = event.get("path", "/")
    body = event.get("body", {})

    # 路由处理
    if method == "GET":
        return get_handler(path, event)
    elif method == "POST":
        return post_handler(path, body)
    elif method == "PUT":
        return put_handler(path, body)
    elif method == "DELETE":
        return delete_handler(path, event)
    else:
        return response(405, {"error": "Method not allowed"})

def get_handler(path, event):
    """处理 GET 请求"""
    return response(200, {"message": "GET request", "path": path})

def post_handler(path, body):
    """处理 POST 请求"""
    return response(201, {"message": "Created", "data": body})

def put_handler(path, body):
    """处理 PUT 请求"""
    return response(200, {"message": "Updated", "data": body})

def delete_handler(path, event):
    """处理 DELETE 请求"""
    return response(204, None)

def response(status_code, body):
    """构造响应"""
    return {
        "statusCode": status_code,
        "headers": {
            "Content-Type": "application/json"
        },
        "body": body
    }
`,
			DefaultMemory:  256,
			DefaultTimeout: 30,
			Tags:           []string{"API", "REST", "HTTP"},
			Popular:        true,
		},
		// Python 定时任务模板
		{
			Name:        "python-scheduled-task",
			DisplayName: "定时任务 (Python)",
			Description: "定时任务模板，适用于周期性执行的后台任务",
			Category:    domain.TemplateCategoryScheduled,
			Runtime:     domain.RuntimePython311,
			Handler:     "handler.handler",
			Code: `import json
from datetime import datetime

def handler(event, context):
    """定时任务处理函数

    此函数将按照 cron 表达式配置的时间周期性执行。
    适用于：
    - 数据清理
    - 报表生成
    - 健康检查
    - 缓存刷新
    """
    now = datetime.utcnow().isoformat()

    # 在这里添加你的定时任务逻辑
    result = run_scheduled_task()

    return {
        "statusCode": 200,
        "body": {
            "message": "Scheduled task completed",
            "executed_at": now,
            "result": result
        }
    }

def run_scheduled_task():
    """执行定时任务的具体逻辑"""
    # TODO: 实现你的业务逻辑
    return {"processed": True}
`,
			DefaultMemory:  256,
			DefaultTimeout: 60,
			Tags:           []string{"定时任务", "Cron", "后台任务"},
			Popular:        false,
		},
		// Python 数据处理模板
		{
			Name:        "python-data-processor",
			DisplayName: "数据处理器 (Python)",
			Description: "数据处理模板，适用于 ETL、数据转换等场景",
			Category:    domain.TemplateCategoryDataProcessing,
			Runtime:     domain.RuntimePython311,
			Handler:     "handler.handler",
			Code: `import json

def handler(event, context):
    """数据处理函数

    处理流程：
    1. 验证输入数据
    2. 转换数据格式
    3. 应用业务规则
    4. 返回处理结果
    """
    # 1. 获取输入数据
    data = event.get("data", [])
    if not isinstance(data, list):
        return error_response(400, "Input data must be an array")

    # 2. 处理数据
    processed = []
    errors = []

    for i, item in enumerate(data):
        try:
            result = process_item(item)
            processed.append(result)
        except Exception as e:
            errors.append({"index": i, "error": str(e)})

    # 3. 返回结果
    return {
        "statusCode": 200,
        "body": {
            "processed_count": len(processed),
            "error_count": len(errors),
            "results": processed,
            "errors": errors if errors else None
        }
    }

def process_item(item):
    """处理单个数据项"""
    # TODO: 实现你的数据处理逻辑
    return {
        "original": item,
        "transformed": item  # 返回转换后的数据
    }

def error_response(status_code, message):
    return {
        "statusCode": status_code,
        "body": {"error": message}
    }
`,
			DefaultMemory:  512,
			DefaultTimeout: 120,
			Tags:           []string{"数据处理", "ETL", "转换"},
			Popular:        false,
		},
		// Python Webhook 处理器
		{
			Name:        "python-webhook-handler",
			DisplayName: "Webhook 处理器 (Python)",
			Description: "Webhook 接收器模板，处理来自第三方服务的回调",
			Category:    domain.TemplateCategoryWebhook,
			Runtime:     domain.RuntimePython311,
			Handler:     "handler.handler",
			Code: `import json
import hmac
import hashlib

# 配置你的 Webhook 密钥
WEBHOOK_SECRET = "your-webhook-secret"

def handler(event, context):
    """Webhook 处理函数

    处理来自第三方服务（如 GitHub、Stripe、Slack 等）的 Webhook 回调
    """
    headers = event.get("headers", {})
    body = event.get("body", {})
    raw_body = event.get("rawBody", "")

    # 1. 验证签名（可选）
    signature = headers.get("X-Webhook-Signature", "")
    if signature and not verify_signature(raw_body, signature):
        return response(401, {"error": "Invalid signature"})

    # 2. 获取事件类型
    event_type = headers.get("X-Webhook-Event", body.get("type", "unknown"))

    # 3. 根据事件类型处理
    result = handle_event(event_type, body)

    return response(200, {
        "message": "Webhook processed",
        "event_type": event_type,
        "result": result
    })

def verify_signature(payload, signature):
    """验证 Webhook 签名"""
    expected = hmac.new(
        WEBHOOK_SECRET.encode(),
        payload.encode(),
        hashlib.sha256
    ).hexdigest()
    return hmac.compare_digest(f"sha256={expected}", signature)

def handle_event(event_type, payload):
    """根据事件类型处理 Webhook"""
    handlers = {
        "push": handle_push,
        "pull_request": handle_pr,
        "issue": handle_issue,
    }

    handler_func = handlers.get(event_type, handle_unknown)
    return handler_func(payload)

def handle_push(payload):
    return {"action": "push processed"}

def handle_pr(payload):
    return {"action": "pull request processed"}

def handle_issue(payload):
    return {"action": "issue processed"}

def handle_unknown(payload):
    return {"action": "unknown event type"}

def response(status_code, body):
    return {
        "statusCode": status_code,
        "headers": {"Content-Type": "application/json"},
        "body": body
    }
`,
			DefaultMemory:  256,
			DefaultTimeout: 30,
			Tags:           []string{"Webhook", "回调", "集成"},
			Popular:        false,
		},
		// Node.js REST API 模板
		{
			Name:        "nodejs-rest-api",
			DisplayName: "REST API (Node.js)",
			Description: "RESTful API 模板，使用 Node.js 实现",
			Category:    domain.TemplateCategoryWebAPI,
			Runtime:     domain.RuntimeNodeJS20,
			Handler:     "index.handler",
			Code: `exports.handler = async (event, context) => {
    const method = event.httpMethod || "GET";
    const path = event.path || "/";
    const body = event.body || {};

    // 路由处理
    switch (method) {
        case "GET":
            return getHandler(path, event);
        case "POST":
            return postHandler(path, body);
        case "PUT":
            return putHandler(path, body);
        case "DELETE":
            return deleteHandler(path, event);
        default:
            return response(405, { error: "Method not allowed" });
    }
};

function getHandler(path, event) {
    return response(200, { message: "GET request", path });
}

function postHandler(path, body) {
    return response(201, { message: "Created", data: body });
}

function putHandler(path, body) {
    return response(200, { message: "Updated", data: body });
}

function deleteHandler(path, event) {
    return response(204, null);
}

function response(statusCode, body) {
    return {
        statusCode,
        headers: { "Content-Type": "application/json" },
        body
    };
}
`,
			DefaultMemory:  256,
			DefaultTimeout: 30,
			Tags:           []string{"API", "REST", "HTTP"},
			Popular:        false,
		},
	}
}
