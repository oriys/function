// Package api 提供 HTTP API 处理器。
// 本文件实现调试 WebSocket 端点。
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/oriys/nimbus/internal/debug"
	"github.com/oriys/nimbus/internal/domain"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/sirupsen/logrus"
)

// DebugHandler 调试 WebSocket 处理器
type DebugHandler struct {
	store         *storage.PostgresStore
	logger        *logrus.Logger
	sessionMgr    *debug.Manager
	upgrader      websocket.Upgrader
	agentConnPool AgentConnectionPool // 用于与 Agent 通信

	// WebSocket 连接管理
	connections   map[string]*websocket.Conn
	connectionsMu sync.RWMutex

	// WebSocket 写锁（防止并发写入）
	wsWriteMu sync.Map // session_id -> *sync.Mutex

	// DAP 客户端连接管理
	dapClients   map[string]*debug.DAPClient
	dapClientsMu sync.RWMutex

	// 调试容器管理
	debugContainers   map[string]string // session_id -> container_id
	debugContainersMu sync.RWMutex
}

// AgentConnectionPool 定义与 Agent 通信的接口
// 实际实现由 scheduler 包提供
type AgentConnectionPool interface {
	// SendDebugMessage 发送调试消息到 Agent
	SendDebugMessage(functionID string, msg json.RawMessage) (json.RawMessage, error)
}

// NewDebugHandler 创建调试处理器
func NewDebugHandler(store *storage.PostgresStore, logger *logrus.Logger) *DebugHandler {
	return &DebugHandler{
		store:  store,
		logger: logger,
		sessionMgr: debug.NewManager(&debug.ManagerConfig{
			Logger: logger,
		}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true // 开发环境允许所有来源
			},
		},
		connections:     make(map[string]*websocket.Conn),
		dapClients:      make(map[string]*debug.DAPClient),
		debugContainers: make(map[string]string),
	}
}

// SetAgentPool 设置 Agent 连接池
func (h *DebugHandler) SetAgentPool(pool AgentConnectionPool) {
	h.agentConnPool = pool
}

// RegisterRoutes 注册调试路由
func (h *DebugHandler) RegisterRoutes(r chi.Router) {
	r.Route("/debug", func(r chi.Router) {
		// WebSocket 调试端点
		r.Get("/ws/{functionId}", h.DebugWebSocket)
		// 调试会话管理
		r.Get("/sessions", h.ListSessions)
		r.Delete("/sessions/{sessionId}", h.StopSession)
	})
}

// DebugMessage 前端发送的调试消息
type DebugMessage struct {
	// Type 消息类型: "dap" | "control"
	Type string `json:"type"`
	// Payload 消息内容
	Payload json.RawMessage `json:"payload"`
}

// DebugWebSocket 处理调试 WebSocket 连接
// 端点: GET /api/debug/ws/{functionId}
func (h *DebugHandler) DebugWebSocket(w http.ResponseWriter, r *http.Request) {
	functionID := chi.URLParam(r, "functionId")
	if functionID == "" {
		http.Error(w, "function id required", http.StatusBadRequest)
		return
	}

	// 验证函数存在
	fn, err := h.store.GetFunctionByID(functionID)
	if err == domain.ErrFunctionNotFound {
		fn, err = h.store.GetFunctionByName(functionID)
	}
	if err != nil {
		http.Error(w, "function not found", http.StatusNotFound)
		return
	}

	// 检查运行时是否支持调试
	if !isDebugSupported(fn.Runtime) {
		http.Error(w, "debugging not supported for this runtime", http.StatusBadRequest)
		return
	}

	// 升级为 WebSocket
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.WithError(err).Error("WebSocket upgrade failed")
		return
	}
	defer conn.Close()

	// 创建或获取调试会话
	session := h.sessionMgr.CreateSession(fn.ID)

	h.connectionsMu.Lock()
	h.connections[session.ID] = conn
	h.connectionsMu.Unlock()

	defer func() {
		h.connectionsMu.Lock()
		delete(h.connections, session.ID)
		h.connectionsMu.Unlock()
	}()

	h.logger.WithFields(logrus.Fields{
		"session_id":  session.ID,
		"function_id": fn.ID,
		"function":    fn.Name,
	}).Info("Debug WebSocket connected")

	// 发送会话信息
	h.sendMessage(conn, map[string]interface{}{
		"type":       "session",
		"session_id": session.ID,
		"state":      session.GetState(),
	})

	// 启动事件转发协程（从 Agent 到前端）
	go h.forwardEvents(session, conn)

	// 处理来自前端的消息
	h.handleMessages(session, conn, fn)
}

