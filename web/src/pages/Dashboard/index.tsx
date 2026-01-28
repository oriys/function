import { useState, useMemo } from 'react'
import {
  Activity,
  CheckCircle2,
  Clock,
  Zap,
  TrendingUp,
  TrendingDown,
  RefreshCw,
  AlertCircle,
  ArrowRight,
  Server,
} from 'lucide-react'
import ReactECharts from 'echarts-for-react'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { metricsService } from '../../services'
import type { TrendDataPoint, SystemStatus, PoolStats } from '../../types'
import { formatNumber, formatPercent, cn } from '../../utils'
import echarts from '../../utils/echarts'

// 格式化延迟显示
function formatLatency(ms: number): string {
  if (ms < 1) return '<1ms'
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

// 统计卡片组件
interface StatsCardProps {
  title: string
  value: string | number
  change?: number
  icon: React.ReactNode
  iconColor: string
  delay?: number
}

function StatsCard({ title, value, change, icon, iconColor, delay = 0 }: StatsCardProps) {
  return (
    <div
      className="bg-card rounded-xl border border-border p-4 card-hover hover:border-accent/30 transition-all duration-300"
      style={{ animationDelay: `${delay}ms` }}
    >
      <div className="flex items-center gap-3">
        <div className={cn('flex h-9 w-9 items-center justify-center rounded-lg transition-transform duration-300 group-hover:scale-110', iconColor)}>
          {icon}
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-xs text-muted-foreground">{title}</p>
          <p className="text-xl font-display font-bold text-foreground font-metric">{value}</p>
        </div>
        {change !== undefined && (
          <div className={cn(
            'flex items-center gap-0.5 text-xs font-medium px-2 py-1 rounded-full',
            change >= 0 ? 'text-emerald-500 bg-emerald-500/10' : 'text-rose-500 bg-rose-500/10'
          )}>
            {change >= 0 ? <TrendingUp className="w-3 h-3" /> : <TrendingDown className="w-3 h-3" />}
            <span>{change >= 0 ? '+' : ''}{formatPercent(change)}</span>
          </div>
        )}
      </div>
    </div>
  )
}

// 趋势图组件
function TrendChart({ data }: { data: TrendDataPoint[] }) {
  const option = useMemo(() => ({
    backgroundColor: 'transparent',
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'cross' },
      backgroundColor: 'rgba(17, 17, 17, 0.95)',
      borderColor: 'rgba(255, 255, 255, 0.1)',
      borderRadius: 8,
      padding: [8, 12],
      textStyle: { color: '#fff', fontSize: 11 },
      formatter: (params: any[]) => {
        const time = params[0]?.axisValue || ''
        let html = `<div style="font-weight:600;margin-bottom:4px;font-size:11px">${time}</div>`
        params.forEach((item: any) => {
          const color = item.color
          const value = item.seriesName === '平均延迟' ? formatLatency(item.value) : item.value
          html += `<div style="display:flex;align-items:center;gap:6px;margin:2px 0;font-size:11px">
            <span style="display:inline-block;width:6px;height:6px;border-radius:50%;background:${color}"></span>
            <span style="color:rgba(255,255,255,0.7)">${item.seriesName}</span>
            <span style="margin-left:auto;font-weight:600">${value}</span>
          </div>`
        })
        return html
      },
    },
    legend: {
      data: ['调用数', '错误数', '平均延迟'],
      bottom: 0,
      textStyle: { color: 'rgba(255, 255, 255, 0.5)', fontSize: 10 },
      itemWidth: 12,
      itemHeight: 8,
    },
    grid: { left: '1%', right: '1%', bottom: '18%', top: '5%', containLabel: true },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: data.map(d => {
        const date = new Date(d.timestamp)
        return `${date.getHours()}:${String(date.getMinutes()).padStart(2, '0')}`
      }),
      axisLine: { lineStyle: { color: 'rgba(255, 255, 255, 0.1)' } },
      axisLabel: { color: 'rgba(255, 255, 255, 0.4)', fontSize: 10 },
    },
    yAxis: [
      { type: 'value', splitLine: { lineStyle: { color: 'rgba(255, 255, 255, 0.05)' } }, axisLabel: { fontSize: 10 } },
      { type: 'value', splitLine: { show: false }, axisLabel: { fontSize: 10 } },
    ],
    series: [
      {
        name: '调用数',
        type: 'line',
        smooth: true,
        symbol: 'none',
        data: data.map(d => d.invocations),
        itemStyle: { color: '#34d399' },
        areaStyle: {
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: 'rgba(52, 211, 153, 0.3)' },
            { offset: 1, color: 'rgba(52, 211, 153, 0)' }
          ])
        },
      },
      {
        name: '错误数',
        type: 'line',
        smooth: true,
        symbol: 'none',
        data: data.map(d => d.errors),
        itemStyle: { color: '#f87171' },
      },
      {
        name: '平均延迟',
        type: 'line',
        smooth: true,
        symbol: 'none',
        yAxisIndex: 1,
        data: data.map(d => d.avg_latency_ms),
        itemStyle: { color: '#fbbf24' },
      },
    ],
  }), [data])

  return <ReactECharts echarts={echarts} option={option} style={{ height: '180px', width: '100%' }} />
}

