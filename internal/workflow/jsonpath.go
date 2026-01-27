// Package workflow 实现了工作流编排引擎。
package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// JSONPathProcessor JSONPath 处理器
type JSONPathProcessor struct{}

// NewJSONPathProcessor 创建 JSONPath 处理器实例
func NewJSONPathProcessor() *JSONPathProcessor {
	return &JSONPathProcessor{}
}

// ProcessInput 处理输入数据
// inputPath: 用于选择输入数据的子集
// parameters: 用于构造新的输入数据
func (p *JSONPathProcessor) ProcessInput(input json.RawMessage, inputPath string, parameters json.RawMessage) (json.RawMessage, error) {
	var data interface{}
	if err := json.Unmarshal(input, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input: %w", err)
	}

	// 应用 InputPath
	if inputPath != "" {
		filtered, err := getJSONPathValue(data, inputPath)
		if err != nil {
			return nil, fmt.Errorf("failed to apply InputPath: %w", err)
		}
		data = filtered
	}

	// 应用 Parameters
	if parameters != nil {
		processed, err := p.processParameters(data, parameters)
		if err != nil {
			return nil, fmt.Errorf("failed to apply Parameters: %w", err)
		}
		data = processed
	}

	result, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal processed input: %w", err)
	}

	return result, nil
}

// ProcessOutput 处理输出数据
// originalInput: 原始输入数据
// output: 状态输出数据
// outputPath: 用于选择输出数据的子集
// resultPath: 用于将输出合并到输入数据中
// resultSelector: 用于构造新的输出数据
func (p *JSONPathProcessor) ProcessOutput(originalInput, output json.RawMessage, outputPath, resultPath string, resultSelector json.RawMessage) (json.RawMessage, error) {
	var outputData interface{}
	if err := json.Unmarshal(output, &outputData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal output: %w", err)
	}

	// 应用 ResultSelector
	if resultSelector != nil {
		processed, err := p.processParameters(outputData, resultSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to apply ResultSelector: %w", err)
		}
		outputData = processed
	}

	// 应用 OutputPath
	if outputPath != "" {
		filtered, err := getJSONPathValue(outputData, outputPath)
		if err != nil {
			return nil, fmt.Errorf("failed to apply OutputPath: %w", err)
		}
		outputData = filtered
	}

	// 应用 ResultPath
	if resultPath != "" {
		var inputData interface{}
		if err := json.Unmarshal(originalInput, &inputData); err != nil {
			return nil, fmt.Errorf("failed to unmarshal original input: %w", err)
		}

		merged, err := p.mergeAtPath(inputData, outputData, resultPath)
		if err != nil {
			return nil, fmt.Errorf("failed to apply ResultPath: %w", err)
		}
		outputData = merged
	}

	result, err := json.Marshal(outputData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal processed output: %w", err)
	}

	return result, nil
}

// processParameters 处理 Parameters/ResultSelector
// 支持使用 JSONPath 引用输入数据中的值
// 键名以 ".$" 结尾的字段值会被视为 JSONPath 表达式
func (p *JSONPathProcessor) processParameters(input interface{}, params json.RawMessage) (interface{}, error) {
	var paramsData map[string]interface{}
	if err := json.Unmarshal(params, &paramsData); err != nil {
		// 如果不是对象，直接返回参数值
		var simpleValue interface{}
		if err := json.Unmarshal(params, &simpleValue); err != nil {
			return nil, err
		}
		return simpleValue, nil
	}

	result := make(map[string]interface{})
	for key, value := range paramsData {
		// 检查是否是 JSONPath 引用
		if strings.HasSuffix(key, ".$") {
			// 获取实际的键名（去掉 ".$" 后缀）
			actualKey := key[:len(key)-2]

			// 值应该是一个 JSONPath 字符串
			pathStr, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("value for key %s must be a string JSONPath", key)
			}

			// 从输入中获取值
			resolved, err := getJSONPathValue(input, pathStr)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve JSONPath %s: %w", pathStr, err)
			}

			result[actualKey] = resolved
		} else {
			// 处理嵌套对象
			if nested, ok := value.(map[string]interface{}); ok {
				nestedJSON, _ := json.Marshal(nested)
				processed, err := p.processParameters(input, nestedJSON)
				if err != nil {
					return nil, err
				}
				result[key] = processed
			} else {
				result[key] = value
			}
		}
	}

	return result, nil
}

// mergeAtPath 将值合并到指定路径
func (p *JSONPathProcessor) mergeAtPath(target interface{}, value interface{}, path string) (interface{}, error) {
	if path == "" || path == "$" {
		return value, nil
	}

	// 去掉开头的 "$."
	if strings.HasPrefix(path, "$.") {
		path = path[2:]
	} else if strings.HasPrefix(path, "$") {
		path = path[1:]
	}

	// 确保 target 是一个对象
	targetMap, ok := target.(map[string]interface{})
	if !ok {
		// 如果 target 不是对象，创建一个新对象
		targetMap = make(map[string]interface{})
	} else {
		// 复制 target 以避免修改原始数据
		targetMap = p.copyMap(targetMap)
	}

	// 解析路径并设置值
	parts := strings.Split(path, ".")
	current := targetMap

	for i, part := range parts {
		if i == len(parts)-1 {
			// 最后一部分，设置值
			current[part] = value
		} else {
			// 中间部分，确保路径存在
			next, exists := current[part]
			if !exists {
				next = make(map[string]interface{})
				current[part] = next
			}
			nextMap, ok := next.(map[string]interface{})
			if !ok {
				// 如果不是对象，替换为新对象
				nextMap = make(map[string]interface{})
				current[part] = nextMap
			}
			current = nextMap
		}
	}

	return targetMap, nil
}

// copyMap 深拷贝 map
func (p *JSONPathProcessor) copyMap(m map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		if nested, ok := v.(map[string]interface{}); ok {
			result[k] = p.copyMap(nested)
		} else if arr, ok := v.([]interface{}); ok {
			result[k] = p.copySlice(arr)
		} else {
			result[k] = v
		}
	}
	return result
}

// copySlice 深拷贝 slice
func (p *JSONPathProcessor) copySlice(s []interface{}) []interface{} {
	result := make([]interface{}, len(s))
	for i, v := range s {
		if nested, ok := v.(map[string]interface{}); ok {
			result[i] = p.copyMap(nested)
		} else if arr, ok := v.([]interface{}); ok {
			result[i] = p.copySlice(arr)
		} else {
			result[i] = v
		}
	}
	return result
}