// handleMessages 处理来自前端的 WebSocket 消息
func (h *DebugHandler) handleMessages(session *debug.Session, conn *websocket.Conn, fn *domain.Function) {
	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.WithError(err).Debug("WebSocket read error")
			}
			return
		}

		var msg DebugMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			h.sendError(conn, "invalid message format")
			continue
		}

		switch msg.Type {
		case "dap":
			// 转发 DAP 消息到 Agent
			h.handleDAPMessage(session, conn, fn, msg.Payload)

		case "control":
			// 处理控制消息（启动/停止调试等）
			h.handleControlMessage(session, conn, fn, msg.Payload)

		default:
			h.sendError(conn, "unknown message type: "+msg.Type)
		}
	}
}

// ControlMessage 控制消息
type ControlMessage struct {
	Action string `json:"action"` // start, stop, launch
	// Attach 模式配置
	Host string `json:"host,omitempty"` // debugpy 主机地址
	Port int    `json:"port,omitempty"` // debugpy 端口
	// Launch 模式配置
	Payload     json.RawMessage `json:"payload,omitempty"`     // 函数调用参数
	StopOnEntry bool            `json:"stopOnEntry,omitempty"` // 是否在入口暂停
}

// handleControlMessage 处理控制消息
func (h *DebugHandler) handleControlMessage(session *debug.Session, conn *websocket.Conn, fn *domain.Function, payload json.RawMessage) {
	var ctrl ControlMessage
	if err := json.Unmarshal(payload, &ctrl); err != nil {
		h.sendError(conn, "invalid control message")
		return
	}

	switch ctrl.Action {
	case "start":
		// 启动调试会话
		session.SetState(debug.StateConnected)

		// 如果提供了 debugpy 地址，尝试连接
		if ctrl.Host != "" && ctrl.Port > 0 {
			dapClient := debug.NewDAPClient(h.logger)

			// 设置事件回调，转发到前端
			dapClient.SetEventHandler(func(event json.RawMessage) {
				h.sendMessage(conn, map[string]interface{}{
					"type":    "dap",
					"payload": json.RawMessage(event),
				})
			})

			if err := dapClient.Connect(ctrl.Host, ctrl.Port); err != nil {
				h.logger.WithError(err).Error("Failed to connect to debugpy")
				h.sendError(conn, "Failed to connect to debugpy: "+err.Error())
				return
			}

			h.dapClientsMu.Lock()
			h.dapClients[session.ID] = dapClient
			h.dapClientsMu.Unlock()

			h.logger.WithFields(logrus.Fields{
				"session_id": session.ID,
				"host":       ctrl.Host,
				"port":       ctrl.Port,
			}).Info("Connected to debugpy")
		}

		h.sendMessage(conn, map[string]interface{}{
			"type":  "control",
			"event": "started",
			"state": session.GetState(),
		})

		h.logger.WithField("session_id", session.ID).Info("Debug session started")

	case "stop":
		// 停止调试会话
		session.SetState(debug.StateStopped)

		// 关闭 DAP 客户端连接
		h.dapClientsMu.Lock()
		if dapClient, ok := h.dapClients[session.ID]; ok {
			dapClient.Close()
			delete(h.dapClients, session.ID)
		}
		h.dapClientsMu.Unlock()

		// 停止调试容器（如果有）
		h.stopDebugContainer(session.ID)

		h.sessionMgr.RemoveSession(session.ID)

		h.sendMessage(conn, map[string]interface{}{
			"type":  "control",
			"event": "stopped",
		})

		h.logger.WithField("session_id", session.ID).Info("Debug session stopped")

	case "launch":
		// Launch 模式：启动带 debugpy 的容器
		session.SetState(debug.StateConnected)

		// 启动调试容器
		debugPort, err := h.launchDebugContainer(session.ID, fn, ctrl.Payload, ctrl.StopOnEntry)
		if err != nil {
			h.logger.WithError(err).Error("Failed to launch debug container")
			h.sendError(conn, "Failed to launch debug container: "+err.Error())
			return
		}

		// 等待调试器就绪并连接
		h.sendMessage(conn, map[string]interface{}{
			"type":    "control",
			"event":   "progress",
			"message": "正在启动调试器...",
		})

		// 直接使用 DAP 客户端连接 debugpy
		// 不做单独的端口测试，因为 debugpy --wait-for-client 只接受一个连接
		dapClient := debug.NewDAPClient(h.logger)
		dapClient.SetEventHandler(func(event json.RawMessage) {
			h.sendMessage(conn, map[string]interface{}{
				"type":    "dap",
				"payload": json.RawMessage(event),
			})
		})

		// 重试连接直到 debugpy 就绪（最多 30 秒）
		maxWaitTime := 30 * time.Second
		retryInterval := 2 * time.Second
		startTime := time.Now()
		var connectErr error

		for time.Since(startTime) < maxWaitTime {
			connectErr = dapClient.Connect("127.0.0.1", debugPort)
			if connectErr == nil {
				break
			}
			h.logger.WithFields(logrus.Fields{
				"elapsed": time.Since(startTime).Seconds(),
				"error":   connectErr.Error(),
			}).Debug("Waiting for debugpy to be ready...")
			time.Sleep(retryInterval)
		}

		if connectErr != nil {
			h.logger.WithError(connectErr).Error("Failed to connect to debugpy")
			h.stopDebugContainer(session.ID)
			h.sendError(conn, "调试器启动超时，请稍后重试")
			return
		}

		h.dapClientsMu.Lock()
		h.dapClients[session.ID] = dapClient
		h.dapClientsMu.Unlock()

		h.sendMessage(conn, map[string]interface{}{
			"type":       "control",
			"event":      "launched",
			"state":      session.GetState(),
			"debug_port": debugPort,
		})

		h.logger.WithFields(logrus.Fields{
			"session_id": session.ID,
			"port":       debugPort,
		}).Info("Debug container launched and connected")

	default:
		h.sendError(conn, "unknown control action: "+ctrl.Action)
	}
}