// 虚拟机池迷你图表
function PoolMiniChart({ pool }: { pool: PoolStats }) {
  const option = useMemo(() => ({
    tooltip: { show: false },
    series: [
      {
        type: 'pie',
        radius: ['65%', '85%'],
        avoidLabelOverlap: false,
        label: { show: false },
        data: [
          { value: pool.warm_vms, name: '预热', itemStyle: { color: '#34d399' } },
          { value: pool.busy_vms, name: '忙碌', itemStyle: { color: '#fbbf24' } },
          { value: Math.max(0, pool.max_vms - pool.total_vms), name: '可用', itemStyle: { color: 'rgba(255,255,255,0.08)' } },
        ],
      },
    ],
  }), [pool])

  return <ReactECharts echarts={echarts} option={option} style={{ height: '56px', width: '56px' }} />
}

// 状态徽章
function StatusBadge({ status }: { status: string }) {
  const isSuccess = status === 'success' || status === 'completed'
  return (
    <span className={cn(
      'inline-flex items-center gap-1 text-xs font-medium',
      isSuccess ? 'text-emerald-500' : 'text-rose-500'
    )}>
      {isSuccess ? <CheckCircle2 className="w-3 h-3" /> : <AlertCircle className="w-3 h-3" />}
    </span>
  )
}

// 系统状态指示器
function SystemStatusIndicator({ status }: { status: SystemStatus | null }) {
  if (!status) return null
  const statusConfig = {
    healthy: { color: 'bg-emerald-500', text: '健康', textColor: 'text-emerald-500', bgColor: 'bg-emerald-500/10' },
    degraded: { color: 'bg-amber-500', text: '降级', textColor: 'text-amber-500', bgColor: 'bg-amber-500/10' },
    unhealthy: { color: 'bg-rose-500', text: '异常', textColor: 'text-rose-500', bgColor: 'bg-rose-500/10' },
  }
  const config = statusConfig[status.status] || statusConfig.unhealthy

  return (
    <div className="flex items-center gap-3 text-xs">
      <div className={cn('flex items-center gap-1.5 px-2 py-1 rounded-full', config.bgColor)}>
        <span className={cn('w-2 h-2 rounded-full status-pulse', config.color)} />
        <span className={cn('font-medium', config.textColor)}>{config.text}</span>
      </div>
      <div className="flex items-center gap-1 text-muted-foreground">
        <Server className="w-3 h-3" />
        <span className="font-mono">{status.version}</span>
      </div>
    </div>
  )
}

