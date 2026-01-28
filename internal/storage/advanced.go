// Package storage 提供 Phase 3 高级特性的存储实现
package storage

import (
	"errors"
	"sync"
	"time"

	"github.com/oriys/nimbus/internal/domain"
)

// 内存存储（用于 Phase 3 功能的临时存储）
var (
	alertRulesMu    sync.RWMutex
	alertRules      = make(map[string]*domain.AlertRule)
	alertsMu        sync.RWMutex
	alerts          = make(map[string]*domain.Alert)
	channelsMu      sync.RWMutex
	channels        = make(map[string]*domain.NotificationChannel)
	warmingPolicies = make(map[string]*domain.WarmingPolicy)
	warmingMu       sync.RWMutex
)

// ==================== 告警规则存储 ====================

// ListAlertRules 获取所有告警规则
func (s *PostgresStore) ListAlertRules() ([]*domain.AlertRule, error) {
	alertRulesMu.RLock()
	defer alertRulesMu.RUnlock()

	rules := make([]*domain.AlertRule, 0, len(alertRules))
	for _, rule := range alertRules {
		rules = append(rules, rule)
	}
	return rules, nil
}

// CreateAlertRule 创建告警规则
func (s *PostgresStore) CreateAlertRule(rule *domain.AlertRule) error {
	alertRulesMu.Lock()
	defer alertRulesMu.Unlock()

	alertRules[rule.ID] = rule
	return nil
}

// GetAlertRule 获取告警规则
func (s *PostgresStore) GetAlertRule(id string) (*domain.AlertRule, error) {
	alertRulesMu.RLock()
	defer alertRulesMu.RUnlock()

	rule, ok := alertRules[id]
	if !ok {
		return nil, errors.New("alert rule not found")
	}
	return rule, nil
}

// UpdateAlertRule 更新告警规则
func (s *PostgresStore) UpdateAlertRule(rule *domain.AlertRule) error {
	alertRulesMu.Lock()
	defer alertRulesMu.Unlock()

	if _, ok := alertRules[rule.ID]; !ok {
		return errors.New("alert rule not found")
	}
	alertRules[rule.ID] = rule
	return nil
}

// DeleteAlertRule 删除告警规则
func (s *PostgresStore) DeleteAlertRule(id string) error {
	alertRulesMu.Lock()
	defer alertRulesMu.Unlock()

	delete(alertRules, id)
	return nil
}

// ==================== 告警实例存储 ====================

// ListAlerts 获取告警列表
func (s *PostgresStore) ListAlerts(status, functionID string) ([]*domain.Alert, error) {
	alertsMu.RLock()
	defer alertsMu.RUnlock()

	result := make([]*domain.Alert, 0)
	for _, alert := range alerts {
		if status != "" && string(alert.Status) != status {
			continue
		}
		if functionID != "" && alert.FunctionID != functionID {
			continue
		}
		result = append(result, alert)
	}
	return result, nil
}

// ResolveAlert 解决告警
func (s *PostgresStore) ResolveAlert(id string) error {
	alertsMu.Lock()
	defer alertsMu.Unlock()

	alert, ok := alerts[id]
	if !ok {
		return errors.New("alert not found")
	}
	alert.Status = domain.AlertStatusResolved
	now := time.Now()
	alert.ResolvedAt = &now
	return nil
}

// ==================== 通知渠道存储 ====================

// ListNotificationChannels 获取所有通知渠道
func (s *PostgresStore) ListNotificationChannels() ([]*domain.NotificationChannel, error) {
	channelsMu.RLock()
	defer channelsMu.RUnlock()

	result := make([]*domain.NotificationChannel, 0, len(channels))
	for _, ch := range channels {
		result = append(result, ch)
	}
	return result, nil
}

// CreateNotificationChannel 创建通知渠道
func (s *PostgresStore) CreateNotificationChannel(ch *domain.NotificationChannel) error {
	channelsMu.Lock()
	defer channelsMu.Unlock()

	channels[ch.ID] = ch
	return nil
}

// DeleteNotificationChannel 删除通知渠道
func (s *PostgresStore) DeleteNotificationChannel(id string) error {
	channelsMu.Lock()
	defer channelsMu.Unlock()

	delete(channels, id)
	return nil
}

// ==================== 预热策略存储 ====================

// GetWarmingPolicy 获取函数预热策略
func (s *PostgresStore) GetWarmingPolicy(functionID string) (*domain.WarmingPolicy, error) {
	warmingMu.RLock()
	defer warmingMu.RUnlock()

	policy, ok := warmingPolicies[functionID]
	if !ok {
		return nil, errors.New("warming policy not found")
	}
	return policy, nil
}

// SaveWarmingPolicy 保存预热策略
func (s *PostgresStore) SaveWarmingPolicy(policy *domain.WarmingPolicy) error {
	warmingMu.Lock()
	defer warmingMu.Unlock()

	warmingPolicies[policy.FunctionID] = policy
	return nil
}

// ==================== 依赖分析存储 ====================

// GetFunctionCallsTo 获取函数调用的其他函数
func (s *PostgresStore) GetFunctionCallsTo(functionID string) ([]domain.FunctionDependency, error) {
	// TODO: 从调用记录中分析依赖关系
	return []domain.FunctionDependency{}, nil
}

// GetFunctionCalledBy 获取调用该函数的其他函数
func (s *PostgresStore) GetFunctionCalledBy(functionID string) ([]domain.FunctionDependency, error) {
	// TODO: 从调用记录中分析依赖关系
	return []domain.FunctionDependency{}, nil
}

// GetAllDependencyEdges 获取所有依赖边
func (s *PostgresStore) GetAllDependencyEdges() ([]domain.DependencyEdge, error) {
	// TODO: 从调用记录和工作流中分析依赖关系
	return []domain.DependencyEdge{}, nil
}

// GetWorkflowsUsingFunction 获取使用该函数的工作流
func (s *PostgresStore) GetWorkflowsUsingFunction(functionID string) ([]string, error) {
	// 查询使用该函数的工作流
	query := `
		SELECT DISTINCT id FROM workflows
		WHERE definition::text LIKE '%' || $1 || '%'
	`
	rows, err := s.db.Query(query, functionID)
	if err != nil {
		return []string{}, nil
	}
	defer rows.Close()

	var workflowIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		workflowIDs = append(workflowIDs, id)
	}
	return workflowIDs, nil
}