// handleDAPMessage 处理 DAP 协议消息
func (h *DebugHandler) handleDAPMessage(session *debug.Session, conn *websocket.Conn, fn *domain.Function, dapMsg json.RawMessage) {
	// 解析 DAP 消息以获取 seq 和 command
	var dapHeader struct {
		Seq     int    `json:"seq"`
		Type    string `json:"type"`
		Command string `json:"command,omitempty"`
	}
	if err := json.Unmarshal(dapMsg, &dapHeader); err != nil {
		h.sendError(conn, "invalid DAP message")
		return
	}

	h.logger.WithFields(logrus.Fields{
		"session_id": session.ID,
		"seq":        dapHeader.Seq,
		"type":       dapHeader.Type,
		"command":    dapHeader.Command,
	}).Debug("Received DAP message")

	// 检查是否有真实的 DAP 客户端连接
	h.dapClientsMu.RLock()
	dapClient, hasRealDAP := h.dapClients[session.ID]
	h.dapClientsMu.RUnlock()

	if dapHeader.Type == "request" {
		if hasRealDAP && dapClient.IsConnected() {
			// 对于 Go 运行时的 launch 请求，需要注入正确的程序路径
			if dapHeader.Command == "launch" && fn.Runtime == domain.RuntimeGo124 {
				dapMsg = h.injectGoLaunchArgs(dapMsg, fn)
			}
			// 对于 Node.js 运行时的 launch 请求，需要注入正确的程序路径
			if dapHeader.Command == "launch" && fn.Runtime == domain.RuntimeNodeJS20 {
				dapMsg = h.injectNodeLaunchArgs(dapMsg, fn)
			}
			// 对于 Rust/WASM 运行时的 launch 请求，需要注入正确的程序路径
			if dapHeader.Command == "launch" && fn.Runtime == domain.RuntimeWasm {
				dapMsg = h.injectRustLaunchArgs(dapMsg, fn)
			}

			// 使用真实 DAP 连接
			resp, err := dapClient.SendRawRequest(dapMsg)
			if err != nil {
				h.logger.WithError(err).Error("Failed to send DAP request")
				h.sendMessage(conn, map[string]interface{}{
					"type": "dap",
					"payload": map[string]interface{}{
						"seq":         dapHeader.Seq + 1,
						"type":        "response",
						"request_seq": dapHeader.Seq,
						"command":     dapHeader.Command,
						"success":     false,
						"message":     err.Error(),
					},
				})
				return
			}

			h.sendMessage(conn, map[string]interface{}{
				"type":    "dap",
				"payload": json.RawMessage(resp),
			})
		} else {
			// 没有真实 DAP 连接，返回模拟响应
			response := h.mockDAPResponse(dapHeader.Seq, dapHeader.Command, dapMsg)
			h.sendMessage(conn, map[string]interface{}{
				"type":    "dap",
				"payload": response,
			})
		}
	}
}

