import { useState, useEffect } from 'react'
import {
  Bell,
  Plus,
  RefreshCw,
  AlertTriangle,
  AlertCircle,
  Info,
  CheckCircle,
  Trash2,
  ToggleLeft,
  ToggleRight,
  X,
  Mail,
  Webhook,
  MessageSquare,
} from 'lucide-react'
import { cn } from '../../utils'
import {
  alertsService,
  type AlertRule,
  type Alert,
  type NotificationChannel,
  type AlertSeverity,
  type AlertConditionType,
  type NotificationChannelType,
} from '../../services/alerts'

type TabType = 'rules' | 'alerts' | 'channels'

const severityConfig: Record<AlertSeverity, { label: string; icon: typeof AlertTriangle; color: string }> = {
  critical: { label: '严重', icon: AlertTriangle, color: 'text-red-400 bg-red-500/10 border-red-500/20' },
  warning: { label: '警告', icon: AlertCircle, color: 'text-yellow-400 bg-yellow-500/10 border-yellow-500/20' },
  info: { label: '信息', icon: Info, color: 'text-blue-400 bg-blue-500/10 border-blue-500/20' },
}

const conditionLabels: Record<AlertConditionType, string> = {
  error_rate: '错误率',
  latency_p95: 'P95 延迟',
  latency_p99: 'P99 延迟',
  cold_start_rate: '冷启动率',
  invocations: '调用次数',
}

const channelTypeConfig: Record<NotificationChannelType, { label: string; icon: typeof Mail }> = {
  email: { label: '邮件', icon: Mail },
  webhook: { label: 'Webhook', icon: Webhook },
  slack: { label: 'Slack', icon: MessageSquare },
  dingtalk: { label: '钉钉', icon: MessageSquare },
}

