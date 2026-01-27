// Package events 提供平台事件总线与触发器管理。
// 当前实现基于 NATS JetStream，用于发布/订阅函数与调用相关事件，并支持事件触发函数异步执行。
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/oriys/nimbus/internal/domain"
	"github.com/sirupsen/logrus"
)

// EventBus 封装 NATS/JetStream 连接与常用发布/订阅操作。
type EventBus struct {
	conn   *nats.Conn
	js     nats.JetStreamContext
	logger *logrus.Logger
}

// Event 表示平台内部事件（JSON 格式）。
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Source    string          `json:"source"`
	Subject   string          `json:"subject"`
	Data      json.RawMessage `json:"data"`
	Timestamp time.Time       `json:"timestamp"`
}

// EventHandler 定义事件处理回调。
type EventHandler func(event *Event) error

// NewEventBus 创建 EventBus 并初始化所需的 JetStream Stream。
func NewEventBus(natsURL string, logger *logrus.Logger) (*EventBus, error) {
	nc, err := nats.Connect(natsURL,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	// 为函数事件/调用事件初始化 Stream（不存在则创建，存在则尝试更新配置）
	streams := []nats.StreamConfig{
		{
			Name:     "FUNCTION_EVENTS",
			Subjects: []string{"function.>"},
			Storage:  nats.FileStorage,
			MaxAge:   24 * time.Hour * 7, // 保留 7 天
		},
		{
			Name:     "INVOCATIONS",
			Subjects: []string{"invocation.>"},
			Storage:  nats.FileStorage,
			MaxAge:   24 * time.Hour * 1, // 保留 1 天
		},
	}

	for _, cfg := range streams {
		_, err := js.AddStream(&cfg)
		if err != nil && err != nats.ErrStreamNameAlreadyInUse {
			// 失败时尝试更新（例如 Stream 已存在但配置不同）
			js.UpdateStream(&cfg)
		}
	}

	return &EventBus{
		conn:   nc,
		js:     js,
		logger: logger,
	}, nil
}

// Close 关闭底层 NATS 连接。
func (eb *EventBus) Close() error {
	eb.conn.Close()
	return nil
}

// Publish 发布事件到指定 subject。
func (eb *EventBus) Publish(ctx context.Context, subject string, event *Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = eb.js.Publish(subject, data)
	if err != nil {
		return fmt.Errorf("failed to publish event: %w", err)
	}

	eb.logger.WithFields(logrus.Fields{
		"subject":  subject,
		"event_id": event.ID,
		"type":     event.Type,
	}).Debug("Event published")

	return nil
}

// Subscribe 订阅匹配 subject 的事件（支持通配符）。
// ctx 取消时将自动取消订阅。
func (eb *EventBus) Subscribe(ctx context.Context, subject string, handler EventHandler) error {
	sub, err := eb.js.Subscribe(subject, func(msg *nats.Msg) {
		var event Event
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			eb.logger.WithError(err).Error("Failed to unmarshal event")
			msg.Nak()
			return
		}

		if err := handler(&event); err != nil {
			eb.logger.WithError(err).WithField("event_id", event.ID).Error("Failed to handle event")
			msg.Nak()
			return
		}

		msg.Ack()
	}, nats.Durable("function-processor"), nats.ManualAck())

	if err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	go func() {
		<-ctx.Done()
		sub.Unsubscribe()
	}()

	return nil
}

// PublishFunctionCreated 发布“函数创建”事件。
func (eb *EventBus) PublishFunctionCreated(ctx context.Context, fn *domain.Function) error {
	data, _ := json.Marshal(fn)
	event := &Event{
		ID:        fn.ID,
		Type:      "function.created",
		Source:    "function-manager",
		Subject:   "function.created",
		Data:      data,
		Timestamp: time.Now(),
	}
	return eb.Publish(ctx, "function.created", event)
}

// PublishInvocationStarted 发布“调用开始”事件。
func (eb *EventBus) PublishInvocationStarted(ctx context.Context, inv *domain.Invocation) error {
	data, _ := json.Marshal(inv)
	event := &Event{
		ID:        inv.ID,
		Type:      "invocation.started",
		Source:    "scheduler",
		Subject:   fmt.Sprintf("invocation.%s.started", inv.FunctionID),
		Data:      data,
		Timestamp: time.Now(),
	}
	return eb.Publish(ctx, event.Subject, event)
}

// PublishInvocationCompleted 发布“调用完成”事件。
func (eb *EventBus) PublishInvocationCompleted(ctx context.Context, inv *domain.Invocation) error {
	data, _ := json.Marshal(inv)
	event := &Event{
		ID:        inv.ID,
		Type:      "invocation.completed",
		Source:    "scheduler",
		Subject:   fmt.Sprintf("invocation.%s.completed", inv.FunctionID),
		Data:      data,
		Timestamp: time.Now(),
	}
	return eb.Publish(ctx, event.Subject, event)
}

// Trigger 表示函数触发器配置。
// Type: "event" 表示事件触发，"cron" 表示定时触发（预留）。
type Trigger struct {
	ID         string `json:"id"`
	FunctionID string `json:"function_id"`
	Type       string `json:"type"` // "event" / "cron"
	Subject    string `json:"subject,omitempty"`
	CronExpr   string `json:"cron_expr,omitempty"`
	Enabled    bool   `json:"enabled"`
}

// TriggerManager 维护触发器集合，并在事件到达时触发函数异步执行。
type TriggerManager struct {
	eventBus  *EventBus
	triggers  map[string]*Trigger
	scheduler InvocationScheduler
	logger    *logrus.Logger
}

// InvocationScheduler 定义触发器所需的最小调度能力（异步调用）。
type InvocationScheduler interface {
	InvokeAsync(req *domain.InvokeRequest) (string, error)
}

// NewTriggerManager 创建触发器管理器。
func NewTriggerManager(eventBus *EventBus, scheduler InvocationScheduler, logger *logrus.Logger) *TriggerManager {
	return &TriggerManager{
		eventBus:  eventBus,
		triggers:  make(map[string]*Trigger),
		scheduler: scheduler,
		logger:    logger,
	}
}

// RegisterTrigger 注册触发器。
// 对于事件触发器，会自动订阅对应 subject 并在事件到达时调用调度器的异步执行接口。
func (tm *TriggerManager) RegisterTrigger(ctx context.Context, trigger *Trigger) error {
	if trigger.Type == "event" {
		// 订阅事件 subject，并在回调中触发函数异步执行
		err := tm.eventBus.Subscribe(ctx, trigger.Subject, func(event *Event) error {
			_, err := tm.scheduler.InvokeAsync(&domain.InvokeRequest{
				FunctionID: trigger.FunctionID,
				Payload:    event.Data,
				Async:      true,
			})
			return err
		})
		if err != nil {
			return err
		}
	}

	tm.triggers[trigger.ID] = trigger
	tm.logger.WithFields(logrus.Fields{
		"trigger_id":  trigger.ID,
		"function_id": trigger.FunctionID,
		"type":        trigger.Type,
	}).Info("Trigger registered")

	return nil
}
