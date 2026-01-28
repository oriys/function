import api from './api'

export interface WarmingPolicy {
  id: string
  function_id: string
  enabled: boolean
  min_instances: number
  max_instances: number
  schedule?: string
  created_at: string
  updated_at: string
}

export interface WarmingStatus {
  function_id: string
  function_name: string
  warm_instances: number
  busy_instances: number
  cold_start_rate: number
  last_warmed_at?: string
  policy?: WarmingPolicy
}

export interface UpdateWarmingPolicyRequest {
  enabled: boolean
  min_instances: number
  max_instances: number
  schedule?: string
}

export interface TriggerWarmingRequest {
  instances?: number
}

export const warmingService = {
  async getStatus(functionId: string): Promise<WarmingStatus> {
    const response = await api.get(`/api/v1/functions/${functionId}/warming`)
    return response.data
  },

  async updatePolicy(functionId: string, data: UpdateWarmingPolicyRequest): Promise<WarmingPolicy> {
    const response = await api.put(`/api/v1/functions/${functionId}/warming`, data)
    return response.data
  },

  async triggerWarming(functionId: string, instances?: number): Promise<{ message: string; instances: number }> {
    const response = await api.post(`/api/v1/functions/${functionId}/warm`, { instances: instances || 1 })
    return response.data
  },
}
