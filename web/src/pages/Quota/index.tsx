import { useState, useEffect } from 'react'
import {
  Gauge,
  RefreshCw,
  Code2,
  Cpu,
  Zap,
  HardDrive,
  TrendingUp,
  AlertTriangle,
} from 'lucide-react'
import { cn } from '../../utils'
import { quotaService, type QuotaUsage } from '../../services/quota'
import { useToast } from '../../components/Toast'

interface QuotaCardProps {
  title: string
  icon: React.ReactNode
  current: number
  max: number
  percent: number
  unit: string
  description: string
}

function QuotaCard({ title, icon, current, max, percent, unit, description }: QuotaCardProps) {
  const getStatusColor = (percent: number) => {
    if (percent >= 90) return 'text-red-400'
    if (percent >= 70) return 'text-yellow-400'
    return 'text-green-400'
  }

  const getProgressColor = (percent: number) => {
    if (percent >= 90) return 'bg-red-500'
    if (percent >= 70) return 'bg-yellow-500'
    return 'bg-green-500'
  }

  const getStatusBg = (percent: number) => {
    if (percent >= 90) return 'bg-red-500/10 border-red-500/20'
    if (percent >= 70) return 'bg-yellow-500/10 border-yellow-500/20'
    return 'bg-green-500/10 border-green-500/20'
  }

  return (
    <div className="bg-card rounded-xl border border-border p-6">
      <div className="flex items-start justify-between mb-4">
        <div className="flex items-center gap-3">
          <div className={cn('p-2 rounded-lg border', getStatusBg(percent))}>
            {icon}
          </div>
          <div>
            <h3 className="text-sm font-medium text-foreground">{title}</h3>
            <p className="text-xs text-muted-foreground">{description}</p>
          </div>
        </div>
        {percent >= 70 && (
          <AlertTriangle className={cn('w-5 h-5', getStatusColor(percent))} />
        )}
      </div>

      <div className="space-y-3">
        <div className="flex items-end justify-between">
          <div>
            <span className="text-3xl font-bold text-foreground">{current.toLocaleString()}</span>
            <span className="text-muted-foreground ml-1">/ {max.toLocaleString()} {unit}</span>
          </div>
          <span className={cn('text-lg font-semibold', getStatusColor(percent))}>
            {percent.toFixed(1)}%
          </span>
        </div>

        <div className="w-full bg-secondary rounded-full h-2 overflow-hidden">
          <div
            className={cn('h-full rounded-full transition-all duration-500', getProgressColor(percent))}
            style={{ width: `${Math.min(percent, 100)}%` }}
          />
        </div>

        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>已使用</span>
          <span>剩余: {(max - current).toLocaleString()} {unit}</span>
        </div>
      </div>
    </div>
  )
}

