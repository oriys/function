import api from './api'

// Alert types
export type AlertSeverity = 'critical' | 'warning' | 'info'
export type AlertStatus = 'active' | 'resolved' | 'silenced'
export type AlertConditionType = 'error_rate' | 'latency_p95' | 'latency_p99' | 'cold_start_rate' | 'invocations'
export type NotificationChannelType = 'email' | 'webhook' | 'slack' | 'dingtalk'

export interface AlertRule {
  id: string
  name: string
  description?: string
  function_id?: string
  condition: AlertConditionType
  operator: string
  threshold: number
  duration: string
  severity: AlertSeverity
  enabled: boolean
  channels: string[]
  created_at: string
  updated_at: string
}

export interface Alert {
  id: string
  rule_id: string
  rule_name: string
  function_id?: string
  function_name?: string
  severity: AlertSeverity
  status: AlertStatus
  message: string
  value: number
  threshold: number
  fired_at: string
  resolved_at?: string
}

export interface NotificationChannel {
  id: string
  name: string
  type: NotificationChannelType
  config: Record<string, string>
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface CreateAlertRuleRequest {
  name: string
  description?: string
  function_id?: string
  condition: AlertConditionType
  operator: string
  threshold: number
  duration: string
  severity: AlertSeverity
  channels: string[]
}

export interface UpdateAlertRuleRequest {
  name?: string
  description?: string
  condition?: AlertConditionType
  operator?: string
  threshold?: number
  duration?: string
  severity?: AlertSeverity
  enabled?: boolean
  channels?: string[]
}

export interface CreateNotificationChannelRequest {
  name: string
  type: NotificationChannelType
  config: Record<string, string>
}

export const alertsService = {
  // Alert Rules
  async listRules(): Promise<{ rules: AlertRule[]; total: number }> {
    const response = await api.get('/api/v1/alerts/rules')
    return response.data
  },

  async getRule(id: string): Promise<AlertRule> {
    const response = await api.get(`/api/v1/alerts/rules/${id}`)
    return response.data
  },

  async createRule(data: CreateAlertRuleRequest): Promise<AlertRule> {
    const response = await api.post('/api/v1/alerts/rules', data)
    return response.data
  },

  async updateRule(id: string, data: UpdateAlertRuleRequest): Promise<AlertRule> {
    const response = await api.put(`/api/v1/alerts/rules/${id}`, data)
    return response.data
  },

  async deleteRule(id: string): Promise<void> {
    await api.delete(`/api/v1/alerts/rules/${id}`)
  },

  // Alerts
  async listAlerts(params?: { status?: string; function_id?: string }): Promise<{ alerts: Alert[]; total: number }> {
    const response = await api.get('/api/v1/alerts', { params })
    return response.data
  },

  async resolveAlert(id: string): Promise<void> {
    await api.post(`/api/v1/alerts/${id}/resolve`)
  },

  // Notification Channels
  async listChannels(): Promise<{ channels: NotificationChannel[]; total: number }> {
    const response = await api.get('/api/v1/alerts/channels')
    return response.data
  },

  async createChannel(data: CreateNotificationChannelRequest): Promise<NotificationChannel> {
    const response = await api.post('/api/v1/alerts/channels', data)
    return response.data
  },

  async deleteChannel(id: string): Promise<void> {
    await api.delete(`/api/v1/alerts/channels/${id}`)
  },
}
