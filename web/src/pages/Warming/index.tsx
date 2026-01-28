import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import {
  Flame,
  RefreshCw,
  Settings,
  Play,
  Pause,
  Zap,
  Clock,
  Server,
  TrendingUp,
  X,
} from 'lucide-react'
import { cn } from '../../utils'
import { warmingService, type WarmingStatus } from '../../services/warming'
import { functionService } from '../../services'
import type { Function } from '../../types'

export default function Warming() {
  const [functions, setFunctions] = useState<Function[]>([])
  const [warmingStatuses, setWarmingStatuses] = useState<Record<string, WarmingStatus>>({})
  const [loading, setLoading] = useState(true)
  const [selectedFunction, setSelectedFunction] = useState<Function | null>(null)
  const [showPolicyModal, setShowPolicyModal] = useState(false)

  useEffect(() => {
    loadData()
  }, [])

  const loadData = async () => {
    setLoading(true)
    try {
      const data = await functionService.list({ limit: 100 })
      setFunctions(data.functions || [])

      // Load warming status for each function
      const statuses: Record<string, WarmingStatus> = {}
      await Promise.all(
        (data.functions || []).map(async (fn) => {
          try {
            const status = await warmingService.getStatus(fn.id)
            statuses[fn.id] = status
          } catch (err) {
            // Ignore errors for individual functions
          }
        })
      )
      setWarmingStatuses(statuses)
    } catch (err) {
      console.error('Failed to load data:', err)
    } finally {
      setLoading(false)
    }
  }

  const handleTriggerWarming = async (fn: Function, instances: number = 1) => {
    try {
      await warmingService.triggerWarming(fn.id, instances)
      // Reload status after warming
      const status = await warmingService.getStatus(fn.id)
      setWarmingStatuses(prev => ({ ...prev, [fn.id]: status }))
    } catch (err) {
      console.error('Failed to trigger warming:', err)
    }
  }

  const openPolicyModal = (fn: Function) => {
    setSelectedFunction(fn)
    setShowPolicyModal(true)
  }

  // Calculate overall stats
  const totalFunctions = functions.length
  const functionsWithWarming = Object.values(warmingStatuses).filter(s => s.policy?.enabled).length
  const totalWarmInstances = Object.values(warmingStatuses).reduce((sum, s) => sum + (s.warm_instances || 0), 0)
  const avgColdStartRate = Object.values(warmingStatuses).length > 0
    ? Object.values(warmingStatuses).reduce((sum, s) => sum + (s.cold_start_rate || 0), 0) / Object.values(warmingStatuses).length
    : 0

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-display font-bold text-foreground">函数预热</h1>
          <p className="text-muted-foreground mt-1">
            管理函数预热策略，减少冷启动延迟
          </p>
        </div>
        <button
          onClick={loadData}
          disabled={loading}
          className="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg border border-border text-muted-foreground hover:text-foreground hover:bg-secondary transition-colors"
        >
          <RefreshCw className={cn('w-4 h-4', loading && 'animate-spin')} />
          刷新
        </button>
      </div>

      {/* Stats Cards */}
      <div className="grid grid-cols-4 gap-4">
        <div className="bg-card rounded-xl border border-border p-4">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-lg bg-blue-500/10">
              <Zap className="w-5 h-5 text-blue-400" />
            </div>
            <div>
              <p className="text-2xl font-bold text-foreground">{totalFunctions}</p>
              <p className="text-sm text-muted-foreground">总函数数</p>
            </div>
          </div>
        </div>
        <div className="bg-card rounded-xl border border-border p-4">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-lg bg-orange-500/10">
              <Flame className="w-5 h-5 text-orange-400" />
            </div>
            <div>
              <p className="text-2xl font-bold text-foreground">{functionsWithWarming}</p>
              <p className="text-sm text-muted-foreground">已启用预热</p>
            </div>
          </div>
        </div>
        <div className="bg-card rounded-xl border border-border p-4">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-lg bg-green-500/10">
              <Server className="w-5 h-5 text-green-400" />
            </div>
            <div>
              <p className="text-2xl font-bold text-foreground">{totalWarmInstances}</p>
              <p className="text-sm text-muted-foreground">预热实例</p>
            </div>
          </div>
        </div>
        <div className="bg-card rounded-xl border border-border p-4">
          <div className="flex items-center gap-3">
            <div className="p-2 rounded-lg bg-purple-500/10">
              <TrendingUp className="w-5 h-5 text-purple-400" />
            </div>
            <div>
              <p className="text-2xl font-bold text-foreground">{(avgColdStartRate * 100).toFixed(1)}%</p>
              <p className="text-sm text-muted-foreground">平均冷启动率</p>
            </div>
          </div>
        </div>
      </div>

      {/* Functions List */}
      <div className="bg-card rounded-xl border border-border overflow-hidden">
        {loading ? (
          <div className="flex items-center justify-center py-16">
            <RefreshCw className="w-6 h-6 text-accent animate-spin" />
          </div>
        ) : functions.length === 0 ? (
          <div className="text-center py-16 text-muted-foreground">
            <Flame className="w-12 h-12 mx-auto mb-3 text-muted-foreground/30" />
            <p>暂无函数</p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-border bg-secondary/30">
                  <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    函数
                  </th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    预热状态
                  </th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    实例
                  </th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    冷启动率
                  </th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    策略
                  </th>
                  <th className="text-right px-4 py-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    操作
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {functions.map((fn) => {
                  const status = warmingStatuses[fn.id]
                  const policy = status?.policy
                  const isEnabled = policy?.enabled || false

                  return (
                    <tr key={fn.id} className="hover:bg-secondary/20 transition-colors">
                      <td className="px-4 py-3">
                        <Link
                          to={`/functions/${fn.id}`}
                          className="font-medium text-foreground hover:text-accent transition-colors"
                        >
                          {fn.name}
                        </Link>
                        <p className="text-sm text-muted-foreground">{fn.runtime}</p>
                      </td>
                      <td className="px-4 py-3">
                        <span className={cn(
                          'inline-flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-full',
                          isEnabled
                            ? 'bg-green-500/10 text-green-400'
                            : 'bg-gray-500/10 text-gray-400'
                        )}>
                          {isEnabled ? (
                            <>
                              <Flame className="w-3 h-3" />
                              已启用
                            </>
                          ) : (
                            <>
                              <Pause className="w-3 h-3" />
                              未启用
                            </>
                          )}
                        </span>
                      </td>
                      <td className="px-4 py-3">
                        <div className="text-sm">
                          <span className="text-foreground font-medium">
                            {status?.warm_instances || 0}
                          </span>
                          <span className="text-muted-foreground"> / </span>
                          <span className="text-muted-foreground">
                            {status?.busy_instances || 0} 使用中
                          </span>
                        </div>
                      </td>
                      <td className="px-4 py-3">
                        <div className="flex items-center gap-2">
                          <div className="w-16 h-2 bg-secondary rounded-full overflow-hidden">
                            <div
                              className={cn(
                                'h-full rounded-full transition-all',
                                (status?.cold_start_rate || 0) > 0.3 ? 'bg-red-400' :
                                (status?.cold_start_rate || 0) > 0.1 ? 'bg-yellow-400' : 'bg-green-400'
                              )}
                              style={{ width: `${Math.min(100, (status?.cold_start_rate || 0) * 100)}%` }}
                            />
                          </div>
                          <span className="text-sm text-muted-foreground">
                            {((status?.cold_start_rate || 0) * 100).toFixed(1)}%
                          </span>
                        </div>
                      </td>
                      <td className="px-4 py-3">
                        {policy ? (
                          <div className="text-sm text-muted-foreground">
                            <span>{policy.min_instances}-{policy.max_instances} 实例</span>
                            {policy.schedule && (
                              <span className="ml-2 flex items-center gap-1">
                                <Clock className="w-3 h-3" />
                                定时
                              </span>
                            )}
                          </div>
                        ) : (
                          <span className="text-sm text-muted-foreground">-</span>
                        )}
                      </td>
                      <td className="px-4 py-3 text-right">
                        <div className="flex items-center justify-end gap-2">
                          <button
                            onClick={() => handleTriggerWarming(fn)}
                            className="p-2 rounded-lg text-muted-foreground hover:text-accent hover:bg-accent/10 transition-colors"
                            title="立即预热"
                          >
                            <Play className="w-4 h-4" />
                          </button>
                          <button
                            onClick={() => openPolicyModal(fn)}
                            className="p-2 rounded-lg text-muted-foreground hover:text-foreground hover:bg-secondary transition-colors"
                            title="配置策略"
                          >
                            <Settings className="w-4 h-4" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Policy Modal */}
      {showPolicyModal && selectedFunction && (
        <WarmingPolicyModal
          fn={selectedFunction}
          status={warmingStatuses[selectedFunction.id]}
          onClose={() => {
            setShowPolicyModal(false)
            setSelectedFunction(null)
          }}
          onSave={() => {
            setShowPolicyModal(false)
            setSelectedFunction(null)
            loadData()
          }}
        />
      )}
    </div>
  )
}

// Warming Policy Modal Component
function WarmingPolicyModal({
  fn,
  status,
  onClose,
  onSave,
}: {
  fn: Function
  status?: WarmingStatus
  onClose: () => void
  onSave: () => void
}) {
  const policy = status?.policy
  const [loading, setLoading] = useState(false)
  const [form, setForm] = useState({
    enabled: policy?.enabled || false,
    min_instances: policy?.min_instances || 1,
    max_instances: policy?.max_instances || 3,
    schedule: policy?.schedule || '',
  })

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setLoading(true)
    try {
      await warmingService.updatePolicy(fn.id, form)
      onSave()
    } catch (err) {
      console.error('Failed to update warming policy:', err)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-card rounded-xl border border-border w-full max-w-md mx-4">
        <div className="flex items-center justify-between p-4 border-b border-border">
          <div>
            <h2 className="text-lg font-semibold text-foreground">预热策略配置</h2>
            <p className="text-sm text-muted-foreground">{fn.name}</p>
          </div>
          <button onClick={onClose} className="p-1 rounded-lg hover:bg-secondary">
            <X className="w-5 h-5 text-muted-foreground" />
          </button>
        </div>
        <form onSubmit={handleSubmit} className="p-4 space-y-4">
          <div className="flex items-center justify-between">
            <label className="text-sm font-medium text-foreground">启用预热</label>
            <button
              type="button"
              onClick={() => setForm({ ...form, enabled: !form.enabled })}
              className={cn(
                'relative inline-flex h-6 w-11 items-center rounded-full transition-colors',
                form.enabled ? 'bg-accent' : 'bg-secondary'
              )}
            >
              <span
                className={cn(
                  'inline-block h-4 w-4 transform rounded-full bg-white transition-transform',
                  form.enabled ? 'translate-x-6' : 'translate-x-1'
                )}
              />
            </button>
          </div>

          {form.enabled && (
            <>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm text-muted-foreground mb-1">最小实例数</label>
                  <input
                    type="number"
                    min="0"
                    max="10"
                    value={form.min_instances}
                    onChange={(e) => setForm({ ...form, min_instances: parseInt(e.target.value) || 0 })}
                    className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                  />
                </div>
                <div>
                  <label className="block text-sm text-muted-foreground mb-1">最大实例数</label>
                  <input
                    type="number"
                    min="1"
                    max="20"
                    value={form.max_instances}
                    onChange={(e) => setForm({ ...form, max_instances: parseInt(e.target.value) || 1 })}
                    className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                  />
                </div>
              </div>

              <div>
                <label className="block text-sm text-muted-foreground mb-1">
                  定时预热 (Cron 表达式)
                </label>
                <input
                  type="text"
                  value={form.schedule}
                  onChange={(e) => setForm({ ...form, schedule: e.target.value })}
                  className="w-full px-3 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                  placeholder="例如: 0 8 * * 1-5 (工作日早8点)"
                />
                <p className="text-xs text-muted-foreground mt-1">
                  留空则不启用定时预热
                </p>
              </div>
            </>
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
              disabled={loading}
              className="px-4 py-2 text-sm rounded-lg bg-accent text-accent-foreground hover:bg-accent/90 disabled:opacity-50"
            >
              {loading ? '保存中...' : '保存'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