export default function Quota() {
  const toast = useToast()
  const [usage, setUsage] = useState<QuotaUsage | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    loadUsage()
  }, [])

  const loadUsage = async () => {
    try {
      setLoading(true)
      const data = await quotaService.getUsage()
      setUsage(data)
    } catch (err) {
      console.error('Failed to load quota usage:', err)
      toast.error('加载配额信息失败')
    } finally {
      setLoading(false)
    }
  }

  const getOverallStatus = () => {
    if (!usage) return { status: 'normal', message: '加载中...' }

    const maxPercent = Math.max(
      usage.function_usage_percent,
      usage.memory_usage_percent,
      usage.invocation_usage_percent,
      usage.code_usage_percent
    )

    if (maxPercent >= 90) {
      return {
        status: 'critical',
        message: '部分资源接近配额上限，请及时处理',
        color: 'text-red-400',
        bg: 'bg-red-500/10 border-red-500/20',
      }
    }
    if (maxPercent >= 70) {
      return {
        status: 'warning',
        message: '部分资源使用较高，请关注使用情况',
        color: 'text-yellow-400',
        bg: 'bg-yellow-500/10 border-yellow-500/20',
      }
    }
    return {
      status: 'normal',
      message: '所有资源使用正常',
      color: 'text-green-400',
      bg: 'bg-green-500/10 border-green-500/20',
    }
  }

  const overallStatus = getOverallStatus()

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-display font-bold text-foreground">配额管理</h1>
          <p className="text-muted-foreground mt-1">
            查看系统资源使用情况和配额限制
          </p>
        </div>
        <button
          onClick={loadUsage}
          disabled={loading}
          className="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg bg-accent text-accent-foreground hover:bg-accent/90 transition-colors"
        >
          <RefreshCw className={cn('w-4 h-4', loading && 'animate-spin')} />
          刷新
        </button>
      </div>

      {/* Overall Status */}
      {usage && (
        <div className={cn('rounded-xl border p-4 flex items-center gap-4', overallStatus.bg)}>
          <div className={cn('p-2 rounded-lg', overallStatus.bg)}>
            <Gauge className={cn('w-6 h-6', overallStatus.color)} />
          </div>
          <div>
            <h3 className={cn('font-medium', overallStatus.color)}>
              {overallStatus.status === 'critical' && '警告：'}
              {overallStatus.status === 'warning' && '注意：'}
              系统状态
            </h3>
            <p className="text-sm text-muted-foreground">{overallStatus.message}</p>
          </div>
        </div>
      )}

      {/* Loading State */}
      {loading ? (
        <div className="flex items-center justify-center py-16">
          <RefreshCw className="w-8 h-8 text-accent animate-spin" />
        </div>
      ) : usage ? (
        <>
          {/* Quota Cards */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            <QuotaCard
              title="函数数量"
              icon={<Code2 className="w-5 h-5 text-blue-400" />}
              current={usage.function_count}
              max={usage.max_functions}
              percent={usage.function_usage_percent}
              unit="个"
              description="已创建的函数总数"
            />
            <QuotaCard
              title="内存配额"
              icon={<Cpu className="w-5 h-5 text-purple-400" />}
              current={usage.total_memory_mb}
              max={usage.max_memory_mb}
              percent={usage.memory_usage_percent}
              unit="MB"
              description="所有函数的内存总和"
            />
            <QuotaCard
              title="今日调用"
              icon={<Zap className="w-5 h-5 text-yellow-400" />}
              current={usage.today_invocations}
              max={usage.max_invocations_per_day}
              percent={usage.invocation_usage_percent}
              unit="次"
              description="今日函数调用总次数"
            />
            <QuotaCard
              title="代码存储"
              icon={<HardDrive className="w-5 h-5 text-green-400" />}
              current={Math.round(usage.total_code_size_kb / 1024 * 10) / 10}
              max={Math.round(usage.max_code_size_kb / 1024)}
              percent={usage.code_usage_percent}
              unit="MB"
              description="所有函数代码包总大小"
            />
          </div>

          {/* Usage Tips */}
          <div className="bg-card rounded-xl border border-border p-6">
            <div className="flex items-center gap-3 mb-4">
              <div className="p-2 bg-accent/10 rounded-lg">
                <TrendingUp className="w-5 h-5 text-accent" />
              </div>
              <h3 className="text-lg font-semibold text-foreground">优化建议</h3>
            </div>
            <ul className="space-y-3 text-sm text-muted-foreground">
              {usage.function_usage_percent >= 70 && (
                <li className="flex items-start gap-2">
                  <span className="text-yellow-400 mt-0.5">•</span>
                  <span>函数数量较多，建议清理不再使用的函数以释放配额</span>
                </li>
              )}
              {usage.memory_usage_percent >= 70 && (
                <li className="flex items-start gap-2">
                  <span className="text-yellow-400 mt-0.5">•</span>
                  <span>内存使用较高，可以考虑降低部分函数的内存配置</span>
                </li>
              )}
              {usage.invocation_usage_percent >= 70 && (
                <li className="flex items-start gap-2">
                  <span className="text-yellow-400 mt-0.5">•</span>
                  <span>今日调用量较大，请关注是否有异常调用</span>
                </li>
              )}
              {usage.code_usage_percent >= 70 && (
                <li className="flex items-start gap-2">
                  <span className="text-yellow-400 mt-0.5">•</span>
                  <span>代码存储空间使用较高，建议清理旧版本或使用层来复用代码</span>
                </li>
              )}
              {Math.max(
                usage.function_usage_percent,
                usage.memory_usage_percent,
                usage.invocation_usage_percent,
                usage.code_usage_percent
              ) < 70 && (
                <li className="flex items-start gap-2">
                  <span className="text-green-400 mt-0.5">•</span>
                  <span>当前资源使用健康，无需特别优化</span>
                </li>
              )}
            </ul>
          </div>
        </>
      ) : (
        <div className="text-center py-16 text-muted-foreground">
          <Gauge className="w-12 h-12 mx-auto mb-3 text-muted-foreground/30" />
          <p>无法加载配额信息</p>
          <button
            onClick={loadUsage}
            className="mt-2 text-sm text-accent hover:underline"
          >
            重新加载
          </button>
        </div>
      )}
    </div>
  )
}
