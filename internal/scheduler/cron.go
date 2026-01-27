package scheduler

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/oriys/nimbus/internal/domain"
	"github.com/oriys/nimbus/internal/storage"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
)

// CronManager 管理定时任务触发器
type CronManager struct {
	cron     *cron.Cron
	store    *storage.PostgresStore
	invoker  func(*domain.InvokeRequest) (string, error)
	logger   *logrus.Logger
	mu       sync.Mutex
	entries  map[string]cron.EntryID // functionID -> cronEntryID
}

// NewCronManager 创建一个新的 CronManager
func NewCronManager(store *storage.PostgresStore, invoker func(*domain.InvokeRequest) (string, error), logger *logrus.Logger) *CronManager {
	return &CronManager{
		cron:    cron.New(cron.WithSeconds()), // 支持秒级
		store:   store,
		invoker: invoker,
		logger:  logger,
		entries: make(map[string]cron.EntryID),
	}
}

// Start 启动 Cron 调度器并从数据库加载现有任务
func (cm *CronManager) Start() error {
	cm.cron.Start()
	cm.logger.Info("Cron manager started")

	// 加载所有带有 cron 表达式的活跃函数
	return cm.ReloadAll()
}

// ReloadAll 从数据库重新加载所有定时任务
func (cm *CronManager) ReloadAll() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 先清空现有任务
	for _, entryID := range cm.entries {
		cm.cron.Remove(entryID)
	}
	cm.entries = make(map[string]cron.EntryID)

	// 分页加载所有函数并筛选
	offset := 0
	limit := 100
	for {
		fns, total, err := cm.store.ListFunctions(offset, limit)
		if err != nil {
			return err
		}

		for _, fn := range fns {
			if fn.CronExpression != "" && fn.Status == domain.FunctionStatusActive {
				cm.addFunction(fn)
			}
		}

		offset += len(fns)
		if offset >= total || len(fns) == 0 {
			break
		}
	}

	cm.logger.WithField("count", len(cm.entries)).Info("Loaded cron tasks from database")
	return nil
}

// AddOrUpdateFunction 添加或更新函数的定时任务
func (cm *CronManager) AddOrUpdateFunction(fn *domain.Function) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 先尝试删除旧任务
	if entryID, ok := cm.entries[fn.ID]; ok {
		cm.cron.Remove(entryID)
		delete(cm.entries, fn.ID)
	}

	// 如果有表达式且状态活跃，则添加新任务
	if fn.CronExpression != "" && fn.Status == domain.FunctionStatusActive {
		cm.addFunction(fn)
	}
}

// RemoveFunction 移除函数的定时任务
func (cm *CronManager) RemoveFunction(functionID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if entryID, ok := cm.entries[functionID]; ok {
		cm.cron.Remove(entryID)
		delete(cm.entries, functionID)
	}
}

// addFunction 内部方法，将函数添加到 cron 调度器
// 调用此方法前必须持有 cm.mu 锁
func (cm *CronManager) addFunction(fn *domain.Function) {
	entryID, err := cm.cron.AddFunc(fn.CronExpression, func() {
		cm.logger.WithFields(logrus.Fields{
			"function_id":   fn.ID,
			"function_name": fn.Name,
			"cron":          fn.CronExpression,
		}).Info("Triggering cron function")

		// 构造一个定时任务触发的载荷
		payload := map[string]interface{}{
			"trigger": "cron",
			"cron":    fn.CronExpression,
			"time":    context.Background().Value("timestamp"), // 占位
		}
		payloadBytes, _ := json.Marshal(payload)

		req := &domain.InvokeRequest{
			FunctionID: fn.ID,
			Payload:    payloadBytes,
			Async:      true,
		}

		if _, err := cm.invoker(req); err != nil {
			cm.logger.WithError(err).WithField("function_id", fn.ID).Error("Failed to invoke cron function")
		}
	})

	if err != nil {
		cm.logger.WithError(err).WithFields(logrus.Fields{
			"function_id": fn.ID,
			"cron":        fn.CronExpression,
		}).Error("Failed to add cron function")
		return
	}

	cm.entries[fn.ID] = entryID
}

// Stop 停止 Cron 调度器
func (cm *CronManager) Stop() {
	cm.cron.Stop()
	cm.logger.Info("Cron manager stopped")
}