// injectGoLaunchArgs 为 Go 运行时注入正确的 launch 参数
func (h *DebugHandler) injectGoLaunchArgs(dapMsg json.RawMessage, fn *domain.Function) json.RawMessage {
	var req map[string]interface{}
	if err := json.Unmarshal(dapMsg, &req); err != nil {
		return dapMsg
	}

	args, ok := req["arguments"].(map[string]interface{})
	if !ok {
		args = make(map[string]interface{})
	}

	// Delve DAP launch 需要的参数
	args["mode"] = "exec"           // 执行预编译的二进制
	args["program"] = "/tmp/app"    // 编译好的程序路径
	args["stopOnEntry"] = true      // 在入口暂停

	req["arguments"] = args

	modified, err := json.Marshal(req)
	if err != nil {
		return dapMsg
	}

	h.logger.WithField("args", args).Debug("Injected Go launch arguments")
	return modified
}

// injectNodeLaunchArgs 为 Node.js 运行时注入正确的 launch 参数
func (h *DebugHandler) injectNodeLaunchArgs(dapMsg json.RawMessage, fn *domain.Function) json.RawMessage {
	var req map[string]interface{}
	if err := json.Unmarshal(dapMsg, &req); err != nil {
		return dapMsg
	}

	args, ok := req["arguments"].(map[string]interface{})
	if !ok {
		args = make(map[string]interface{})
	}

	// Node.js DAP launch 需要的参数
	args["program"] = "/tmp/debug_runner.js" // 运行器脚本路径
	args["cwd"] = "/tmp"                      // 工作目录
	args["stopOnEntry"] = true                // 在入口暂停

	req["arguments"] = args

	modified, err := json.Marshal(req)
	if err != nil {
		return dapMsg
	}

	h.logger.WithField("args", args).Debug("Injected Node.js launch arguments")
	return modified
}

// injectRustLaunchArgs 为 Rust/WASM 运行时注入正确的 launch 参数
func (h *DebugHandler) injectRustLaunchArgs(dapMsg json.RawMessage, fn *domain.Function) json.RawMessage {
	var req map[string]interface{}
	if err := json.Unmarshal(dapMsg, &req); err != nil {
		return dapMsg
	}

	args, ok := req["arguments"].(map[string]interface{})
	if !ok {
		args = make(map[string]interface{})
	}

	// lldb-vscode DAP launch 需要的参数
	args["program"] = "/tmp/project/target/debug/debug_runner" // 编译好的 Rust 程序
	args["cwd"] = "/tmp/project"                                // 工作目录
	args["stopOnEntry"] = true                                  // 在入口暂停

	req["arguments"] = args

	modified, err := json.Marshal(req)
	if err != nil {
		return dapMsg
	}

	h.logger.WithField("args", args).Debug("Injected Rust launch arguments")
	return modified
}

