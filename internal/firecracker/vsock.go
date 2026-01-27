//go:build linux
// +build linux

// Package firecracker 提供 Firecracker 微虚拟机的管理功能。
package firecracker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/mdlayher/vsock"
	"github.com/sirupsen/logrus"
)

// vsock 消息类型常量。
// vsock 是一种用于主机和虚拟机之间通信的高效套接字协议。
const (
	VsockPort       = 9999      // vsock 通信使用的端口号
	MessageTypeInit = 1         // 初始化消息类型，用于设置函数环境
	MessageTypeExec = 2         // 执行消息类型，用于触发函数执行
	MessageTypeResp = 3         // 响应消息类型，用于返回执行结果
	MessageTypePing = 4         // 心跳检测请求消息
	MessageTypePong = 5         // 心跳检测响应消息
)

// VsockMessage 表示通过 vsock 传输的消息结构。
// 使用 JSON 序列化，支持携带不同类型的 payload。
type VsockMessage struct {
	Type      uint8           `json:"type"`                 // 消息类型（Init/Exec/Resp/Ping/Pong）
	RequestID string          `json:"request_id"`           // 请求唯一标识符，用于关联请求和响应
	Payload   json.RawMessage `json:"payload,omitempty"`    // 消息载荷（可选）
}

// InitPayload 表示函数初始化请求的载荷。
// 包含运行函数所需的所有配置信息。
type InitPayload struct {
	FunctionID    string            `json:"function_id"`        // 函数唯一标识符
	Handler       string            `json:"handler"`            // 函数入口点（如 "main.handler"）
	Code          string            `json:"code"`               // 函数源代码或代码包路径
	Runtime       string            `json:"runtime"`            // 运行时类型（如 python3.11, nodejs20）
	EnvVars       map[string]string `json:"env_vars,omitempty"` // 环境变量
	MemoryLimitMB int               `json:"memory_limit_mb"`    // 内存限制（MB）
	TimeoutSec    int               `json:"timeout_sec"`        // 执行超时时间（秒）
	Layers        []LayerInfo       `json:"layers,omitempty"`   // 函数层列表（可选）
}

// LayerInfo 表示函数层的信息。
// 包含层的标识、版本、内容和加载顺序。
type LayerInfo struct {
	LayerID string `json:"layer_id"` // 层唯一标识符
	Version int    `json:"version"`  // 层版本号
	Content []byte `json:"content"`  // 层内容（ZIP 压缩包）
	Order   int    `json:"order"`    // 加载顺序（小的先加载）
}

// ExecPayload 表示函数执行请求的载荷。
// 包含传递给函数的输入参数。
type ExecPayload struct {
	Input json.RawMessage `json:"input"` // 函数输入参数（JSON 格式）
}

// ResponsePayload 表示函数执行响应的载荷。
// 包含执行结果或错误信息。
type ResponsePayload struct {
	Success      bool            `json:"success"`               // 执行是否成功
	Output       json.RawMessage `json:"output,omitempty"`      // 函数输出（成功时）
	Error        string          `json:"error,omitempty"`       // 错误信息（失败时）
	DurationMs   int64           `json:"duration_ms"`           // 执行耗时（毫秒）
	MemoryUsedMB int             `json:"memory_used_mb"`        // 内存使用量（MB）
}

// VsockClient 是 vsock 客户端，用于与虚拟机内的 agent 通信。
// 运行在主机侧，通过 CID（Context ID）连接到特定虚拟机。
type VsockClient struct {
	cid    uint32         // 虚拟机的 CID（Context ID）
	conn   net.Conn       // vsock 连接
	logger *logrus.Logger // 日志记录器
	mu     sync.Mutex     // 保护连接操作的互斥锁
}

// NewVsockClient 创建新的 vsock 客户端。
// 参数：
//   - cid: 目标虚拟机的 CID
//   - logger: 日志记录器
func NewVsockClient(cid uint32, logger *logrus.Logger) *VsockClient {
	return &VsockClient{
		cid:    cid,
		logger: logger,
	}
}

// Connect 连接到虚拟机内的 vsock 服务。
// 使用指数退避策略重试连接，最多重试 10 次。
func (c *VsockClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 如果已连接，直接返回
	if c.conn != nil {
		return nil
	}

	// 使用退避策略重试连接
	var lastErr error
	for i := 0; i < 10; i++ {
		conn, err := vsock.Dial(c.cid, VsockPort, nil)
		if err == nil {
			c.conn = conn
			c.logger.WithField("cid", c.cid).Debug("Vsock connected")
			return nil
		}
		lastErr = err
		// 等待递增的时间后重试
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(100*(i+1)) * time.Millisecond):
		}
	}
	return fmt.Errorf("failed to connect to vsock after retries: %w", lastErr)
}

