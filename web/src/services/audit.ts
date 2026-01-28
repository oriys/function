import api from './api'

export interface AuditLog {
  id: string
  action: string
  resource_type: string
  resource_id: string
  resource_name: string
  actor: string
  actor_ip: string
  details: Record<string, unknown> | null
  created_at: string
}

export interface ListAuditLogsParams {
  action?: string
  resource_type?: string
  resource_id?: string
  limit?: number
  offset?: number
}

export interface ListAuditLogsResponse {
  logs: AuditLog[]
  total: number
}

export interface AuditAction {
  value: string
  label: string
}

export const auditService = {
  // 获取审计日志列表
  list: async (params?: ListAuditLogsParams): Promise<ListAuditLogsResponse> => {
    const queryParams = new URLSearchParams()
    if (params?.action) queryParams.append('action', params.action)
    if (params?.resource_type) queryParams.append('resource_type', params.resource_type)
    if (params?.resource_id) queryParams.append('resource_id', params.resource_id)
    if (params?.limit) queryParams.append('limit', params.limit.toString())
    if (params?.offset) queryParams.append('offset', params.offset.toString())

    const queryString = queryParams.toString()
    const url = queryString ? `/v1/audit?${queryString}` : '/v1/audit'
    return api.get(url)
  },

  // 获取可用的操作类型
  getActions: async (): Promise<AuditAction[]> => {
    const res = await api.get<{ actions: AuditAction[] }>('/v1/audit/actions')
    return (res as unknown as { actions: AuditAction[] }).actions || []
  },
}