// mockDAPResponse 生成模拟的 DAP 响应（用于开发阶段）
func (h *DebugHandler) mockDAPResponse(seq int, command string, request json.RawMessage) map[string]interface{} {
	response := map[string]interface{}{
		"seq":         seq + 1,
		"type":        "response",
		"request_seq": seq,
		"command":     command,
		"success":     true,
	}

	switch command {
	case "initialize":
		response["body"] = map[string]interface{}{
			"supportsConfigurationDoneRequest": true,
			"supportsSetVariable":              true,
			"supportsConditionalBreakpoints":   true,
			"supportsHitConditionalBreakpoints": true,
			"supportsEvaluateForHovers":        true,
			"supportsStepBack":                 false,
			"supportsRestartFrame":             false,
			"supportsGotoTargetsRequest":       false,
			"supportsStepInTargetsRequest":     false,
			"supportsCompletionsRequest":       false,
			"supportsModulesRequest":           false,
			"supportsExceptionOptions":         false,
			"supportsValueFormattingOptions":   false,
			"supportsExceptionInfoRequest":     false,
			"supportTerminateDebuggee":         true,
			"supportsDelayedStackTraceLoading": false,
			"supportsLoadedSourcesRequest":     false,
			"supportsLogPoints":                true,
			"supportsTerminateThreadsRequest":  false,
			"supportsSetExpression":            false,
			"supportsTerminateRequest":         true,
		}

	case "setBreakpoints":
		var req struct {
			Arguments struct {
				Source struct {
					Path string `json:"path"`
				} `json:"source"`
				Breakpoints []struct {
					Line int `json:"line"`
				} `json:"breakpoints"`
			} `json:"arguments"`
		}
		json.Unmarshal(request, &req)

		breakpoints := make([]map[string]interface{}, len(req.Arguments.Breakpoints))
		for i, bp := range req.Arguments.Breakpoints {
			breakpoints[i] = map[string]interface{}{
				"id":       i + 1,
				"verified": true,
				"line":     bp.Line,
				"source": map[string]interface{}{
					"path": req.Arguments.Source.Path,
				},
			}
		}
		response["body"] = map[string]interface{}{
			"breakpoints": breakpoints,
		}

	case "configurationDone":
		// 配置完成，无需特殊 body

	case "threads":
		response["body"] = map[string]interface{}{
			"threads": []map[string]interface{}{
				{"id": 1, "name": "MainThread"},
			},
		}

	case "stackTrace":
		response["body"] = map[string]interface{}{
			"stackFrames": []map[string]interface{}{},
			"totalFrames": 0,
		}

	case "scopes":
		response["body"] = map[string]interface{}{
			"scopes": []map[string]interface{}{},
		}

	case "variables":
		response["body"] = map[string]interface{}{
			"variables": []map[string]interface{}{},
		}

	case "continue":
		response["body"] = map[string]interface{}{
			"allThreadsContinued": true,
		}

	case "next", "stepIn", "stepOut":
		// 步进命令，无需 body

	case "disconnect":
		response["body"] = map[string]interface{}{}

	default:
		response["success"] = false
		response["message"] = "Unsupported command: " + command
	}

	return response
}

// forwardEvents 转发 Agent 事件到前端
func (h *DebugHandler) forwardEvents(session *debug.Session, conn *websocket.Conn) {
	for {
		select {
		case <-session.Context().Done():
			return
		case event, ok := <-session.Events():
			if !ok {
				return
			}
			h.sendMessage(conn, map[string]interface{}{
				"type":    "dap",
				"payload": event,
			})
		}
	}
}

// sendMessage 发送 WebSocket 消息（线程安全）
func (h *DebugHandler) sendMessage(conn *websocket.Conn, msg interface{}) {
	// 使用连接地址作为锁的 key
	lockKey := fmt.Sprintf("%p", conn)
	mutexI, _ := h.wsWriteMu.LoadOrStore(lockKey, &sync.Mutex{})
	mutex := mutexI.(*sync.Mutex)

	mutex.Lock()
	defer mutex.Unlock()

	if err := conn.WriteJSON(msg); err != nil {
		h.logger.WithError(err).Debug("Failed to send WebSocket message")
	}
}