export default function Dashboard() {
  const [period, setPeriod] = useState('24h')

  // 使用 React Query 获取统计数据
  const { data: stats, isLoading: statsLoading } = useQuery({
    queryKey: ['dashboard-stats', period],
    queryFn: () => metricsService.getDashboardStats(period),
    refetchInterval: 30000, // 30秒刷新一次
  })

  // 趋势数据
  const { data: trends = [], isLoading: trendsLoading } = useQuery({
    queryKey: ['dashboard-trends', period],
    queryFn: () => metricsService.getInvocationTrends(period, period === '24h' ? '1h' : '1d'),
    refetchInterval: 60000,
  })

  // 热门函数
  const { data: topFunctions = [] } = useQuery({
    queryKey: ['dashboard-top-functions', period],
    queryFn: () => metricsService.getTopFunctions(period, 5),
  })

  // 最近调用
  const { data: recentInvocations = [] } = useQuery({
    queryKey: ['dashboard-recent-invocations'],
    queryFn: () => metricsService.getRecentInvocations(5),
    refetchInterval: 10000,
  })

  // 系统状态
  const { data: systemStatus = null } = useQuery({
    queryKey: ['system-status'],
    queryFn: () => metricsService.getSystemStatus(),
    refetchInterval: 15000,
  })

  const loading = statsLoading || trendsLoading

  if (loading && !stats) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="flex flex-col items-center gap-3">
          <RefreshCw className="w-8 h-8 text-accent animate-spin" />
          <span className="text-sm text-muted-foreground">加载中...</span>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-4 animate-fade-in">
      {/* 页头 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-4">
          <h1 className="text-xl font-display font-semibold text-accent">概览</h1>
          <SystemStatusIndicator status={systemStatus} />
        </div>
        <div className="flex items-center gap-2">
          <select
            value={period}
            onChange={(e) => setPeriod(e.target.value)}
            className="px-3 py-1.5 bg-card border border-border rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-accent/30 transition-all"
          >
            <option value="1h">1 小时</option>
            <option value="6h">6 小时</option>
            <option value="24h">24 小时</option>
            <option value="7d">7 天</option>
          </select>
        </div>
      </div>

      {/* 统计卡片 */}
      <div className="grid grid-cols-4 gap-3">
        <StatsCard
          title="总调用数"
          value={formatNumber(stats?.total_invocations || 0)}
          change={stats?.invocations_change}
          icon={<Activity className="w-4 h-4 text-violet-400" />}
          iconColor="bg-violet-500/15"
          delay={0}
        />
        <StatsCard
          title="成功率"
          value={formatPercent(stats?.success_rate || 0)}
          change={stats?.success_rate_change}
          icon={<CheckCircle2 className="w-4 h-4 text-emerald-400" />}
          iconColor="bg-emerald-500/15"
          delay={50}
        />
        <StatsCard
          title="P99 延迟"
          value={formatLatency(stats?.p99_latency_ms || 0)}
          change={stats?.latency_change}
          icon={<Clock className="w-4 h-4 text-blue-400" />}
          iconColor="bg-blue-500/15"
          delay={100}
        />
        <StatsCard
          title="冷启动率"
          value={formatPercent(stats?.cold_start_rate || 0)}
          change={stats?.cold_start_change}
          icon={<Zap className="w-4 h-4 text-amber-400" />}
          iconColor="bg-amber-500/15"
          delay={150}
        />
      </div>

      <div className="grid grid-cols-12 gap-3">
        <div className="col-span-6 bg-card rounded-xl border border-border p-4 card-hover">
          <h2 className="text-sm font-display font-medium text-foreground mb-2">调用趋势</h2>
          {trends.length > 0 ? <TrendChart data={trends} /> : <div className="h-44 flex items-center justify-center text-muted-foreground text-sm">暂无数据</div>}
        </div>

        <div className="col-span-3 bg-card rounded-xl border border-border p-4 card-hover">
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-sm font-display font-medium text-foreground">热门函数</h2>
            <Link to="/functions" className="text-xs text-accent hover:text-accent/80 flex items-center gap-0.5 transition-colors">全部 <ArrowRight className="w-3 h-3" /></Link>
          </div>
          <div className="space-y-2">
            {topFunctions.map((fn, i) => (
              <div key={i} className="flex items-center gap-2 group">
                <span className="w-4 text-xs text-muted-foreground font-mono">{i + 1}</span>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center justify-between mb-1">
                    <span className="text-xs text-foreground truncate group-hover:text-accent transition-colors">{fn.function_name}</span>
                    <span className="text-xs text-muted-foreground ml-2 font-mono">{fn.invocations}</span>
                  </div>
                  <div className="w-full bg-secondary rounded-full h-1 overflow-hidden">
                    <div className="bg-accent h-1 rounded-full transition-all duration-500" style={{ width: `${fn.percentage}%` }} />
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>

        <div className="col-span-3 bg-card rounded-xl border border-border p-4 card-hover">
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-sm font-display font-medium text-foreground">实例池</h2>
            <Link to="/metrics" className="text-xs text-accent hover:text-accent/80 flex items-center gap-0.5 transition-colors">详情 <ArrowRight className="w-3 h-3" /></Link>
          </div>
          <div className="space-y-2">
            {systemStatus?.pool_stats?.map((pool) => (
              <div key={pool.runtime} className="flex items-center gap-3 group">
                <PoolMiniChart pool={pool} />
                <div className="flex-1 min-w-0">
                  <p className="text-xs font-medium text-foreground truncate group-hover:text-accent transition-colors">{pool.runtime}</p>
                  <div className="flex items-center gap-2 text-xs text-muted-foreground mt-0.5">
                    <span className="flex items-center gap-1"><span className="w-1.5 h-1.5 rounded-full bg-emerald-500" />{pool.warm_vms}</span>
                    <span className="flex items-center gap-1"><span className="w-1.5 h-1.5 rounded-full bg-amber-500" />{pool.busy_vms}</span>
                    <span className="text-muted-foreground/50 font-mono">{pool.total_vms}/{pool.max_vms}</span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>

      <div className="bg-card rounded-xl border border-border p-4 card-hover">
        <div className="flex items-center justify-between mb-3">
          <h2 className="text-sm font-display font-medium text-foreground">最近调用</h2>
          <Link to="/invocations" className="text-xs text-accent hover:text-accent/80 flex items-center gap-0.5 transition-colors">全部 <ArrowRight className="w-3 h-3" /></Link>
        </div>
        <div className="grid grid-cols-5 gap-3">
          {recentInvocations.map((inv, i) => (
            <div key={i} className="flex items-center gap-2 py-2 px-3 rounded-lg bg-secondary/30 hover:bg-secondary/50 transition-all cursor-pointer group">
              <StatusBadge status={inv.status} />
              <span className="text-xs text-foreground truncate flex-1 group-hover:text-accent transition-colors">{inv.function_name}</span>
              <span className="text-xs text-muted-foreground font-mono">{formatLatency(inv.duration_ms)}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}