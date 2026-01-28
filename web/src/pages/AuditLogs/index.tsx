import { useState, useEffect } from 'react'
import {
  FileText,
  RefreshCw,
  Filter,
  ChevronLeft,
  ChevronRight,
  User,
  Globe,
  Clock,
  Activity,
  X,
} from 'lucide-react'
import { cn } from '../../utils'
import { auditService, type AuditLog, type AuditAction } from '../../services/audit'

// 资源类型中文映射
const resourceTypeLabels: Record<string, string> = {
  function: '函数',
  alias: '别名',
  layer: '层',
  environment: '环境',
  dlq: '死信队列',
  setting: '设置',
  retention: '数据保留',
  workflow: '工作流',
  apikey: 'API Key',
}

// 操作类型颜色
const actionColors: Record<string, string> = {
  create: 'bg-green-500/10 text-green-400 border-green-500/20',
  update: 'bg-blue-500/10 text-blue-400 border-blue-500/20',
  delete: 'bg-red-500/10 text-red-400 border-red-500/20',
  invoke: 'bg-purple-500/10 text-purple-400 border-purple-500/20',
  deploy: 'bg-orange-500/10 text-orange-400 border-orange-500/20',
  enable: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20',
  disable: 'bg-gray-500/10 text-gray-400 border-gray-500/20',
  retry: 'bg-yellow-500/10 text-yellow-400 border-yellow-500/20',
  cleanup: 'bg-pink-500/10 text-pink-400 border-pink-500/20',
}

function getActionColor(action: string): string {
  const actionType = action.split('.')[1] || action
  return actionColors[actionType] || 'bg-muted text-muted-foreground border-border'
}