// sendError 发送错误消息
func (h *DebugHandler) sendError(conn *websocket.Conn, errMsg string) {
	h.sendMessage(conn, map[string]interface{}{
		"type":  "error",
		"error": errMsg,
	})
}

// ListSessions 列出所有调试会话
// 端点: GET /api/debug/sessions
func (h *DebugHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	sessions := h.sessionMgr.ListSessions()

	result := make([]map[string]interface{}, len(sessions))
	for i, s := range sessions {
		result[i] = map[string]interface{}{
			"id":            s.ID,
			"function_id":   s.FunctionID,
			"state":         s.GetState(),
			"created_at":    s.CreatedAt.Format(time.RFC3339),
			"last_activity": s.LastActivity.Format(time.RFC3339),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions": result,
	})
}

// StopSession 停止调试会话
// 端点: DELETE /api/debug/sessions/{sessionId}
func (h *DebugHandler) StopSession(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionId")
	if sessionID == "" {
		http.Error(w, "session id required", http.StatusBadRequest)
		return
	}

	session, err := h.sessionMgr.GetSession(sessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// 通知 WebSocket 客户端
	h.connectionsMu.RLock()
	if conn, ok := h.connections[sessionID]; ok {
		h.sendMessage(conn, map[string]interface{}{
			"type":  "control",
			"event": "stopped",
		})
	}
	h.connectionsMu.RUnlock()

	h.sessionMgr.RemoveSession(sessionID)

	h.logger.WithFields(logrus.Fields{
		"session_id":  sessionID,
		"function_id": session.FunctionID,
	}).Info("Debug session stopped via API")

	w.WriteHeader(http.StatusNoContent)
}

// isDebugSupported 检查运行时是否支持调试
func isDebugSupported(runtime domain.Runtime) bool {
	switch runtime {
	case domain.RuntimePython311:
		return true
	case domain.RuntimeNodeJS20:
		return true // Phase 2
	case domain.RuntimeGo124:
		return true // Phase 3
	case domain.RuntimeWasm:
		return true // Phase 4 - Rust/WASM debugging
	default:
		return false
	}
}

// GetSessionManager 获取会话管理器（用于其他组件访问）
func (h *DebugHandler) GetSessionManager() *debug.Manager {
	return h.sessionMgr
}

// launchDebugContainer 启动带调试器的容器
func (h *DebugHandler) launchDebugContainer(sessionID string, fn *domain.Function, payload json.RawMessage, stopOnEntry bool) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 分配调试端口
	debugPort, err := h.allocateDebugPort()
	if err != nil {
		return 0, fmt.Errorf("failed to allocate debug port: %w", err)
	}

	// 根据运行时选择镜像、命令和容器内调试端口
	var imageName string
	var dockerCmd string
	var containerDebugPort int
	var envVars []string

	switch fn.Runtime {
	case domain.RuntimePython311:
		// 使用预装了 debugpy 的调试专用镜像
		imageName = "function-runtime-python-debug:latest"
		containerDebugPort = 5678
		// 使用 python -m debugpy 命令行模式启动调试
		// 将用户代码写入 handler.py
		dockerCmd = fmt.Sprintf(`cat > /tmp/handler.py << 'EOFPY'
%s
EOFPY

cat > /tmp/debug_runner.py << 'EOFDEBUG'
import sys
import json
import os
sys.path.insert(0, '/tmp')

from handler import handler

input_json = os.environ.get('FUNCTION_INPUT', '{}')
try:
    input_data = json.loads(input_json)
except:
    input_data = {}

result = handler(input_data)
print(json.dumps(result))
EOFDEBUG

python -m debugpy --listen 0.0.0.0:5678 --wait-for-client /tmp/debug_runner.py`, fn.Code)
		envVars = []string{
			fmt.Sprintf("FUNCTION_INPUT=%s", string(payload)),
			"PYTHONUNBUFFERED=1",
		}

	case domain.RuntimeNodeJS20:
		// 使用预装了 DAP 服务器的 Node.js 调试镜像
		imageName = "function-runtime-nodejs-debug:latest"
		containerDebugPort = 9229
		// 使用自定义的 DAP-to-CDP 桥接服务器
		// 1. 将用户代码写入 handler.js
		// 2. 创建运行器脚本
		// 3. 启动 DAP 服务器
		dockerCmd = fmt.Sprintf(`cat > /tmp/handler.js << 'EOFJS'
%s
EOFJS

cat > /tmp/debug_runner.js << 'EOFDEBUG'
const handler = require('/tmp/handler.js');
const inputJson = process.env.FUNCTION_INPUT || '{}';
let inputData = {};
try { inputData = JSON.parse(inputJson); } catch(e) {}

const fn = handler.handler || handler.default || handler;
if (typeof fn === 'function') {
    Promise.resolve(fn(inputData)).then(r => console.log(JSON.stringify(r)))
    .catch(e => { console.error(JSON.stringify({error: e.message})); process.exit(1); });
} else {
    console.error(JSON.stringify({error: 'handler not found'}));
    process.exit(1);
}
EOFDEBUG

# 启动 DAP 服务器
DAP_PORT=%d node /opt/dap-server/node-dap-server.js`, fn.Code, containerDebugPort)
		envVars = []string{
			fmt.Sprintf("FUNCTION_INPUT=%s", string(payload)),
		}

	case domain.RuntimeGo124:
		// 使用预装了 delve 的镜像
		imageName = "function-runtime-go-debug:latest"
		containerDebugPort = 2345
		// Go 使用 delve DAP 模式调试
		// 先编译代码，然后用 dlv dap 模式启动
		// 将用户代码直接写入 handler.go
		dockerCmd = fmt.Sprintf(`
# Write the Go source code
cat > /tmp/handler.go << 'EOFGO'
%s
EOFGO

# Write the main wrapper
cat > /tmp/main.go << 'EOFMAIN'
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	inputJson := os.Getenv("FUNCTION_INPUT")
	if inputJson == "" {
		inputJson = "{}"
	}
	var input map[string]interface{}
	json.Unmarshal([]byte(inputJson), &input)

	result := handler(input)
	output, _ := json.Marshal(result)
	fmt.Println(string(output))
}
EOFMAIN

# Compile with debug info (disable optimizations and inlining)
cd /tmp && go build -gcflags="all=-N -l" -o /tmp/app main.go handler.go

# Run delve in DAP mode
/go/bin/dlv dap --listen=0.0.0.0:2345 --accept-multiclient
`, fn.Code)
		envVars = []string{
			fmt.Sprintf("FUNCTION_INPUT=%s", string(payload)),
		}

	case domain.RuntimeWasm:
		// Rust/WASM 使用 GDB-based DAP 调试
		// 在调试模式下，编译为原生二进制而不是 WASM
		imageName = "function-runtime-rust-debug:latest"
		containerDebugPort = 4711
		// 使用 Python GDB DAP 服务器
		dockerCmd = fmt.Sprintf(`
# Copy pre-cached Cargo dependencies (speeds up build significantly)
cp -r /root/.cargo-cache/Cargo.lock /tmp/project/ 2>/dev/null || true
mkdir -p /tmp/project/target
cp -r /root/.cargo-cache/target/debug /tmp/project/target/ 2>/dev/null || true

# Create source directory structure (Cargo expects src/ subdirectory)
mkdir -p /tmp/project/src

# Write the Rust source code (user's handler function)
cat > /tmp/project/src/handler.rs << 'EOFRUST'
%s
EOFRUST

# Create Cargo.toml for debug build
cat > /tmp/project/Cargo.toml << 'EOFTOML'
[package]
name = "debug_runner"
version = "0.1.0"
edition = "2021"

[dependencies]
serde = { version = "1.0", features = ["derive"] }
serde_json = "1.0"

[profile.dev]
opt-level = 0
debug = true
EOFTOML

# Create main wrapper that includes the handler
cat > /tmp/project/src/main.rs << 'EOFMAIN'
use std::env;
use std::collections::HashMap;
use serde_json::Value;

mod handler;

fn main() {
    let input_json = env::var("FUNCTION_INPUT").unwrap_or_else(|_| "{}".to_string());
    let input: HashMap<String, Value> = serde_json::from_str(&input_json).unwrap_or_default();

    let result = handler::handler(input);
    match serde_json::to_string(&result) {
        Ok(s) => println!("{}", s),
        Err(e) => eprintln!("Error: {}", e),
    }
}
EOFMAIN

# Build with debug info
cd /tmp/project && cargo build 2>&1

# Start Python GDB DAP server
DAP_PORT=%d PROGRAM=/tmp/project/target/debug/debug_runner python3 /opt/dap-server/rust-gdb-dap-server.py
`, fn.Code, containerDebugPort)
		envVars = []string{
			fmt.Sprintf("FUNCTION_INPUT=%s", string(payload)),
		}

	default:
		return 0, fmt.Errorf("unsupported runtime for debugging: %s", fn.Runtime)
	}

	// 容器名称
	containerName := fmt.Sprintf("nimbus-debug-%s", sessionID[:8])

	// 构建 docker run 命令参数
	args := []string{
		"run",
		"-d",                                                          // 后台运行
		"--name", containerName,                                       // 容器名称
		"-p", fmt.Sprintf("%d:%d", debugPort, containerDebugPort),     // 端口映射
		"--user", "root",                                              // 以 root 用户运行以便写入文件
		"--label", "nimbus.debug=true",
		"--label", fmt.Sprintf("nimbus.session_id=%s", sessionID),
		"--label", fmt.Sprintf("nimbus.function=%s", fn.Name),
		"--label", fmt.Sprintf("nimbus.runtime=%s", fn.Runtime),
		"--network", "bridge",
		"--entrypoint", "/bin/sh",
	}

	// 添加环境变量
	for _, env := range envVars {
		args = append(args, "-e", env)
	}

	args = append(args, imageName, "-c", dockerCmd)

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		h.logger.WithError(err).WithField("output", string(output)).Error("Failed to create debug container")
		return 0, fmt.Errorf("failed to create container: %w, output: %s", err, string(output))
	}

	containerID := strings.TrimSpace(string(output))
	if containerID == "" {
		return 0, fmt.Errorf("docker run returned empty container id")
	}

	// 记录容器 ID
	h.debugContainersMu.Lock()
	h.debugContainers[sessionID] = containerID
	h.debugContainersMu.Unlock()

	h.logger.WithFields(logrus.Fields{
		"container_id":   containerID[:12],
		"container_name": containerName,
		"session_id":     sessionID,
		"debug_port":     debugPort,
	}).Info("Debug container started")

	return debugPort, nil
}

