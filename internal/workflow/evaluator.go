// Package workflow 实现了工作流编排引擎。
package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/oriys/nimbus/internal/domain"
)

// Evaluator Choice 条件求值器
type Evaluator struct{}

// NewEvaluator 创建求值器实例
func NewEvaluator() *Evaluator {
	return &Evaluator{}
}

// EvaluateChoice 评估 Choice 规则
func (e *Evaluator) EvaluateChoice(rule *domain.ChoiceRule, input json.RawMessage) (bool, error) {
	// 处理逻辑运算符
	if len(rule.And) > 0 {
		return e.evaluateAnd(rule.And, input)
	}
	if len(rule.Or) > 0 {
		return e.evaluateOr(rule.Or, input)
	}
	if rule.Not != nil {
		result, err := e.EvaluateChoice(rule.Not, input)
		if err != nil {
			return false, err
		}
		return !result, nil
	}

	// 处理比较条件
	return e.evaluateCondition(rule, input)
}

// evaluateAnd 评估 And 逻辑
func (e *Evaluator) evaluateAnd(rules []domain.ChoiceRule, input json.RawMessage) (bool, error) {
	for _, rule := range rules {
		result, err := e.EvaluateChoice(&rule, input)
		if err != nil {
			return false, err
		}
		if !result {
			return false, nil
		}
	}
	return true, nil
}

// evaluateOr 评估 Or 逻辑
func (e *Evaluator) evaluateOr(rules []domain.ChoiceRule, input json.RawMessage) (bool, error) {
	for _, rule := range rules {
		result, err := e.EvaluateChoice(&rule, input)
		if err != nil {
			return false, err
		}
		if result {
			return true, nil
		}
	}
	return false, nil
}

// evaluateCondition 评估单个条件
func (e *Evaluator) evaluateCondition(rule *domain.ChoiceRule, input json.RawMessage) (bool, error) {
	// 获取变量值
	value, err := e.getVariableValue(rule.Variable, input)
	if err != nil {
		// 变量不存在时的处理
		if rule.IsPresent != nil {
			return !*rule.IsPresent, nil
		}
		if rule.IsNull != nil {
			return *rule.IsNull, nil
		}
		return false, nil
	}

	// 存在性检查
	if rule.IsPresent != nil {
		return *rule.IsPresent, nil
	}
	if rule.IsNull != nil {
		return (value == nil) == *rule.IsNull, nil
	}

	// 类型检查
	if rule.IsNumeric != nil {
		_, isNum := value.(float64)
		return isNum == *rule.IsNumeric, nil
	}
	if rule.IsString != nil {
		_, isStr := value.(string)
		return isStr == *rule.IsString, nil
	}
	if rule.IsBoolean != nil {
		_, isBool := value.(bool)
		return isBool == *rule.IsBoolean, nil
	}
	if rule.IsTimestamp != nil {
		str, isStr := value.(string)
		if !isStr {
			return false == *rule.IsTimestamp, nil
		}
		_, err := time.Parse(time.RFC3339, str)
		return (err == nil) == *rule.IsTimestamp, nil
	}

	// 字符串比较
	if rule.StringEquals != "" {
		strVal, ok := value.(string)
		return ok && strVal == rule.StringEquals, nil
	}
	if rule.StringNotEquals != "" {
		strVal, ok := value.(string)
		return ok && strVal != rule.StringNotEquals, nil
	}
	if rule.StringLessThan != "" {
		strVal, ok := value.(string)
		return ok && strVal < rule.StringLessThan, nil
	}
	if rule.StringGreaterThan != "" {
		strVal, ok := value.(string)
		return ok && strVal > rule.StringGreaterThan, nil
	}
	if rule.StringLessThanEquals != "" {
		strVal, ok := value.(string)
		return ok && strVal <= rule.StringLessThanEquals, nil
	}
	if rule.StringGreaterThanEquals != "" {
		strVal, ok := value.(string)
		return ok && strVal >= rule.StringGreaterThanEquals, nil
	}
	if rule.StringMatches != "" {
		strVal, ok := value.(string)
		if !ok {
			return false, nil
		}
		// 转换简单通配符到正则表达式
		pattern := e.wildcardToRegex(rule.StringMatches)
		matched, err := regexp.MatchString(pattern, strVal)
		if err != nil {
			return false, fmt.Errorf("invalid pattern: %w", err)
		}
		return matched, nil
	}

	// 数字比较
	if rule.NumericEquals != nil {
		numVal, ok := value.(float64)
		return ok && numVal == *rule.NumericEquals, nil
	}
	if rule.NumericNotEquals != nil {
		numVal, ok := value.(float64)
		return ok && numVal != *rule.NumericNotEquals, nil
	}
	if rule.NumericLessThan != nil {
		numVal, ok := value.(float64)
		return ok && numVal < *rule.NumericLessThan, nil
	}
	if rule.NumericGreaterThan != nil {
		numVal, ok := value.(float64)
		return ok && numVal > *rule.NumericGreaterThan, nil
	}
	if rule.NumericLessThanEquals != nil {
		numVal, ok := value.(float64)
		return ok && numVal <= *rule.NumericLessThanEquals, nil
	}
	if rule.NumericGreaterThanEquals != nil {
		numVal, ok := value.(float64)
		return ok && numVal >= *rule.NumericGreaterThanEquals, nil
	}

	// 布尔比较
	if rule.BooleanEquals != nil {
		boolVal, ok := value.(bool)
		return ok && boolVal == *rule.BooleanEquals, nil
	}

	// 时间戳比较
	if rule.TimestampEquals != "" {
		return e.compareTimestamps(value, rule.TimestampEquals, func(a, b time.Time) bool { return a.Equal(b) })
	}
	if rule.TimestampNotEquals != "" {
		return e.compareTimestamps(value, rule.TimestampNotEquals, func(a, b time.Time) bool { return !a.Equal(b) })
	}
	if rule.TimestampLessThan != "" {
		return e.compareTimestamps(value, rule.TimestampLessThan, func(a, b time.Time) bool { return a.Before(b) })
	}
	if rule.TimestampGreaterThan != "" {
		return e.compareTimestamps(value, rule.TimestampGreaterThan, func(a, b time.Time) bool { return a.After(b) })
	}
	if rule.TimestampLessThanEquals != "" {
		return e.compareTimestamps(value, rule.TimestampLessThanEquals, func(a, b time.Time) bool { return a.Before(b) || a.Equal(b) })
	}
	if rule.TimestampGreaterThanEquals != "" {
		return e.compareTimestamps(value, rule.TimestampGreaterThanEquals, func(a, b time.Time) bool { return a.After(b) || a.Equal(b) })
	}

	return false, nil
}