// Close 关闭 vsock 连接。
func (c *VsockClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// InitFunction 初始化虚拟机中的函数环境。
// 发送函数配置信息到 agent，准备执行环境。
func (c *VsockClient) InitFunction(ctx context.Context, payload *InitPayload) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	msg := &VsockMessage{
		Type:      MessageTypeInit,
		RequestID: fmt.Sprintf("init-%d", time.Now().UnixNano()),
		Payload:   data,
	}

	resp, err := c.sendAndReceive(ctx, msg)
	if err != nil {
		return err
	}

	var respPayload ResponsePayload
	if err := json.Unmarshal(resp.Payload, &respPayload); err != nil {
		return err
	}

	if !respPayload.Success {
		return fmt.Errorf("init failed: %s", respPayload.Error)
	}

	return nil
}

// Execute 执行函数并返回结果。
// 向虚拟机内的 agent 发送执行请求，等待并返回执行结果。
// 参数：
//   - ctx: 上下文，用于超时控制
//   - requestID: 请求唯一标识符
//   - input: 函数输入参数（JSON 格式）
func (c *VsockClient) Execute(ctx context.Context, requestID string, input json.RawMessage) (*ResponsePayload, error) {
	execPayload := &ExecPayload{Input: input}
	data, err := json.Marshal(execPayload)
	if err != nil {
		return nil, err
	}

	msg := &VsockMessage{
		Type:      MessageTypeExec,
		RequestID: requestID,
		Payload:   data,
	}

	resp, err := c.sendAndReceive(ctx, msg)
	if err != nil {
		return nil, err
	}

	var respPayload ResponsePayload
	if err := json.Unmarshal(resp.Payload, &respPayload); err != nil {
		return nil, err
	}

	return &respPayload, nil
}

// Ping 发送心跳检测请求。
// 用于检查虚拟机内的 agent 是否正常运行。
func (c *VsockClient) Ping(ctx context.Context) error {
	msg := &VsockMessage{
		Type:      MessageTypePing,
		RequestID: fmt.Sprintf("ping-%d", time.Now().UnixNano()),
	}

	resp, err := c.sendAndReceive(ctx, msg)
	if err != nil {
		return err
	}

	if resp.Type != MessageTypePong {
		return fmt.Errorf("unexpected response type: %d", resp.Type)
	}

	return nil
}

// sendAndReceive 发送消息并等待响应。
// 这是一个同步操作，会阻塞直到收到响应或超时。
func (c *VsockClient) sendAndReceive(ctx context.Context, msg *VsockMessage) (*VsockMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, fmt.Errorf("not connected")
	}

	// 从上下文获取截止时间并设置连接超时
	if deadline, ok := ctx.Deadline(); ok {
		c.conn.SetDeadline(deadline)
		defer c.conn.SetDeadline(time.Time{}) // 清除超时设置
	}

	// 发送消息
	if err := c.writeMessage(msg); err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	// 接收响应
	resp, err := c.readMessage()
	if err != nil {
		return nil, fmt.Errorf("failed to receive message: %w", err)
	}

	return resp, nil
}

// writeMessage 将消息写入 vsock 连接。
// 使用长度前缀协议：4 字节大端序长度 + 消息体。
func (c *VsockClient) writeMessage(msg *VsockMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// 写入 4 字节长度前缀（大端序）
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := c.conn.Write(lenBuf); err != nil {
		return err
	}

	// 写入消息体
	if _, err := c.conn.Write(data); err != nil {
		return err
	}

	return nil
}

// readMessage 从 vsock 连接读取消息。
// 使用长度前缀协议解析消息。
func (c *VsockClient) readMessage() (*VsockMessage, error) {
	// 读取 4 字节长度前缀
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(c.conn, lenBuf); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lenBuf)

	// 读取消息体
	data := make([]byte, length)
	if _, err := io.ReadFull(c.conn, data); err != nil {
		return nil, err
	}

	var msg VsockMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}

	return &msg, nil
}

// VsockListener 是 vsock 监听器，用于接受来自虚拟机的连接。
// 运行在主机侧，监听特定端口的 vsock 连接请求。
type VsockListener struct {
	listener *vsock.Listener // 底层 vsock 监听器
	logger   *logrus.Logger  // 日志记录器
}

// NewVsockListener 创建新的 vsock 监听器。
// 参数：
//   - port: 监听的端口号
//   - logger: 日志记录器
func NewVsockListener(port uint32, logger *logrus.Logger) (*VsockListener, error) {
	l, err := vsock.Listen(port, nil)
	if err != nil {
		return nil, err
	}

	return &VsockListener{
		listener: l,
		logger:   logger,
	}, nil
}

// Accept 接受一个新的 vsock 连接。
// 阻塞直到有新连接到来。
func (l *VsockListener) Accept() (net.Conn, error) {
	return l.listener.Accept()
}

// Close 关闭监听器。
func (l *VsockListener) Close() error {
	return l.listener.Close()
}
