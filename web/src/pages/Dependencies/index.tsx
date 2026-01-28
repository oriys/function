import { useState, useEffect, useRef } from 'react'
import {
  Network,
  RefreshCw,
  AlertTriangle,
  ChevronRight,
  Box,
  Workflow,
  ArrowRight,
  Info,
} from 'lucide-react'
import { cn } from '../../utils'
import {
  dependenciesService,
  type DependencyGraph,
  type ImpactAnalysis,
} from '../../services/dependencies'

type ViewMode = 'graph' | 'list'

const nodeStatusColors: Record<string, string> = {
  active: 'bg-green-500',
  inactive: 'bg-gray-500',
  error: 'bg-red-500',
  deploying: 'bg-yellow-500',
}

const dependencyTypeLabels: Record<string, string> = {
  direct_call: '直接调用',
  workflow: '工作流',
  http: 'HTTP 调用',
}

export default function Dependencies() {
  const [graph, setGraph] = useState<DependencyGraph | null>(null)
  const [loading, setLoading] = useState(true)
  const [viewMode, setViewMode] = useState<ViewMode>('list')
  const [selectedNode, setSelectedNode] = useState<string | null>(null)
  const [impactAnalysis, setImpactAnalysis] = useState<ImpactAnalysis | null>(null)
  const [loadingImpact, setLoadingImpact] = useState(false)

  useEffect(() => {
    loadGraph()
  }, [])

  useEffect(() => {
    if (selectedNode) {
      loadImpactAnalysis(selectedNode)
    } else {
      setImpactAnalysis(null)
    }
  }, [selectedNode])

  const loadGraph = async () => {
    setLoading(true)
    try {
      const data = await dependenciesService.getGraph()
      setGraph(data)
    } catch (err) {
      console.error('Failed to load dependency graph:', err)
    } finally {
      setLoading(false)
    }
  }

  const loadImpactAnalysis = async (functionId: string) => {
    setLoadingImpact(true)
    try {
      const data = await dependenciesService.getImpactAnalysis(functionId)
      setImpactAnalysis(data)
    } catch (err) {
      console.error('Failed to load impact analysis:', err)
    } finally {
      setLoadingImpact(false)
    }
  }

  const getNodeEdges = (nodeId: string) => {
    if (!graph) return { incoming: [], outgoing: [] }
    return {
      incoming: graph.edges.filter(e => e.target === nodeId),
      outgoing: graph.edges.filter(e => e.source === nodeId),
    }
  }

  const getNodeById = (id: string) => graph?.nodes.find(n => n.id === id)

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-display font-bold text-foreground">依赖分析</h1>
          <p className="text-muted-foreground mt-1">
            可视化函数之间的调用关系和依赖图
          </p>
        </div>
        <div className="flex items-center gap-2">
          <div className="flex items-center rounded-lg border border-border overflow-hidden">
            <button
              onClick={() => setViewMode('list')}
              className={cn(
                'px-3 py-2 text-sm font-medium transition-colors',
                viewMode === 'list' ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:text-foreground'
              )}
            >
              列表
            </button>
            <button
              onClick={() => setViewMode('graph')}
              className={cn(
                'px-3 py-2 text-sm font-medium transition-colors',
                viewMode === 'graph' ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:text-foreground'
              )}
            >
              图形
            </button>
          </div>
          <button
            onClick={loadGraph}
            disabled={loading}
            className="inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg border border-border text-muted-foreground hover:text-foreground hover:bg-secondary transition-colors"
          >
            <RefreshCw className={cn('w-4 h-4', loading && 'animate-spin')} />
            刷新
          </button>
        </div>
      </div>

      {/* Stats */}
      {graph && (
        <div className="grid grid-cols-3 gap-4">
          <div className="bg-card rounded-xl border border-border p-4">
            <div className="flex items-center gap-3">
              <div className="p-2 rounded-lg bg-blue-500/10">
                <Box className="w-5 h-5 text-blue-400" />
              </div>
              <div>
                <p className="text-2xl font-bold text-foreground">{graph.nodes.length}</p>
                <p className="text-sm text-muted-foreground">函数节点</p>
              </div>
            </div>
          </div>
          <div className="bg-card rounded-xl border border-border p-4">
            <div className="flex items-center gap-3">
              <div className="p-2 rounded-lg bg-purple-500/10">
                <ArrowRight className="w-5 h-5 text-purple-400" />
              </div>
              <div>
                <p className="text-2xl font-bold text-foreground">{graph.edges.length}</p>
                <p className="text-sm text-muted-foreground">依赖关系</p>
              </div>
            </div>
          </div>
          <div className="bg-card rounded-xl border border-border p-4">
            <div className="flex items-center gap-3">
              <div className="p-2 rounded-lg bg-orange-500/10">
                <Workflow className="w-5 h-5 text-orange-400" />
              </div>
              <div>
                <p className="text-2xl font-bold text-foreground">
                  {graph.edges.filter(e => e.type === 'workflow').length}
                </p>
                <p className="text-sm text-muted-foreground">工作流连接</p>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Content */}
      <div className="grid grid-cols-3 gap-6">
        {/* Main Content */}
        <div className="col-span-2 bg-card rounded-xl border border-border overflow-hidden">
          {loading ? (
            <div className="flex items-center justify-center py-16">
              <RefreshCw className="w-6 h-6 text-accent animate-spin" />
            </div>
          ) : !graph || graph.nodes.length === 0 ? (
            <div className="text-center py-16 text-muted-foreground">
              <Network className="w-12 h-12 mx-auto mb-3 text-muted-foreground/30" />
              <p>暂无依赖数据</p>
              <p className="text-sm mt-1">函数之间的调用关系将在此显示</p>
            </div>
          ) : viewMode === 'graph' ? (
            <DependencyGraphView
              graph={graph}
              selectedNode={selectedNode}
              onSelectNode={setSelectedNode}
            />
          ) : (
            <div className="divide-y divide-border">
              {graph.nodes.map((node) => {
                const edges = getNodeEdges(node.id)
                const isSelected = selectedNode === node.id
                return (
                  <div
                    key={node.id}
                    onClick={() => setSelectedNode(isSelected ? null : node.id)}
                    className={cn(
                      'p-4 cursor-pointer transition-colors',
                      isSelected ? 'bg-accent/5' : 'hover:bg-secondary/20'
                    )}
                  >
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-3">
                        <div className={cn(
                          'w-2 h-2 rounded-full',
                          nodeStatusColors[node.status] || 'bg-gray-500'
                        )} />
                        <div>
                          <h3 className="font-medium text-foreground">{node.name}</h3>
                          <p className="text-sm text-muted-foreground">
                            {node.runtime && <span className="mr-2">{node.runtime}</span>}
                            <span>{node.type}</span>
                          </p>
                        </div>
                      </div>
                      <div className="flex items-center gap-4 text-sm text-muted-foreground">
                        {edges.incoming.length > 0 && (
                          <span className="flex items-center gap-1">
                            <ArrowRight className="w-4 h-4 rotate-180" />
                            {edges.incoming.length} 被调用
                          </span>
                        )}
                        {edges.outgoing.length > 0 && (
                          <span className="flex items-center gap-1">
                            {edges.outgoing.length} 调用
                            <ArrowRight className="w-4 h-4" />
                          </span>
                        )}
                        <ChevronRight className={cn(
                          'w-4 h-4 transition-transform',
                          isSelected && 'rotate-90'
                        )} />
                      </div>
                    </div>
                    {isSelected && (
                      <div className="mt-4 pt-4 border-t border-border">
                        <div className="grid grid-cols-2 gap-4">
                          <div>
                            <h4 className="text-sm font-medium text-foreground mb-2">调用的函数</h4>
                            {edges.outgoing.length === 0 ? (
                              <p className="text-sm text-muted-foreground">无</p>
                            ) : (
                              <ul className="space-y-1">
                                {edges.outgoing.map((edge, idx) => {
                                  const target = getNodeById(edge.target)
                                  return (
                                    <li key={idx} className="flex items-center gap-2 text-sm">
                                      <ArrowRight className="w-3 h-3 text-muted-foreground" />
                                      <span className="text-foreground">{target?.name || edge.target}</span>
                                      <span className="text-muted-foreground">
                                        ({dependencyTypeLabels[edge.type] || edge.type}, {edge.call_count} 次)
                                      </span>
                                    </li>
                                  )
                                })}
                              </ul>
                            )}
                          </div>
                          <div>
                            <h4 className="text-sm font-medium text-foreground mb-2">被调用</h4>
                            {edges.incoming.length === 0 ? (
                              <p className="text-sm text-muted-foreground">无</p>
                            ) : (
                              <ul className="space-y-1">
                                {edges.incoming.map((edge, idx) => {
                                  const source = getNodeById(edge.source)
                                  return (
                                    <li key={idx} className="flex items-center gap-2 text-sm">
                                      <ArrowRight className="w-3 h-3 text-muted-foreground rotate-180" />
                                      <span className="text-foreground">{source?.name || edge.source}</span>
                                      <span className="text-muted-foreground">
                                        ({dependencyTypeLabels[edge.type] || edge.type}, {edge.call_count} 次)
                                      </span>
                                    </li>
                                  )
                                })}
                              </ul>
                            )}
                          </div>
                        </div>
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* Impact Analysis Sidebar */}
        <div className="bg-card rounded-xl border border-border overflow-hidden">
          <div className="p-4 border-b border-border">
            <h2 className="font-semibold text-foreground flex items-center gap-2">
              <AlertTriangle className="w-4 h-4 text-orange-400" />
              影响分析
            </h2>
          </div>
          {!selectedNode ? (
            <div className="p-4 text-center text-muted-foreground">
              <Info className="w-8 h-8 mx-auto mb-2 text-muted-foreground/30" />
              <p className="text-sm">选择一个函数查看其影响分析</p>
            </div>
          ) : loadingImpact ? (
            <div className="p-8 flex items-center justify-center">
              <RefreshCw className="w-5 h-5 text-accent animate-spin" />
            </div>
          ) : impactAnalysis ? (
            <div className="p-4 space-y-4">
              <div>
                <h3 className="text-sm font-medium text-foreground mb-1">
                  {impactAnalysis.function_name}
                </h3>
                <p className="text-xs text-muted-foreground">
                  总影响: {impactAnalysis.total_impact_count} 个组件
                </p>
              </div>

              <div>
                <h4 className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
                  直接依赖方 ({impactAnalysis.direct_dependents.length})
                </h4>
                {impactAnalysis.direct_dependents.length === 0 ? (
                  <p className="text-sm text-muted-foreground">无</p>
                ) : (
                  <ul className="space-y-1">
                    {impactAnalysis.direct_dependents.map((dep) => (
                      <li key={dep.id} className="flex items-center gap-2 text-sm">
                        <div className={cn(
                          'w-1.5 h-1.5 rounded-full',
                          nodeStatusColors[dep.status] || 'bg-gray-500'
                        )} />
                        <span className="text-foreground">{dep.name}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>

              <div>
                <h4 className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
                  间接依赖方 ({impactAnalysis.indirect_dependents.length})
                </h4>
                {impactAnalysis.indirect_dependents.length === 0 ? (
                  <p className="text-sm text-muted-foreground">无</p>
                ) : (
                  <ul className="space-y-1">
                    {impactAnalysis.indirect_dependents.map((dep) => (
                      <li key={dep.id} className="flex items-center gap-2 text-sm">
                        <div className={cn(
                          'w-1.5 h-1.5 rounded-full',
                          nodeStatusColors[dep.status] || 'bg-gray-500'
                        )} />
                        <span className="text-foreground">{dep.name}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>

              <div>
                <h4 className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-2">
                  受影响的工作流 ({impactAnalysis.affected_workflows.length})
                </h4>
                {impactAnalysis.affected_workflows.length === 0 ? (
                  <p className="text-sm text-muted-foreground">无</p>
                ) : (
                  <ul className="space-y-1">
                    {impactAnalysis.affected_workflows.map((wf, idx) => (
                      <li key={idx} className="flex items-center gap-2 text-sm">
                        <Workflow className="w-3 h-3 text-muted-foreground" />
                        <span className="text-foreground">{wf}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  )
}

// Simple Graph View Component
function DependencyGraphView({
  graph,
  selectedNode,
  onSelectNode,
}: {
  graph: DependencyGraph
  selectedNode: string | null
  onSelectNode: (id: string | null) => void
}) {
  const canvasRef = useRef<HTMLDivElement>(null)

  // Simple force-directed layout simulation
  const [positions, setPositions] = useState<Record<string, { x: number; y: number }>>({})

  useEffect(() => {
    if (!graph || graph.nodes.length === 0) return

    // Initialize positions in a circle
    const centerX = 400
    const centerY = 300
    const radius = Math.min(200, 50 * graph.nodes.length / Math.PI)

    const initialPositions: Record<string, { x: number; y: number }> = {}
    graph.nodes.forEach((node, i) => {
      const angle = (2 * Math.PI * i) / graph.nodes.length
      initialPositions[node.id] = {
        x: centerX + radius * Math.cos(angle),
        y: centerY + radius * Math.sin(angle),
      }
    })
    setPositions(initialPositions)
  }, [graph])

  if (Object.keys(positions).length === 0) {
    return (
      <div className="h-[600px] flex items-center justify-center">
        <RefreshCw className="w-6 h-6 text-accent animate-spin" />
      </div>
    )
  }

  return (
    <div ref={canvasRef} className="h-[600px] relative overflow-hidden">
      <svg className="w-full h-full">
        {/* Edges */}
        {graph.edges.map((edge, idx) => {
          const source = positions[edge.source]
          const target = positions[edge.target]
          if (!source || !target) return null

          const isHighlighted = selectedNode === edge.source || selectedNode === edge.target

          return (
            <g key={idx}>
              <line
                x1={source.x}
                y1={source.y}
                x2={target.x}
                y2={target.y}
                stroke={isHighlighted ? 'rgb(var(--accent))' : 'rgb(var(--border))'}
                strokeWidth={isHighlighted ? 2 : 1}
                markerEnd="url(#arrowhead)"
              />
            </g>
          )
        })}

        {/* Arrow marker definition */}
        <defs>
          <marker
            id="arrowhead"
            markerWidth="10"
            markerHeight="7"
            refX="9"
            refY="3.5"
            orient="auto"
          >
            <polygon
              points="0 0, 10 3.5, 0 7"
              fill="rgb(var(--muted-foreground))"
            />
          </marker>
        </defs>

        {/* Nodes */}
        {graph.nodes.map((node) => {
          const pos = positions[node.id]
          if (!pos) return null

          const isSelected = selectedNode === node.id

          return (
            <g
              key={node.id}
              onClick={() => onSelectNode(isSelected ? null : node.id)}
              className="cursor-pointer"
            >
              <circle
                cx={pos.x}
                cy={pos.y}
                r={isSelected ? 28 : 24}
                fill={isSelected ? 'rgb(var(--accent))' : 'rgb(var(--card))'}
                stroke={isSelected ? 'rgb(var(--accent))' : 'rgb(var(--border))'}
                strokeWidth={2}
              />
              <text
                x={pos.x}
                y={pos.y}
                textAnchor="middle"
                dominantBaseline="middle"
                fill={isSelected ? 'rgb(var(--accent-foreground))' : 'rgb(var(--foreground))'}
                fontSize="10"
                fontWeight={isSelected ? 600 : 400}
              >
                {node.name.length > 8 ? node.name.slice(0, 8) + '...' : node.name}
              </text>
              <circle
                cx={pos.x + 16}
                cy={pos.y - 16}
                r={4}
                fill={nodeStatusColors[node.status] || 'rgb(107, 114, 128)'}
              />
            </g>
          )
        })}
      </svg>

      {/* Legend */}
      <div className="absolute bottom-4 left-4 bg-card/90 backdrop-blur-sm rounded-lg border border-border p-3 text-xs">
        <div className="flex items-center gap-4">
          <div className="flex items-center gap-1.5">
            <div className="w-2 h-2 rounded-full bg-green-500" />
            <span className="text-muted-foreground">活跃</span>
          </div>
          <div className="flex items-center gap-1.5">
            <div className="w-2 h-2 rounded-full bg-gray-500" />
            <span className="text-muted-foreground">未激活</span>
          </div>
          <div className="flex items-center gap-1.5">
            <div className="w-2 h-2 rounded-full bg-red-500" />
            <span className="text-muted-foreground">错误</span>
          </div>
        </div>
      </div>
    </div>
  )
}