export default function Alerts() {
  const [activeTab, setActiveTab] = useState<TabType>('rules')
  const [rules, setRules] = useState<AlertRule[]>([])
  const [alerts, setAlerts] = useState<Alert[]>([])
  const [channels, setChannels] = useState<NotificationChannel[]>([])
  const [loading, setLoading] = useState(true)
  const [showCreateRule, setShowCreateRule] = useState(false)
  const [showCreateChannel, setShowCreateChannel] = useState(false)

  useEffect(() => {
    loadData()
  }, [activeTab])

  const loadData = async () => {
    setLoading(true)
    try {
      if (activeTab === 'rules') {
        const data = await alertsService.listRules()
        setRules(data.rules || [])
      } else if (activeTab === 'alerts') {
        const data = await alertsService.listAlerts()
        setAlerts(data.alerts || [])
      } else {
        const data = await alertsService.listChannels()
        setChannels(data.channels || [])
      }
    } catch (err) {
      console.error('Failed to load data:', err)
    } finally {
      setLoading(false)
    }
  }

  const handleDeleteRule = async (id: string) => {
    if (!confirm('确定要删除此告警规则吗？')) return
    try {
      await alertsService.deleteRule(id)
      loadData()
    } catch (err) {
      console.error('Failed to delete rule:', err)
    }
  }

  const handleToggleRule = async (rule: AlertRule) => {
    try {
      await alertsService.updateRule(rule.id, { enabled: !rule.enabled })
      loadData()
    } catch (err) {
      console.error('Failed to toggle rule:', err)
    }
  }

  const handleResolveAlert = async (id: string) => {
    try {
      await alertsService.resolveAlert(id)
      loadData()
    } catch (err) {
      console.error('Failed to resolve alert:', err)
    }
  }

  const handleDeleteChannel = async (id: string) => {
    if (!confirm('确定要删除此通知渠道吗？')) return
    try {
      await alertsService.deleteChannel(id)
      loadData()
    } catch (err) {
      console.error('Failed to delete channel:', err)
    }
  }

  const tabs: { id: TabType; label: string; count?: number }[] = [
    { id: 'rules', label: '告警规则', count: rules.length },
    { id: 'alerts', label: '活跃告警', count: alerts.filter(a => a.status === 'active').length },
    { id: 'channels', label: '通知渠道', count: channels.length },
  ]

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-display font-bold text-foreground">告警管理</h1>
          <p className="text-muted-foreground mt-1">
            配置告警规则，监控函数运行状态
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={loadData}
            disabled={loading}
            className="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg border border-border text-muted-foreground hover:text-foreground hover:bg-secondary transition-colors"
          >
            <RefreshCw className={cn('w-4 h-4', loading && 'animate-spin')} />
            刷新
          </button>
          {activeTab === 'rules' && (
            <button
              onClick={() => setShowCreateRule(true)}
              className="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg bg-accent text-accent-foreground hover:bg-accent/90 transition-colors"
            >
              <Plus className="w-4 h-4" />
              创建规则
            </button>
          )}
          {activeTab === 'channels' && (
            <button
              onClick={() => setShowCreateChannel(true)}
              className="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg bg-accent text-accent-foreground hover:bg-accent/90 transition-colors"
            >
              <Plus className="w-4 h-4" />
              添加渠道
            </button>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="border-b border-border">
        <div className="flex gap-4">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                'px-4 py-3 text-sm font-medium border-b-2 transition-colors',
                activeTab === tab.id
                  ? 'border-accent text-accent'
                  : 'border-transparent text-muted-foreground hover:text-foreground'
              )}
            >
              {tab.label}
              {tab.count !== undefined && tab.count > 0 && (
                <span className={cn(
                  'ml-2 px-2 py-0.5 text-xs rounded-full',
                  activeTab === tab.id ? 'bg-accent/20' : 'bg-muted'
                )}>
                  {tab.count}
                </span>
              )}
            </button>
          ))}
        </div>
      </div>

      {/* Content */}
      <div className="bg-card rounded-xl border border-border overflow-hidden">
        {loading ? (
          <div className="flex items-center justify-center py-16">
            <RefreshCw className="w-6 h-6 text-accent animate-spin" />
          </div>
        ) : (
          <>
            {/* Rules Tab */}
            {activeTab === 'rules' && (
              rules.length === 0 ? (
                <div className="text-center py-16 text-muted-foreground">
                  <Bell className="w-12 h-12 mx-auto mb-3 text-muted-foreground/30" />
                  <p>暂无告警规则</p>
                  <button
                    onClick={() => setShowCreateRule(true)}
                    className="mt-4 text-sm text-accent hover:underline"
                  >
                    创建第一个告警规则
                  </button>
                </div>
              ) : (
                <div className="divide-y divide-border">
                  {rules.map((rule) => {
                    const severity = severityConfig[rule.severity]
                    const SeverityIcon = severity.icon
                    return (
                      <div key={rule.id} className="p-4 hover:bg-secondary/20 transition-colors">
                        <div className="flex items-start justify-between">
                          <div className="flex-1">
                            <div className="flex items-center gap-3">
                              <h3 className="font-medium text-foreground">{rule.name}</h3>
                              <span className={cn(
                                'inline-flex items-center gap-1 px-2 py-0.5 text-xs rounded-full border',
                                severity.color
                              )}>
                                <SeverityIcon className="w-3 h-3" />
                                {severity.label}
                              </span>
                              {!rule.enabled && (
                                <span className="px-2 py-0.5 text-xs rounded-full bg-muted text-muted-foreground">
                                  已禁用
                                </span>
                              )}
                            </div>
                            {rule.description && (
                              <p className="text-sm text-muted-foreground mt-1">{rule.description}</p>
                            )}
                            <div className="flex items-center gap-4 mt-2 text-sm text-muted-foreground">
                              <span>条件: {conditionLabels[rule.condition]} {rule.operator} {rule.threshold}</span>
                              <span>持续: {rule.duration}</span>
                              {rule.function_id && <span>函数: {rule.function_id}</span>}
                            </div>
                          </div>
                          <div className="flex items-center gap-2">
                            <button
                              onClick={() => handleToggleRule(rule)}
                              className="p-2 rounded-lg text-muted-foreground hover:text-foreground hover:bg-secondary transition-colors"
                              title={rule.enabled ? '禁用' : '启用'}
                            >
                              {rule.enabled ? (
                                <ToggleRight className="w-5 h-5 text-green-400" />
                              ) : (
                                <ToggleLeft className="w-5 h-5" />
                              )}
                            </button>
                            <button
                              onClick={() => handleDeleteRule(rule.id)}
                              className="p-2 rounded-lg text-muted-foreground hover:text-red-400 hover:bg-red-500/10 transition-colors"
                              title="删除"
                            >
                              <Trash2 className="w-4 h-4" />
                            </button>
                          </div>
                        </div>
                      </div>
                    )
                  })}
                </div>
              )
            )}

            {/* Alerts Tab */}
            {activeTab === 'alerts' && (
              alerts.length === 0 ? (
                <div className="text-center py-16 text-muted-foreground">
                  <CheckCircle className="w-12 h-12 mx-auto mb-3 text-green-400/30" />
                  <p>当前没有活跃告警</p>
                </div>
              ) : (
                <div className="divide-y divide-border">
                  {alerts.map((alert) => {
                    const severity = severityConfig[alert.severity]
                    const SeverityIcon = severity.icon
                    return (
                      <div key={alert.id} className="p-4 hover:bg-secondary/20 transition-colors">
                        <div className="flex items-start justify-between">
                          <div className="flex-1">
                            <div className="flex items-center gap-3">
                              <SeverityIcon className={cn('w-5 h-5', severity.color.split(' ')[0])} />
                              <h3 className="font-medium text-foreground">{alert.rule_name}</h3>
                              <span className={cn(
                                'px-2 py-0.5 text-xs rounded-full',
                                alert.status === 'active' ? 'bg-red-500/10 text-red-400' :
                                alert.status === 'resolved' ? 'bg-green-500/10 text-green-400' :
                                'bg-gray-500/10 text-gray-400'
                              )}>
                                {alert.status === 'active' ? '活跃' : alert.status === 'resolved' ? '已解决' : '已静默'}
                              </span>
                            </div>
                            <p className="text-sm text-muted-foreground mt-1">{alert.message}</p>
                            <div className="flex items-center gap-4 mt-2 text-sm text-muted-foreground">
                              <span>值: {alert.value} (阈值: {alert.threshold})</span>
                              {alert.function_name && <span>函数: {alert.function_name}</span>}
                              <span>触发时间: {new Date(alert.fired_at).toLocaleString()}</span>
                            </div>
                          </div>
                          {alert.status === 'active' && (
                            <button
                              onClick={() => handleResolveAlert(alert.id)}
                              className="px-3 py-1.5 text-sm rounded-lg bg-green-500/10 text-green-400 hover:bg-green-500/20 transition-colors"
                            >
                              解决
                            </button>
                          )}
                        </div>
                      </div>
                    )
                  })}
                </div>
              )
            )}

            {/* Channels Tab */}
            {activeTab === 'channels' && (
              channels.length === 0 ? (
                <div className="text-center py-16 text-muted-foreground">
                  <Mail className="w-12 h-12 mx-auto mb-3 text-muted-foreground/30" />
                  <p>暂无通知渠道</p>
                  <button
                    onClick={() => setShowCreateChannel(true)}
                    className="mt-4 text-sm text-accent hover:underline"
                  >
                    添加第一个通知渠道
                  </button>
                </div>
              ) : (
                <div className="divide-y divide-border">
                  {channels.map((channel) => {
                    const typeConfig = channelTypeConfig[channel.type]
                    const TypeIcon = typeConfig.icon
                    return (
                      <div key={channel.id} className="p-4 hover:bg-secondary/20 transition-colors">
                        <div className="flex items-center justify-between">
                          <div className="flex items-center gap-3">
                            <div className="p-2 rounded-lg bg-accent/10">
                              <TypeIcon className="w-5 h-5 text-accent" />
                            </div>
                            <div>
                              <h3 className="font-medium text-foreground">{channel.name}</h3>
                              <p className="text-sm text-muted-foreground">{typeConfig.label}</p>
                            </div>
                            {!channel.enabled && (
                              <span className="px-2 py-0.5 text-xs rounded-full bg-muted text-muted-foreground">
                                已禁用
                              </span>
                            )}
                          </div>
                          <button
                            onClick={() => handleDeleteChannel(channel.id)}
                            className="p-2 rounded-lg text-muted-foreground hover:text-red-400 hover:bg-red-500/10 transition-colors"
                          >
                            <Trash2 className="w-4 h-4" />
                          </button>
                        </div>
                      </div>
                    )
                  })}
                </div>
              )
            )}
          </>
        )}
      </div>

      {/* Create Rule Modal */}
      {showCreateRule && (
        <CreateRuleModal
          channels={channels}
          onClose={() => setShowCreateRule(false)}
          onSuccess={() => {
            setShowCreateRule(false)
            loadData()
          }}
        />
      )}

      {/* Create Channel Modal */}
      {showCreateChannel && (
        <CreateChannelModal
          onClose={() => setShowCreateChannel(false)}
          onSuccess={() => {
            setShowCreateChannel(false)
            loadData()
          }}
        />
      )}
    </div>
  )
}