// getVariableValue 从输入中获取变量值
func (e *Evaluator) getVariableValue(variable string, input json.RawMessage) (interface{}, error) {
	if variable == "" {
		return nil, fmt.Errorf("variable is empty")
	}

	var data interface{}
	if err := json.Unmarshal(input, &data); err != nil {
		return nil, err
	}

	return getJSONPathValue(data, variable)
}

// compareTimestamps 比较时间戳
func (e *Evaluator) compareTimestamps(value interface{}, compareWith string, compare func(a, b time.Time) bool) (bool, error) {
	strVal, ok := value.(string)
	if !ok {
		return false, nil
	}

	valueTime, err := time.Parse(time.RFC3339, strVal)
	if err != nil {
		return false, nil
	}

	compareTime, err := time.Parse(time.RFC3339, compareWith)
	if err != nil {
		return false, fmt.Errorf("invalid timestamp to compare: %w", err)
	}

	return compare(valueTime, compareTime), nil
}

// wildcardToRegex 将简单通配符转换为正则表达式
func (e *Evaluator) wildcardToRegex(pattern string) string {
	// 转义正则特殊字符
	result := regexp.QuoteMeta(pattern)
	// 将 \* 转换为 .*
	result = strings.ReplaceAll(result, `\*`, `.*`)
	return "^" + result + "$"
}

// getJSONPathValue 从数据中获取 JSONPath 值
func getJSONPathValue(data interface{}, path string) (interface{}, error) {
	if path == "" || path == "$" {
		return data, nil
	}

	// 去掉开头的 "$"
	if strings.HasPrefix(path, "$.") {
		path = path[2:]
	} else if strings.HasPrefix(path, "$") {
		path = path[1:]
	}

	// 简单的 JSONPath 实现，只支持点号分隔的路径
	parts := strings.Split(path, ".")
	current := data

	for _, part := range parts {
		if part == "" {
			continue
		}

		// 处理数组索引，如 items[0]
		if idx := strings.Index(part, "["); idx != -1 {
			fieldName := part[:idx]
			indexStr := part[idx+1 : len(part)-1]

			// 先获取字段
			if fieldName != "" {
				obj, ok := current.(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("cannot access field %s on non-object", fieldName)
				}
				val, exists := obj[fieldName]
				if !exists {
					return nil, fmt.Errorf("field %s not found", fieldName)
				}
				current = val
			}

			// 再获取数组元素
			arr, ok := current.([]interface{})
			if !ok {
				return nil, fmt.Errorf("cannot index non-array")
			}
			var index int
			fmt.Sscanf(indexStr, "%d", &index)
			if index < 0 || index >= len(arr) {
				return nil, fmt.Errorf("index %d out of bounds", index)
			}
			current = arr[index]
		} else {
			// 普通字段访问
			obj, ok := current.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("cannot access field %s on non-object", part)
			}
			val, exists := obj[part]
			if !exists {
				return nil, fmt.Errorf("field %s not found", part)
			}
			current = val
		}
	}

	return current, nil
}