// stopDebugContainer 停止调试容器
func (h *DebugHandler) stopDebugContainer(sessionID string) {
	h.debugContainersMu.Lock()
	containerID, exists := h.debugContainers[sessionID]
	if exists {
		delete(h.debugContainers, sessionID)
	}
	h.debugContainersMu.Unlock()

	if !exists {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 停止并删除容器
	cmd := exec.CommandContext(ctx, "docker", "rm", "-f", containerID)
	if err := cmd.Run(); err != nil {
		h.logger.WithError(err).WithField("container_id", containerID[:12]).Debug("Failed to stop debug container")
		return
	}

	h.logger.WithFields(logrus.Fields{
		"container_id": containerID[:12],
		"session_id":   sessionID,
	}).Info("Debug container stopped")
}

// allocateDebugPort 分配一个可用的调试端口
func (h *DebugHandler) allocateDebugPort() (int, error) {
	// 使用更高的端口范围避免与 K8s 服务冲突
	// 15678-15800 范围专门用于调试容器
	for port := 15678; port <= 15800; port++ {
		// 检查端口是否可用（绑定所有接口确保没有冲突）
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err == nil {
			ln.Close()
			// 额外检查端口确实可用
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
			if err != nil {
				// 端口确实空闲
				return port, nil
			}
			conn.Close()
			// 端口被其他进程占用
			continue
		}
	}
	return 0, fmt.Errorf("no available debug port in range 15678-15800")
}