// Create Rule Modal Component
function CreateRuleModal({
  channels,
  onClose,
  onSuccess,
}: {
  channels: NotificationChannel[]
  onClose: () => void
  onSuccess: () => void
}) {
  const [loading, setLoading] = useState(false)
  const [form, setForm] = useState({
    name: '',
    description: '',
    condition: 'error_rate' as AlertConditionType,
    operator: '>',
    threshold: 5,
    duration: '5m',
    severity: 'warning' as AlertSeverity,
    channels: [] as string[],
  })

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim()) return

    setLoading(true)
    try {
      await alertsService.createRule(form)
      onSuccess()
    } catch (err) {
      console.error('Failed to create rule:', err)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-card rounded-xl border border-border w-full max-w-lg mx-4">
        <div className="flex items-center justify-between p-4 border-b border-border">
          <h2 className="text-lg font-semibold text-foreground">创建告警规则</h2>
          <button onClick={onClose} className="p-1 rounded-lg hover:bg-secondary">
            <X className="w-5 h-5 text-muted-foreground" />
          </button>
        </div>
        <form onSubmit={handleSubmit} className="p-4 space-y-4">
          <div>
            <label className="block text-sm text-muted-foreground mb-1">规则名称 *</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              placeholder="例如: 高错误率告警"
            />
          </div>
          <div>
            <label className="block text-sm text-muted-foreground mb-1">描述</label>
            <input
              type="text"
              value={form.description}
              onChange={(e) => setForm({ ...form, description: e.target.value })}
              className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              placeholder="规则描述"
            />
          </div>
          <div className="grid grid-cols-3 gap-4">
            <div>
              <label className="block text-sm text-muted-foreground mb-1">条件</label>
              <select
                value={form.condition}
                onChange={(e) => setForm({ ...form, condition: e.target.value as AlertConditionType })}
                className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              >
                {Object.entries(conditionLabels).map(([value, label]) => (
                  <option key={value} value={value}>{label}</option>
                ))}
              </select>
            </div>
            <div>
              <label className="block text-sm text-muted-foreground mb-1">运算符</label>
              <select
                value={form.operator}
                onChange={(e) => setForm({ ...form, operator: e.target.value })}
                className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              >
                <option value=">">&gt;</option>
                <option value=">=">&gt;=</option>
                <option value="<">&lt;</option>
                <option value="<=">&lt;=</option>
                <option value="==">==</option>
              </select>
            </div>
            <div>
              <label className="block text-sm text-muted-foreground mb-1">阈值</label>
              <input
                type="number"
                value={form.threshold}
                onChange={(e) => setForm({ ...form, threshold: parseFloat(e.target.value) })}
                className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm text-muted-foreground mb-1">持续时间</label>
              <input
                type="text"
                value={form.duration}
                onChange={(e) => setForm({ ...form, duration: e.target.value })}
                className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                placeholder="例如: 5m, 1h"
              />
            </div>
            <div>
              <label className="block text-sm text-muted-foreground mb-1">严重程度</label>
              <select
                value={form.severity}
                onChange={(e) => setForm({ ...form, severity: e.target.value as AlertSeverity })}
                className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              >
                <option value="info">信息</option>
                <option value="warning">警告</option>
                <option value="critical">严重</option>
              </select>
            </div>
          </div>
          {channels.length > 0 && (
            <div>
              <label className="block text-sm text-muted-foreground mb-1">通知渠道</label>
              <div className="space-y-2">
                {channels.map((channel) => (
                  <label key={channel.id} className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={form.channels.includes(channel.id)}
                      onChange={(e) => {
                        if (e.target.checked) {
                          setForm({ ...form, channels: [...form.channels, channel.id] })
                        } else {
                          setForm({ ...form, channels: form.channels.filter(id => id !== channel.id) })
                        }
                      }}
                      className="rounded border-border"
                    />
                    <span className="text-sm text-foreground">{channel.name}</span>
                  </label>
                ))}
              </div>
            </div>
          )}
          <div className="flex justify-end gap-2 pt-4">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 text-sm rounded-lg border border-border text-muted-foreground hover:text-foreground hover:bg-secondary"
            >
              取消
            </button>
            <button
              type="submit"
              disabled={loading || !form.name.trim()}
              className="px-4 py-2 text-sm rounded-lg bg-accent text-accent-foreground hover:bg-accent/90 disabled:opacity-50"
            >
              {loading ? '创建中...' : '创建'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

// Create Channel Modal Component
function CreateChannelModal({
  onClose,
  onSuccess,
}: {
  onClose: () => void
  onSuccess: () => void
}) {
  const [loading, setLoading] = useState(false)
  const [form, setForm] = useState({
    name: '',
    type: 'webhook' as NotificationChannelType,
    url: '',
  })

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!form.name.trim() || !form.url.trim()) return

    setLoading(true)
    try {
      await alertsService.createChannel({
        name: form.name,
        type: form.type,
        config: { url: form.url },
      })
      onSuccess()
    } catch (err) {
      console.error('Failed to create channel:', err)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-card rounded-xl border border-border w-full max-w-md mx-4">
        <div className="flex items-center justify-between p-4 border-b border-border">
          <h2 className="text-lg font-semibold text-foreground">添加通知渠道</h2>
          <button onClick={onClose} className="p-1 rounded-lg hover:bg-secondary">
            <X className="w-5 h-5 text-muted-foreground" />
          </button>
        </div>
        <form onSubmit={handleSubmit} className="p-4 space-y-4">
          <div>
            <label className="block text-sm text-muted-foreground mb-1">渠道名称 *</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              placeholder="例如: 运维通知群"
            />
          </div>
          <div>
            <label className="block text-sm text-muted-foreground mb-1">渠道类型</label>
            <select
              value={form.type}
              onChange={(e) => setForm({ ...form, type: e.target.value as NotificationChannelType })}
              className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
            >
              {Object.entries(channelTypeConfig).map(([value, config]) => (
                <option key={value} value={value}>{config.label}</option>
              ))}
            </select>
          </div>
          <div>
            <label className="block text-sm text-muted-foreground mb-1">Webhook URL *</label>
            <input
              type="url"
              value={form.url}
              onChange={(e) => setForm({ ...form, url: e.target.value })}
              className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              placeholder="https://..."
            />
          </div>
          <div className="flex justify-end gap-2 pt-4">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 text-sm rounded-lg border border-border text-muted-foreground hover:text-foreground hover:bg-secondary"
            >
              取消
            </button>
            <button
              type="submit"
              disabled={loading || !form.name.trim() || !form.url.trim()}
              className="px-4 py-2 text-sm rounded-lg bg-accent text-accent-foreground hover:bg-accent/90 disabled:opacity-50"
            >
              {loading ? '添加中...' : '添加'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
