import api from './api'

export interface QuotaUsage {
  // 当前使用量
  function_count: number
  total_memory_mb: number
  today_invocations: number
  total_code_size_kb: number

  // 配额限制
  max_functions: number
  max_memory_mb: number
  max_invocations_per_day: number
  max_code_size_kb: number

  // 使用百分比
  function_usage_percent: number
  memory_usage_percent: number
  invocation_usage_percent: number
  code_usage_percent: number
}

export const quotaService = {
  // 获取配额使用情况
  getUsage: async (): Promise<QuotaUsage> => {
    return api.get('/v1/quota')
  },
}
