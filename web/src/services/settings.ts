import api from './api'

export interface SystemSetting {
  key: string
  value: string
  description?: string
  updated_at: string
}

export interface ListSettingsResponse {
  settings: SystemSetting[]
}

// 设置分组定义
export interface SettingDefinition {
  key: string
  label: string
  description: string
  type: 'text' | 'number' | 'select' | 'boolean'
  options?: { value: string; label: string }[]
  unit?: string
  readonly?: boolean
}

export interface SettingGroup {
  id: string
  label: string
  description: string
  settings: SettingDefinition[]
}

// 预定义的设置分组
export const settingGroups: SettingGroup[] = [
  {
    id: 'general',
    label: '通用设置',
    description: '系统基础配置',
    settings: [
      {
        key: 'default_timeout',
        label: '默认超时时间',
        description: '函数执行的默认超时时间',
        type: 'number',
        unit: '秒',
      },
      {
        key: 'default_memory',
        label: '默认内存限制',
        description: '函数执行的默认内存配额',
        type: 'select',
        options: [
          { value: '128', label: '128 MB' },
          { value: '256', label: '256 MB' },
          { value: '512', label: '512 MB' },
          { value: '1024', label: '1024 MB' },
        ],
      },
    ],
  },
  {
    id: 'function',
    label: '函数设置',
    description: '函数相关配置',
    settings: [
      {
        key: 'max_code_size',
        label: '最大代码大小',
        description: '函数代码包的最大允许大小',
        type: 'number',
        unit: 'MB',
      },
      {
        key: 'max_concurrency',
        label: '最大并发数',
        description: '单个函数的最大并发执行数量',
        type: 'number',
      },
      {
        key: 'cold_start_timeout',
        label: '冷启动超时',
        description: '函数冷启动的最大等待时间',
        type: 'number',
        unit: '秒',
      },
    ],
  },
  {
    id: 'workflow',
    label: '工作流设置',
    description: '工作流引擎配置',
    settings: [
      {
        key: 'workflow_default_timeout',
        label: '工作流默认超时',
        description: '工作流执行的默认超时时间',
        type: 'number',
        unit: '秒',
      },
      {
        key: 'workflow_max_states',
        label: '最大状态数',
        description: '工作流允许的最大状态节点数量',
        type: 'number',
      },
      {
        key: 'workflow_max_history',
        label: '历史记录保留数',
        description: '每个工作流保留的最大执行历史数量',
        type: 'number',
      },
    ],
  },
  {
    id: 'retention',
    label: '数据保留',
    description: '数据清理策略配置',
    settings: [
      {
        key: 'log_retention_days',
        label: '日志保留天数',
        description: '调用日志的保留天数，超过后自动清理',
        type: 'number',
        unit: '天',
      },
      {
        key: 'dlq_retention_days',
        label: 'DLQ 保留天数',
        description: '死信队列消息的保留天数',
        type: 'number',
        unit: '天',
      },
      {
        key: 'invocation_retention_days',
        label: '调用记录保留天数',
        description: '函数调用记录的保留天数',
        type: 'number',
        unit: '天',
      },
    ],
  },
]

// 设置默认值
export const defaultSettings: Record<string, string> = {
  default_timeout: '30',
  default_memory: '128',
  max_code_size: '50',
  max_concurrency: '100',
  cold_start_timeout: '10',
  workflow_default_timeout: '300',
  workflow_max_states: '50',
  workflow_max_history: '100',
  log_retention_days: '30',
  dlq_retention_days: '90',
  invocation_retention_days: '30',
}

export const settingsService = {
  // 获取所有设置
  list: async (): Promise<SystemSetting[]> => {
    const res = await api.get<ListSettingsResponse>('/v1/settings')
    // Response interceptor already returns response.data
    return (res as unknown as ListSettingsResponse).settings || []
  },

  // 获取单个设置
  get: async (key: string): Promise<SystemSetting> => {
    return api.get(`/v1/settings/${key}`)
  },

  // 更新设置
  update: async (key: string, value: string): Promise<SystemSetting> => {
    return api.put(`/v1/settings/${key}`, { value })
  },
}
