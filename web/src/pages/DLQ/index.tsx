import { useState, useEffect } from 'react'
import {
  AlertTriangle,
  RefreshCw,
  RotateCcw,
  Trash2,
  Eye,
  X,
  CheckCircle2,
  Clock,
  Archive,
  Filter,
  XCircle,
} from 'lucide-react'
import { dlqService, functionService } from '../../services'
import type { DeadLetterMessage, DLQStatus, Function } from '../../types'
import { DLQ_STATUS_COLORS, DLQ_STATUS_LABELS } from '../../types'
import { formatDate, cn } from '../../utils'
import Pagination from '../../components/Pagination'
import { Skeleton } from '../../components/Skeleton'
import EmptyState from '../../components/EmptyState'
import { useToast } from '../../components/Toast'

// 统计卡片
interface StatsCardProps {
  title: string
  value: number
  icon: typeof AlertTriangle
  color: string
}

function StatsCard({ title, value, icon: Icon, color }: StatsCardProps) {
  return (
    <div className="bg-card border border-border rounded-lg p-4">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm text-muted-foreground">{title}</p>
          <p className="text-2xl font-bold text-foreground mt-1">{value}</p>
        </div>
        <div className={cn('p-3 rounded-lg', color)}>
          <Icon className="w-5 h-5" />
        </div>
      </div>
    </div>
  )
}

// 状态徽章
function StatusBadge({ status }: { status: DLQStatus }) {
  return (
    <span className={cn('px-2 py-0.5 text-xs font-medium rounded-full', DLQ_STATUS_COLORS[status])}>
      {DLQ_STATUS_LABELS[status]}
    </span>
  )
}

// 消息详情 Modal
interface MessageDetailModalProps {
  message: DeadLetterMessage | null
  isOpen: boolean
  onClose: () => void
  onRetry: (id: string) => void
  onDiscard: (id: string) => void
  isRetrying: boolean
}

function MessageDetailModal({
  message,
  isOpen,
  onClose,
  onRetry,
  onDiscard,
  isRetrying,
}: MessageDetailModalProps) {
  if (!isOpen || !message) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm"
      onClick={(e) => e.target === e.currentTarget && onClose()}
    >
      <div className="w-[700px] max-h-[85vh] bg-card border border-border rounded-xl shadow-2xl overflow-hidden flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border bg-muted/30">
          <div className="flex items-center gap-2">
            <AlertTriangle className="w-4 h-4 text-red-500" />
            <span className="font-medium text-foreground">消息详情</span>
            <StatusBadge status={message.status} />
          </div>
          <button
            onClick={onClose}
            className="p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted rounded-lg transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Content */}
        <div className="flex-1 overflow-y-auto p-4 space-y-4">
          {/* Basic Info */}
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="text-xs text-muted-foreground">消息 ID</label>
              <p className="text-sm font-mono text-foreground">{message.id}</p>
            </div>
            <div>
              <label className="text-xs text-muted-foreground">函数名称</label>
              <p className="text-sm text-foreground">{message.function_name || message.function_id}</p>
            </div>
            <div>
              <label className="text-xs text-muted-foreground">创建时间</label>
              <p className="text-sm text-foreground">{formatDate(message.created_at)}</p>
            </div>
            <div>
              <label className="text-xs text-muted-foreground">重试次数</label>
              <p className="text-sm text-foreground">{message.retry_count} 次</p>
            </div>
            {message.last_retry_at && (
              <div>
                <label className="text-xs text-muted-foreground">最后重试时间</label>
                <p className="text-sm text-foreground">{formatDate(message.last_retry_at)}</p>
              </div>
            )}
            {message.resolved_at && (
              <div>
                <label className="text-xs text-muted-foreground">解决时间</label>
                <p className="text-sm text-foreground">{formatDate(message.resolved_at)}</p>
              </div>
            )}
          </div>

          {/* Error Message */}
          <div>
            <label className="text-xs text-muted-foreground mb-1 block">错误信息</label>
            <div className="bg-red-500/10 border border-red-500/20 rounded-lg p-3">
              <pre className="text-sm text-red-400 whitespace-pre-wrap font-mono">{message.error}</pre>
            </div>
          </div>

          {/* Payload */}
          <div>
            <label className="text-xs text-muted-foreground mb-1 block">请求载荷</label>
            <div className="bg-muted/50 rounded-lg p-3 max-h-48 overflow-auto">
              <pre className="text-sm text-foreground whitespace-pre-wrap font-mono">
                {JSON.stringify(message.payload, null, 2)}
              </pre>
            </div>
          </div>
        </div>

        {/* Footer */}
        {message.status === 'pending' && (
          <div className="flex items-center justify-end gap-3 px-4 py-3 border-t border-border bg-muted/30">
            <button
              onClick={() => onDiscard(message.id)}
              disabled={isRetrying}
              className="px-4 py-2 text-sm text-muted-foreground hover:text-foreground transition-colors rounded-lg"
            >
              丢弃
            </button>
            <button
              onClick={() => onRetry(message.id)}
              disabled={isRetrying}
              className={cn(
                'inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg transition-colors',
                'bg-accent text-accent-foreground hover:bg-accent/90',
                isRetrying && 'opacity-50 cursor-not-allowed'
              )}
            >
              <RotateCcw className={cn('w-4 h-4', isRetrying && 'animate-spin')} />
              {isRetrying ? '重试中...' : '重试'}
            </button>
          </div>
        )}
      </div>
    </div>
  )
}