export default function AuditLogs() {
  const [logs, setLogs] = useState<AuditLog[]>([])
  const [actions, setActions] = useState<AuditAction[]>([])
  const [loading, setLoading] = useState(true)
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const pageSize = 20

  // 筛选条件
  const [filterAction, setFilterAction] = useState<string>('')
  const [filterResourceType, setFilterResourceType] = useState<string>('')
  const [showFilters, setShowFilters] = useState(false)

  useEffect(() => {
    loadActions()
  }, [])

  useEffect(() => {
    loadLogs()
  }, [page, filterAction, filterResourceType])

  const loadActions = async () => {
    try {
      const data = await auditService.getActions()
      setActions(data)
    } catch (err) {
      console.error('Failed to load actions:', err)
    }
  }

  const loadLogs = async () => {
    try {
      setLoading(true)
      const data = await auditService.list({
        action: filterAction || undefined,
        resource_type: filterResourceType || undefined,
        limit: pageSize,
        offset: (page - 1) * pageSize,
      })
      setLogs(data.logs || [])
      setTotal(data.total || 0)
    } catch (err) {
      console.error('Failed to load audit logs:', err)
      setLogs([])
    } finally {
      setLoading(false)
    }
  }

  const handleRefresh = () => {
    setPage(1)
    loadLogs()
  }

  const clearFilters = () => {
    setFilterAction('')
    setFilterResourceType('')
    setPage(1)
  }

  const hasFilters = filterAction || filterResourceType
  const totalPages = Math.ceil(total / pageSize)

  // 获取唯一的资源类型
  const resourceTypes = [...new Set(actions.map((a) => a.value.split('.')[0]))]

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-display font-bold text-foreground">审计日志</h1>
          <p className="text-muted-foreground mt-1">
            查看系统操作记录，追踪所有变更历史
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setShowFilters(!showFilters)}
            className={cn(
              'inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg border transition-colors',
              hasFilters
                ? 'border-accent bg-accent/10 text-accent'
                : 'border-border text-muted-foreground hover:text-foreground hover:bg-secondary'
            )}
          >
            <Filter className="w-4 h-4" />
            筛选
            {hasFilters && (
              <span className="ml-1 px-1.5 py-0.5 text-xs bg-accent text-accent-foreground rounded">
                {[filterAction, filterResourceType].filter(Boolean).length}
              </span>
            )}
          </button>
          <button
            onClick={handleRefresh}
            disabled={loading}
            className="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg bg-accent text-accent-foreground hover:bg-accent/90 transition-colors"
          >
            <RefreshCw className={cn('w-4 h-4', loading && 'animate-spin')} />
            刷新
          </button>
        </div>
      </div>

      {/* Filters */}
      {showFilters && (
        <div className="bg-card rounded-xl border border-border p-4">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-sm font-medium text-foreground">筛选条件</h3>
            {hasFilters && (
              <button
                onClick={clearFilters}
                className="text-xs text-muted-foreground hover:text-foreground flex items-center gap-1"
              >
                <X className="w-3 h-3" />
                清除筛选
              </button>
            )}
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-xs text-muted-foreground mb-1">资源类型</label>
              <select
                value={filterResourceType}
                onChange={(e) => {
                  setFilterResourceType(e.target.value)
                  setPage(1)
                }}
                className="w-full px-3 py-2 bg-input border border-border rounded-lg text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              >
                <option value="">全部类型</option>
                {resourceTypes.map((type) => (
                  <option key={type} value={type}>
                    {resourceTypeLabels[type] || type}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className="block text-xs text-muted-foreground mb-1">操作类型</label>
              <select
                value={filterAction}
                onChange={(e) => {
                  setFilterAction(e.target.value)
                  setPage(1)
                }}
                className="w-full px-3 py-2 bg-input border border-border rounded-lg text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              >
                <option value="">全部操作</option>
                {actions
                  .filter((a) => !filterResourceType || a.value.startsWith(filterResourceType + '.'))
                  .map((action) => (
                    <option key={action.value} value={action.value}>
                      {action.label || action.value}
                    </option>
                  ))}
              </select>
            </div>
          </div>
        </div>
      )}

      {/* Stats */}
      <div className="flex items-center gap-4 text-sm text-muted-foreground">
        <span>共 {total} 条记录</span>
        {hasFilters && (
          <span className="text-accent">
            已筛选: {[filterResourceType && resourceTypeLabels[filterResourceType], filterAction].filter(Boolean).join(', ')}
          </span>
        )}
      </div>

      {/* Logs Table */}
      <div className="bg-card rounded-xl border border-border overflow-hidden">
        {loading ? (
          <div className="flex items-center justify-center py-16">
            <RefreshCw className="w-6 h-6 text-accent animate-spin" />
          </div>
        ) : logs.length === 0 ? (
          <div className="text-center py-16 text-muted-foreground">
            <FileText className="w-12 h-12 mx-auto mb-3 text-muted-foreground/30" />
            <p>暂无审计日志</p>
            {hasFilters && (
              <button
                onClick={clearFilters}
                className="mt-2 text-sm text-accent hover:underline"
              >
                清除筛选条件
              </button>
            )}
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="border-b border-border bg-secondary/30">
                  <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    时间
                  </th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    操作
                  </th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    资源
                  </th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    执行者
                  </th>
                  <th className="text-left px-4 py-3 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                    详情
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {logs.map((log) => (
                  <tr key={log.id} className="hover:bg-secondary/20 transition-colors">
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2 text-sm">
                        <Clock className="w-4 h-4 text-muted-foreground" />
                        <span className="text-foreground">
                          {new Date(log.created_at).toLocaleString()}
                        </span>
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <span
                        className={cn(
                          'inline-flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-full border',
                          getActionColor(log.action)
                        )}
                      >
                        <Activity className="w-3 h-3" />
                        {log.action}
                      </span>
                    </td>
                    <td className="px-4 py-3">
                      <div className="text-sm">
                        <span className="text-muted-foreground">
                          {resourceTypeLabels[log.resource_type] || log.resource_type}:
                        </span>{' '}
                        <span className="text-foreground font-medium">
                          {log.resource_name || log.resource_id}
                        </span>
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2 text-sm">
                        <User className="w-4 h-4 text-muted-foreground" />
                        <span className="text-foreground">{log.actor || 'system'}</span>
                        {log.actor_ip && (
                          <span className="flex items-center gap-1 text-muted-foreground">
                            <Globe className="w-3 h-3" />
                            {log.actor_ip}
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <span className="text-sm text-muted-foreground max-w-xs truncate block">
                        {log.details ? JSON.stringify(log.details) : '-'}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <div className="text-sm text-muted-foreground">
            第 {page} 页，共 {totalPages} 页
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page === 1}
              className="p-2 rounded-lg border border-border text-muted-foreground hover:text-foreground hover:bg-secondary disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              <ChevronLeft className="w-4 h-4" />
            </button>
            <button
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page === totalPages}
              className="p-2 rounded-lg border border-border text-muted-foreground hover:text-foreground hover:bg-secondary disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              <ChevronRight className="w-4 h-4" />
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
