import { useState, useEffect, useRef } from 'react'
import { Terminal, Trash2, StopCircle, Play, ChevronDown } from 'lucide-react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { cn } from '../../utils'
import type { LogEntry } from '../../types'

interface LogStreamViewerProps {
  functionId: string
  className?: string
}

export default function LogStreamViewer({ functionId, className }: LogStreamViewerProps) {
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [connected, setConnected] = useState(false)
  const [paused, setPaused] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const virtuosoRef = useRef<VirtuosoHandle>(null)
  const wsRef = useRef<WebSocket | null>(null)

  useEffect(() => {
    if (paused) {
      if (wsRef.current) {
        wsRef.current.close()
        wsRef.current = null
      }
      setConnected(false)
      return
    }

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const host = window.location.hostname === 'localhost' ? 'localhost:8080' : window.location.host
    const wsUrl = `${protocol}//${host}/api/v1/console/logs/stream?function_id=${functionId}`

    const ws = new WebSocket(wsUrl)
    wsRef.current = ws

    ws.onopen = () => setConnected(true)
    ws.onclose = () => setConnected(false)
    ws.onmessage = (event) => {
      try {
        const log = JSON.parse(event.data) as LogEntry
        setLogs((prev) => [...prev.slice(-1999), log]) // 增加到 2000 条，虚拟滚动可以处理更多
      } catch (err) {
        console.error('Failed to parse log message:', err)
      }
    }

    return () => {
      ws.close()
    }
  }, [functionId, paused])

  // 自动滚动到底部
  useEffect(() => {
    if (autoScroll && logs.length > 0) {
      virtuosoRef.current?.scrollToIndex({
        index: logs.length - 1,
        behavior: 'auto',
      })
    }
  }, [logs, autoScroll])

  const clearLogs = () => setLogs([])

  return (
    <div className={cn('flex flex-col bg-slate-950 rounded-xl border border-slate-800 overflow-hidden font-mono', className)}>
      {/* 工具栏 */}
      <div className="flex items-center justify-between px-4 py-2 bg-slate-900 border-b border-slate-800 text-slate-400">
        <div className="flex items-center gap-3 text-xs">
          <div className="flex items-center gap-1.5">
            <div className={cn('w-2 h-2 rounded-full', connected ? 'bg-green-500 animate-pulse' : 'bg-slate-600')} />
            <span>{connected ? '正在接收实时日志...' : paused ? '日志已暂停' : '正在连接...'}</span>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setPaused(!paused)}
            className="p-1.5 hover:bg-slate-800 rounded-md transition-colors"
            title={paused ? '开始' : '暂停'}
          >
            {paused ? <Play className="w-4 h-4 text-green-400" /> : <StopCircle className="w-4 h-4 text-orange-400" />}
          </button>
          <button
            onClick={clearLogs}
            className="p-1.5 hover:bg-slate-800 rounded-md transition-colors"
            title="清空"
          >
            <Trash2 className="w-4 h-4" />
          </button>
          <div className="w-px h-4 bg-slate-800 mx-1" />
          <button
            onClick={() => setAutoScroll(!autoScroll)}
            className={cn(
              'flex items-center gap-1 px-2 py-1 rounded text-[10px] uppercase font-bold transition-colors',
              autoScroll ? 'bg-accent/20 text-accent' : 'hover:bg-slate-800'
            )}
          >
            自动滚动
            <ChevronDown className={cn('w-3 h-3 transition-transform', autoScroll ? 'rotate-0' : '-rotate-90')} />
          </button>
        </div>
      </div>

      {/* 日志内容 - 虚拟化渲染 */}
      <div className="flex-1 min-h-0">
        {logs.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-full text-slate-600 gap-2">
            <Terminal className="w-8 h-8 opacity-20" />
            <p className="text-xs">等待日志输出...</p>
          </div>
        ) : (
          <Virtuoso
            ref={virtuosoRef}
            data={logs}
            followOutput={autoScroll}
            className="scrollbar-thin scrollbar-thumb-slate-800 scrollbar-track-transparent"
            itemContent={(_index, log) => (
              <div className="group flex gap-3 py-0.5 px-4 hover:bg-slate-900/50">
                <span className="text-slate-600 flex-shrink-0 select-none text-xs">
                  {new Date(log.timestamp).toLocaleTimeString([], { hour12: false })}
                </span>
                <span className={cn(
                  'flex-shrink-0 w-12 font-bold text-[10px] mt-0.5 px-1 rounded flex items-center justify-center h-4',
                  log.level === 'ERROR' ? 'bg-red-500/20 text-red-400' :
                  log.level === 'WARN' ? 'bg-yellow-500/20 text-yellow-400' :
                  'bg-blue-500/20 text-blue-400'
                )}>
                  {log.level}
                </span>
                <span className="text-slate-300 break-all text-sm">{log.message}</span>
                {log.request_id && (
                  <span className="text-[10px] text-slate-600 opacity-0 group-hover:opacity-100 transition-opacity whitespace-nowrap">
                    ID: {log.request_id}
                  </span>
                )}
              </div>
            )}
          />
        )}
      </div>
    </div>
  )
}