// 主页面
export default function DLQPage() {
  const toast = useToast()
  const [messages, setMessages] = useState<DeadLetterMessage[]>([])
  const [functions, setFunctions] = useState<Function[]>([])
  const [loading, setLoading] = useState(true)
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)
  const limit = 20

  // 筛选
  const [filterFunctionId, setFilterFunctionId] = useState<string>('')
  const [filterStatus, setFilterStatus] = useState<string>('')

  // 统计
  const [stats, setStats] = useState({
    total: 0,
    pending: 0,
    retrying: 0,
    resolved: 0,
    discarded: 0,
  })

  // 详情 Modal
  const [selectedMessage, setSelectedMessage] = useState<DeadLetterMessage | null>(null)
  const [showDetail, setShowDetail] = useState(false)
  const [isRetrying, setIsRetrying] = useState(false)

  // 批量选择
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())

  // 加载数据
  const fetchData = async () => {
    setLoading(true)
    try {
      const [dlqRes, statsRes, fnRes] = await Promise.all([
        dlqService.list({
          offset: (page - 1) * limit,
          limit,
          function_id: filterFunctionId || undefined,
          status: filterStatus || undefined,
        }),
        dlqService.stats(),
        functionService.list({ page: 1, limit: 100 }),
      ])
      setMessages(dlqRes.messages || [])
      setTotal(dlqRes.total)
      // 从消息列表中计算各状态的数量
      const allMessages = dlqRes.messages || []
      setStats({
        total: statsRes.total || 0,
        pending: allMessages.filter(m => m.status === 'pending').length,
        retrying: allMessages.filter(m => m.status === 'retrying').length,
        resolved: allMessages.filter(m => m.status === 'resolved').length,
        discarded: allMessages.filter(m => m.status === 'discarded').length,
      })
      setFunctions(fnRes.functions || [])
    } catch (error) {
      console.error('Failed to fetch DLQ data:', error)
      toast.error('加载死信队列失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchData()
  }, [page, filterFunctionId, filterStatus])

  // 重试消息
  const handleRetry = async (id: string) => {
    setIsRetrying(true)
    try {
      await dlqService.retry(id)
      toast.success('重试请求已发送')
      setShowDetail(false)
      fetchData()
    } catch (error) {
      console.error('Failed to retry message:', error)
      toast.error('重试失败')
    } finally {
      setIsRetrying(false)
    }
  }

  // 丢弃消息
  const handleDiscard = async (id: string) => {
    try {
      await dlqService.discard(id)
      toast.success('消息已丢弃')
      setShowDetail(false)
      fetchData()
    } catch (error) {
      console.error('Failed to discard message:', error)
      toast.error('丢弃失败')
    }
  }

  // 批量重试
  const handleBatchRetry = async () => {
    if (selectedIds.size === 0) return

    const ids = Array.from(selectedIds)
    let successCount = 0

    for (const id of ids) {
      try {
        await dlqService.retry(id)
        successCount++
      } catch (error) {
        console.error(`Failed to retry message ${id}:`, error)
      }
    }

    toast.success(`已重试 ${successCount}/${ids.length} 条消息`)
    setSelectedIds(new Set())
    fetchData()
  }

  // 批量丢弃
  const handleBatchDiscard = async () => {
    if (selectedIds.size === 0) return

    if (!confirm(`确定要丢弃选中的 ${selectedIds.size} 条消息吗？此操作不可撤销。`)) {
      return
    }

    const ids = Array.from(selectedIds)
    let successCount = 0

    for (const id of ids) {
      try {
        await dlqService.discard(id)
        successCount++
      } catch (error) {
        console.error(`Failed to discard message ${id}:`, error)
      }
    }

    toast.success(`已丢弃 ${successCount}/${ids.length} 条消息`)
    setSelectedIds(new Set())
    fetchData()
  }

  // 清空队列
  const handlePurge = async () => {
    if (!confirm('确定要清空所有死信消息吗？此操作不可撤销。')) {
      return
    }

    try {
      const result = await dlqService.purge()
      toast.success(`已删除 ${result.deleted} 条消息`)
      fetchData()
    } catch (error) {
      console.error('Failed to purge DLQ:', error)
      toast.error('清空失败')
    }
  }

  // 切换选择
  const toggleSelect = (id: string) => {
    const newSet = new Set(selectedIds)
    if (newSet.has(id)) {
      newSet.delete(id)
    } else {
      newSet.add(id)
    }
    setSelectedIds(newSet)
  }

  // 全选/取消全选
  const toggleSelectAll = () => {
    if (selectedIds.size === messages.filter(m => m.status === 'pending').length) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(messages.filter(m => m.status === 'pending').map(m => m.id)))
    }
  }

  // Loading skeleton
  if (loading && messages.length === 0) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <Skeleton className="h-8 w-32" />
          <Skeleton className="h-10 w-24" />
        </div>
        <div className="grid grid-cols-4 gap-4">
          {[...Array(4)].map((_, i) => (
            <Skeleton key={i} className="h-24" />
          ))}
        </div>
        <Skeleton className="h-64" />
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-display font-bold text-foreground">死信队列</h1>
          <p className="text-sm text-muted-foreground mt-1">管理执行失败的函数调用消息</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={fetchData}
            disabled={loading}
            className="inline-flex items-center gap-2 px-3 py-2 text-sm text-muted-foreground hover:text-foreground border border-border rounded-lg hover:bg-muted transition-colors"
          >
            <RefreshCw className={cn('w-4 h-4', loading && 'animate-spin')} />
            刷新
          </button>
          {stats.total > 0 && (
            <button
              onClick={handlePurge}
              className="inline-flex items-center gap-2 px-3 py-2 text-sm text-red-500 hover:text-red-400 border border-red-500/30 rounded-lg hover:bg-red-500/10 transition-colors"
            >
              <Trash2 className="w-4 h-4" />
              清空
            </button>
          )}
        </div>
      </div>

      {/* Stats Cards */}
      <div className="grid grid-cols-4 gap-4">
        <StatsCard
          title="总消息数"
          value={stats.total}
          icon={Archive}
          color="bg-muted text-muted-foreground"
        />
        <StatsCard
          title="待处理"
          value={stats.pending}
          icon={Clock}
          color="bg-yellow-500/10 text-yellow-500"
        />
        <StatsCard
          title="已解决"
          value={stats.resolved}
          icon={CheckCircle2}
          color="bg-green-500/10 text-green-500"
        />
        <StatsCard
          title="已丢弃"
          value={stats.discarded}
          icon={XCircle}
          color="bg-gray-500/10 text-gray-500"
        />
      </div>

      {/* Filters */}
      <div className="flex items-center gap-4 p-4 bg-card border border-border rounded-lg">
        <Filter className="w-4 h-4 text-muted-foreground" />
        <select
          value={filterFunctionId}
          onChange={(e) => {
            setFilterFunctionId(e.target.value)
            setPage(1)
          }}
          className="bg-input border border-border rounded-lg px-3 py-1.5 text-sm outline-none focus:ring-2 focus:ring-accent/50"
        >
          <option value="">所有函数</option>
          {functions.map((fn) => (
            <option key={fn.id} value={fn.id}>
              {fn.name}
            </option>
          ))}
        </select>
        <select
          value={filterStatus}
          onChange={(e) => {
            setFilterStatus(e.target.value)
            setPage(1)
          }}
          className="bg-input border border-border rounded-lg px-3 py-1.5 text-sm outline-none focus:ring-2 focus:ring-accent/50"
        >
          <option value="">所有状态</option>
          <option value="pending">待处理</option>
          <option value="retrying">重试中</option>
          <option value="resolved">已解决</option>
          <option value="discarded">已丢弃</option>
        </select>

        {/* Batch Actions */}
        {selectedIds.size > 0 && (
          <div className="flex items-center gap-2 ml-auto">
            <span className="text-sm text-muted-foreground">已选择 {selectedIds.size} 条</span>
            <button
              onClick={handleBatchRetry}
              className="inline-flex items-center gap-1 px-3 py-1.5 text-sm bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors"
            >
              <RotateCcw className="w-3.5 h-3.5" />
              批量重试
            </button>
            <button
              onClick={handleBatchDiscard}
              className="inline-flex items-center gap-1 px-3 py-1.5 text-sm text-red-500 border border-red-500/30 rounded-lg hover:bg-red-500/10 transition-colors"
            >
              <Trash2 className="w-3.5 h-3.5" />
              批量丢弃
            </button>
          </div>
        )}
      </div>

      {/* Message List */}
      {messages.length === 0 ? (
        <EmptyState
          type="general"
          title="暂无死信消息"
          description="当函数执行失败时，相关消息会出现在这里"
        />
      ) : (
        <div className="bg-card border border-border rounded-lg overflow-hidden">
          <table className="w-full">
            <thead className="bg-muted/50">
              <tr>
                <th className="w-10 px-4 py-3">
                  <input
                    type="checkbox"
                    checked={
                      selectedIds.size > 0 &&
                      selectedIds.size === messages.filter(m => m.status === 'pending').length
                    }
                    onChange={toggleSelectAll}
                    className="rounded border-border"
                  />
                </th>
                <th className="px-4 py-3 text-left text-xs font-medium text-muted-foreground uppercase">
                  消息 ID
                </th>
                <th className="px-4 py-3 text-left text-xs font-medium text-muted-foreground uppercase">
                  函数
                </th>
                <th className="px-4 py-3 text-left text-xs font-medium text-muted-foreground uppercase">
                  错误信息
                </th>
                <th className="px-4 py-3 text-left text-xs font-medium text-muted-foreground uppercase">
                  重试次数
                </th>
                <th className="px-4 py-3 text-left text-xs font-medium text-muted-foreground uppercase">
                  状态
                </th>
                <th className="px-4 py-3 text-left text-xs font-medium text-muted-foreground uppercase">
                  创建时间
                </th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground uppercase">
                  操作
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {messages.map((msg) => (
                <tr key={msg.id} className="hover:bg-muted/30 transition-colors">
                  <td className="px-4 py-3">
                    {msg.status === 'pending' && (
                      <input
                        type="checkbox"
                        checked={selectedIds.has(msg.id)}
                        onChange={() => toggleSelect(msg.id)}
                        className="rounded border-border"
                      />
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <span className="text-sm font-mono text-foreground">{msg.id.slice(0, 8)}...</span>
                  </td>
                  <td className="px-4 py-3">
                    <span className="text-sm text-foreground">{msg.function_name || '-'}</span>
                  </td>
                  <td className="px-4 py-3 max-w-xs">
                    <span className="text-sm text-red-400 truncate block">{msg.error}</span>
                  </td>
                  <td className="px-4 py-3">
                    <span className="text-sm text-foreground">{msg.retry_count}</span>
                  </td>
                  <td className="px-4 py-3">
                    <StatusBadge status={msg.status} />
                  </td>
                  <td className="px-4 py-3">
                    <span className="text-sm text-muted-foreground">{formatDate(msg.created_at)}</span>
                  </td>
                  <td className="px-4 py-3 text-right">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={() => {
                          setSelectedMessage(msg)
                          setShowDetail(true)
                        }}
                        className="p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted rounded transition-colors"
                        title="查看详情"
                      >
                        <Eye className="w-4 h-4" />
                      </button>
                      {msg.status === 'pending' && (
                        <>
                          <button
                            onClick={() => handleRetry(msg.id)}
                            className="p-1.5 text-muted-foreground hover:text-accent hover:bg-accent/10 rounded transition-colors"
                            title="重试"
                          >
                            <RotateCcw className="w-4 h-4" />
                          </button>
                          <button
                            onClick={() => handleDiscard(msg.id)}
                            className="p-1.5 text-muted-foreground hover:text-red-500 hover:bg-red-500/10 rounded transition-colors"
                            title="丢弃"
                          >
                            <Trash2 className="w-4 h-4" />
                          </button>
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {total > limit && (
        <Pagination page={page} pageSize={limit} total={total} onChange={setPage} />
      )}

      {/* Detail Modal */}
      <MessageDetailModal
        message={selectedMessage}
        isOpen={showDetail}
        onClose={() => setShowDetail(false)}
        onRetry={handleRetry}
        onDiscard={handleDiscard}
        isRetrying={isRetrying}
      />
    </div>
  )
}
