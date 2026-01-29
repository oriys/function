import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import {
  Network,
  RefreshCw,
  AlertTriangle,
  ChevronRight,
  Box,
  Workflow,
  ArrowRight,
  Info,
  Search,
  Filter,
  X,
  ZoomIn,
  ZoomOut,
  Maximize2,
} from 'lucide-react'
import { cn } from '../../utils'
import {
  dependenciesService,
  type DependencyGraph,
  type DependencyNode,
  type DependencyEdge,
  type ImpactAnalysis,
} from '../../services/dependencies'

type ViewMode = 'graph' | 'list'

type FilterType = 'all' | 'has-deps' | 'no-deps' | 'workflow'

const nodeStatusColors: Record<string, string> = {
  active: 'bg-green-500',
  inactive: 'bg-gray-500',
  error: 'bg-red-500',
  deploying: 'bg-yellow-500',
}

const nodeStatusSvgColors: Record<string, string> = {
  active: '#22c55e',
  inactive: '#6b7280',
  error: '#ef4444',
  deploying: '#eab308',
}

const dependencyTypeLabels: Record<string, string> = {
  direct_call: '直接调用',
  workflow: '工作流',
  http: 'HTTP 调用',
}

const dependencyTypeColors: Record<string, string> = {
  direct_call: '#8b5cf6',
  workflow: '#f97316',
  http: '#06b6d4',
}

const filterOptions: { value: FilterType; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'has-deps', label: '有依赖' },
  { value: 'no-deps', label: '无依赖' },
  { value: 'workflow', label: '工作流相关' },
]

export default function Dependencies() {
  const [graph, setGraph] = useState<DependencyGraph | null>(null)
  const [loading, setLoading] = useState(true)
  const [viewMode, setViewMode] = useState<ViewMode>('list')
  const [selectedNode, setSelectedNode] = useState<string | null>(null)
  const [impactAnalysis, setImpactAnalysis] = useState<ImpactAnalysis | null>(null)
  const [loadingImpact, setLoadingImpact] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [filterType, setFilterType] = useState<FilterType>('all')
  const [showFilters, setShowFilters] = useState(false)

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

  const getNodeEdges = useCallback((nodeId: string) => {
    if (!graph) return { incoming: [], outgoing: [] }
    return {
      incoming: graph.edges.filter(e => e.target === nodeId),
      outgoing: graph.edges.filter(e => e.source === nodeId),
    }
  }, [graph])

  const getNodeById = useCallback((id: string) => graph?.nodes.find(n => n.id === id), [graph])

  // Filter nodes based on search and filter type
  const filteredNodes = useMemo(() => {
    if (!graph) return []

    let nodes = graph.nodes

    // Search filter
    if (searchQuery) {
      const query = searchQuery.toLowerCase()
      nodes = nodes.filter(n =>
        n.name.toLowerCase().includes(query) ||
        n.runtime?.toLowerCase().includes(query)
      )
    }

    // Type filter
    if (filterType !== 'all') {
      nodes = nodes.filter(node => {
        const edges = getNodeEdges(node.id)
        const hasDeps = edges.incoming.length > 0 || edges.outgoing.length > 0
        const hasWorkflow = graph.edges.some(
          e => (e.source === node.id || e.target === node.id) && e.type === 'workflow'
        )

        switch (filterType) {
          case 'has-deps':
            return hasDeps
          case 'no-deps':
            return !hasDeps
          case 'workflow':
            return hasWorkflow
          default:
            return true
        }
      })
    }

    return nodes
  }, [graph, searchQuery, filterType, getNodeEdges])

  // Filter edges to only include filtered nodes
  const filteredEdges = useMemo(() => {
    if (!graph) return []
    const nodeIds = new Set(filteredNodes.map(n => n.id))
    return graph.edges.filter(e => nodeIds.has(e.source) && nodeIds.has(e.target))
  }, [graph, filteredNodes])

  const filteredGraph = useMemo(() => ({
    nodes: filteredNodes,
    edges: filteredEdges,
  }), [filteredNodes, filteredEdges])

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

      {/* Search and Filters */}
      <div className="flex items-center gap-4">
        <div className="relative flex-1 max-w-md">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
          <input
            type="text"
            placeholder="搜索函数名称或运行时..."
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            className="w-full pl-10 pr-10 py-2 text-sm rounded-lg border border-border bg-background text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-accent/50"
          />
          {searchQuery && (
            <button
              onClick={() => setSearchQuery('')}
              className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
            >
              <X className="w-4 h-4" />
            </button>
          )}
        </div>

        <div className="relative">
          <button
            onClick={() => setShowFilters(!showFilters)}
            className={cn(
              'inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg border transition-colors',
              filterType !== 'all'
                ? 'border-accent bg-accent/10 text-accent'
                : 'border-border text-muted-foreground hover:text-foreground hover:bg-secondary'
            )}
          >
            <Filter className="w-4 h-4" />
            筛选
            {filterType !== 'all' && (
              <span className="px-1.5 py-0.5 text-xs rounded bg-accent text-accent-foreground">
                {filterOptions.find(f => f.value === filterType)?.label}
              </span>
            )}
          </button>

          {showFilters && (
            <div className="absolute right-0 top-full mt-2 w-48 bg-card rounded-lg border border-border shadow-lg z-10">
              <div className="p-2">
                {filterOptions.map((option) => (
                  <button
                    key={option.value}
                    onClick={() => {
                      setFilterType(option.value)
                      setShowFilters(false)
                    }}
                    className={cn(
                      'w-full px-3 py-2 text-sm text-left rounded-md transition-colors',
                      filterType === option.value
                        ? 'bg-accent text-accent-foreground'
                        : 'text-foreground hover:bg-secondary'
                    )}
                  >
                    {option.label}
                  </button>
                ))}
              </div>
            </div>
          )}
        </div>

        {(searchQuery || filterType !== 'all') && (
          <button
            onClick={() => {
              setSearchQuery('')
              setFilterType('all')
            }}
            className="text-sm text-muted-foreground hover:text-foreground"
          >
            清除筛选
          </button>
        )}
      </div>

      {/* Stats */}
      {graph && (
        <div className="grid grid-cols-4 gap-4">
          <div className="bg-card rounded-xl border border-border p-4">
            <div className="flex items-center gap-3">
              <div className="p-2 rounded-lg bg-blue-500/10">
                <Box className="w-5 h-5 text-blue-400" />
              </div>
              <div>
                <p className="text-2xl font-bold text-foreground">
                  {filteredNodes.length}
                  {filteredNodes.length !== graph.nodes.length && (
                    <span className="text-sm font-normal text-muted-foreground ml-1">
                      / {graph.nodes.length}
                    </span>
                  )}
                </p>
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
                <p className="text-2xl font-bold text-foreground">
                  {filteredEdges.length}
                  {filteredEdges.length !== graph.edges.length && (
                    <span className="text-sm font-normal text-muted-foreground ml-1">
                      / {graph.edges.length}
                    </span>
                  )}
                </p>
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
                  {filteredEdges.filter(e => e.type === 'workflow').length}
                </p>
                <p className="text-sm text-muted-foreground">工作流连接</p>
              </div>
            </div>
          </div>
          <div className="bg-card rounded-xl border border-border p-4">
            <div className="flex items-center gap-3">
              <div className="p-2 rounded-lg bg-cyan-500/10">
                <Network className="w-5 h-5 text-cyan-400" />
              </div>
              <div>
                <p className="text-2xl font-bold text-foreground">
                  {Math.max(...filteredEdges.map(e => e.call_count), 0)}
                </p>
                <p className="text-sm text-muted-foreground">最大调用次数</p>
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
          ) : filteredNodes.length === 0 ? (
            <div className="text-center py-16 text-muted-foreground">
              <Search className="w-12 h-12 mx-auto mb-3 text-muted-foreground/30" />
              <p>没有匹配的结果</p>
              <p className="text-sm mt-1">尝试调整搜索条件或筛选器</p>
            </div>
          ) : viewMode === 'graph' ? (
            <ForceDirectedGraph
              graph={filteredGraph}
              selectedNode={selectedNode}
              onSelectNode={setSelectedNode}
            />
          ) : (
            <div className="divide-y divide-border max-h-[600px] overflow-y-auto">
              {filteredNodes.map((node) => {
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

// Force-directed graph layout types
interface NodePosition {
  x: number
  y: number
  vx: number
  vy: number
}

// Force-Directed Graph Component with physics simulation
function ForceDirectedGraph({
  graph,
  selectedNode,
  onSelectNode,
}: {
  graph: { nodes: DependencyNode[]; edges: DependencyEdge[] }
  selectedNode: string | null
  onSelectNode: (id: string | null) => void
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [positions, setPositions] = useState<Record<string, NodePosition>>({})
  const [zoom, setZoom] = useState(1)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const [isDragging, setIsDragging] = useState(false)
  const [dragStart, setDragStart] = useState({ x: 0, y: 0 })
  const [hoveredNode, setHoveredNode] = useState<string | null>(null)

  const width = 800
  const height = 600

  // Calculate max call count for edge thickness scaling
  const maxCallCount = useMemo(() =>
    Math.max(...graph.edges.map(e => e.call_count), 1),
    [graph.edges]
  )

  // Get edge stroke width based on call count
  const getEdgeWidth = useCallback((callCount: number) => {
    const minWidth = 1
    const maxWidth = 6
    return minWidth + (callCount / maxCallCount) * (maxWidth - minWidth)
  }, [maxCallCount])

  // Initialize and run force simulation
  useEffect(() => {
    if (!graph || graph.nodes.length === 0) return

    // Initialize positions
    const centerX = width / 2
    const centerY = height / 2
    const initialPositions: Record<string, NodePosition> = {}

    graph.nodes.forEach((node, i) => {
      const angle = (2 * Math.PI * i) / graph.nodes.length
      const radius = Math.min(150, 30 * graph.nodes.length / Math.PI)
      initialPositions[node.id] = {
        x: centerX + radius * Math.cos(angle) + (Math.random() - 0.5) * 50,
        y: centerY + radius * Math.sin(angle) + (Math.random() - 0.5) * 50,
        vx: 0,
        vy: 0,
      }
    })

    // Run force simulation
    const iterations = 100
    const positions = { ...initialPositions }

    for (let i = 0; i < iterations; i++) {
      const alpha = 1 - i / iterations

      // Repulsion between all nodes
      for (const nodeA of graph.nodes) {
        for (const nodeB of graph.nodes) {
          if (nodeA.id >= nodeB.id) continue

          const posA = positions[nodeA.id]
          const posB = positions[nodeB.id]
          const dx = posB.x - posA.x
          const dy = posB.y - posA.y
          const dist = Math.sqrt(dx * dx + dy * dy) || 1
          const force = (150 * 150) / dist

          const fx = (dx / dist) * force * alpha * 0.1
          const fy = (dy / dist) * force * alpha * 0.1

          posA.vx -= fx
          posA.vy -= fy
          posB.vx += fx
          posB.vy += fy
        }
      }

      // Attraction along edges
      for (const edge of graph.edges) {
        const posA = positions[edge.source]
        const posB = positions[edge.target]
        if (!posA || !posB) continue

        const dx = posB.x - posA.x
        const dy = posB.y - posA.y
        const dist = Math.sqrt(dx * dx + dy * dy) || 1
        const force = (dist - 100) * 0.05 * alpha

        const fx = (dx / dist) * force
        const fy = (dy / dist) * force

        posA.vx += fx
        posA.vy += fy
        posB.vx -= fx
        posB.vy -= fy
      }

      // Center gravity
      for (const node of graph.nodes) {
        const pos = positions[node.id]
        pos.vx += (centerX - pos.x) * 0.01 * alpha
        pos.vy += (centerY - pos.y) * 0.01 * alpha
      }

      // Apply velocities with damping
      for (const node of graph.nodes) {
        const pos = positions[node.id]
        pos.x += pos.vx
        pos.y += pos.vy
        pos.vx *= 0.9
        pos.vy *= 0.9

        // Keep within bounds
        pos.x = Math.max(50, Math.min(width - 50, pos.x))
        pos.y = Math.max(50, Math.min(height - 50, pos.y))
      }
    }

    setPositions(positions)
  }, [graph])

  // Handle mouse events for panning
  const handleMouseDown = (e: React.MouseEvent) => {
    if (e.target === e.currentTarget || (e.target as HTMLElement).tagName === 'svg') {
      setIsDragging(true)
      setDragStart({ x: e.clientX - pan.x, y: e.clientY - pan.y })
    }
  }

  const handleMouseMove = (e: React.MouseEvent) => {
    if (isDragging) {
      setPan({
        x: e.clientX - dragStart.x,
        y: e.clientY - dragStart.y,
      })
    }
  }

  const handleMouseUp = () => {
    setIsDragging(false)
  }

  const handleZoomIn = () => setZoom(z => Math.min(z + 0.2, 3))
  const handleZoomOut = () => setZoom(z => Math.max(z - 0.2, 0.3))
  const handleReset = () => {
    setZoom(1)
    setPan({ x: 0, y: 0 })
  }

  if (Object.keys(positions).length === 0) {
    return (
      <div className="h-[600px] flex items-center justify-center">
        <RefreshCw className="w-6 h-6 text-accent animate-spin" />
      </div>
    )
  }

  return (
    <div
      ref={containerRef}
      className="h-[600px] relative overflow-hidden bg-secondary/10"
      onMouseDown={handleMouseDown}
      onMouseMove={handleMouseMove}
      onMouseUp={handleMouseUp}
      onMouseLeave={handleMouseUp}
    >
      {/* Zoom controls */}
      <div className="absolute top-4 right-4 flex flex-col gap-1 z-10">
        <button
          onClick={handleZoomIn}
          className="p-2 bg-card rounded-lg border border-border hover:bg-secondary transition-colors"
          title="放大"
        >
          <ZoomIn className="w-4 h-4" />
        </button>
        <button
          onClick={handleZoomOut}
          className="p-2 bg-card rounded-lg border border-border hover:bg-secondary transition-colors"
          title="缩小"
        >
          <ZoomOut className="w-4 h-4" />
        </button>
        <button
          onClick={handleReset}
          className="p-2 bg-card rounded-lg border border-border hover:bg-secondary transition-colors"
          title="重置视图"
        >
          <Maximize2 className="w-4 h-4" />
        </button>
      </div>

      <svg
        className="w-full h-full"
        style={{
          cursor: isDragging ? 'grabbing' : 'grab',
        }}
      >
        <g transform={`translate(${pan.x}, ${pan.y}) scale(${zoom})`}>
          {/* Edges with gradient and varying thickness */}
          <defs>
            {graph.edges.map((edge, idx) => (
              <linearGradient
                key={`gradient-${idx}`}
                id={`edge-gradient-${idx}`}
                x1="0%"
                y1="0%"
                x2="100%"
                y2="0%"
              >
                <stop offset="0%" stopColor={dependencyTypeColors[edge.type] || '#8b5cf6'} stopOpacity="0.8" />
                <stop offset="100%" stopColor={dependencyTypeColors[edge.type] || '#8b5cf6'} stopOpacity="0.3" />
              </linearGradient>
            ))}

            {/* Arrow markers for different edge types */}
            {Object.entries(dependencyTypeColors).map(([type, color]) => (
              <marker
                key={type}
                id={`arrowhead-${type}`}
                markerWidth="10"
                markerHeight="7"
                refX="9"
                refY="3.5"
                orient="auto"
              >
                <polygon
                  points="0 0, 10 3.5, 0 7"
                  fill={color}
                />
              </marker>
            ))}
          </defs>

          {graph.edges.map((edge, idx) => {
            const source = positions[edge.source]
            const target = positions[edge.target]
            if (!source || !target) return null

            const isHighlighted = selectedNode === edge.source || selectedNode === edge.target ||
                                  hoveredNode === edge.source || hoveredNode === edge.target
            const strokeWidth = getEdgeWidth(edge.call_count)

            // Calculate arrow offset to not overlap with node circle
            const dx = target.x - source.x
            const dy = target.y - source.y
            const dist = Math.sqrt(dx * dx + dy * dy) || 1
            const offsetX = (dx / dist) * 30
            const offsetY = (dy / dist) * 30

            return (
              <g key={idx}>
                <line
                  x1={source.x}
                  y1={source.y}
                  x2={target.x - offsetX}
                  y2={target.y - offsetY}
                  stroke={isHighlighted
                    ? dependencyTypeColors[edge.type] || '#8b5cf6'
                    : `url(#edge-gradient-${idx})`
                  }
                  strokeWidth={isHighlighted ? strokeWidth + 1 : strokeWidth}
                  strokeOpacity={isHighlighted ? 1 : 0.6}
                  markerEnd={`url(#arrowhead-${edge.type})`}
                  className="transition-all duration-200"
                />
                {/* Call count label on edge */}
                {isHighlighted && edge.call_count > 1 && (
                  <text
                    x={(source.x + target.x) / 2}
                    y={(source.y + target.y) / 2 - 8}
                    textAnchor="middle"
                    fontSize="10"
                    fill={dependencyTypeColors[edge.type] || '#8b5cf6'}
                    fontWeight="600"
                  >
                    {edge.call_count}x
                  </text>
                )}
              </g>
            )
          })}

          {/* Nodes */}
          {graph.nodes.map((node) => {
            const pos = positions[node.id]
            if (!pos) return null

            const isSelected = selectedNode === node.id
            const isHovered = hoveredNode === node.id
            const isHighlighted = isSelected || isHovered

            return (
              <g
                key={node.id}
                onClick={(e) => {
                  e.stopPropagation()
                  onSelectNode(isSelected ? null : node.id)
                }}
                onMouseEnter={() => setHoveredNode(node.id)}
                onMouseLeave={() => setHoveredNode(null)}
                className="cursor-pointer"
                style={{ transition: 'transform 0.2s' }}
              >
                {/* Glow effect for selected/hovered */}
                {isHighlighted && (
                  <circle
                    cx={pos.x}
                    cy={pos.y}
                    r={38}
                    fill="none"
                    stroke={nodeStatusSvgColors[node.status] || '#6b7280'}
                    strokeWidth={2}
                    strokeOpacity={0.3}
                  />
                )}

                {/* Node circle */}
                <circle
                  cx={pos.x}
                  cy={pos.y}
                  r={isHighlighted ? 32 : 28}
                  fill={isSelected ? nodeStatusSvgColors[node.status] || '#6b7280' : 'var(--card)'}
                  stroke={nodeStatusSvgColors[node.status] || '#6b7280'}
                  strokeWidth={isHighlighted ? 3 : 2}
                  className="transition-all duration-200"
                />

                {/* Node name */}
                <text
                  x={pos.x}
                  y={pos.y - 4}
                  textAnchor="middle"
                  dominantBaseline="middle"
                  fill={isSelected ? 'white' : 'var(--foreground)'}
                  fontSize="10"
                  fontWeight={isHighlighted ? 600 : 400}
                >
                  {node.name.length > 10 ? node.name.slice(0, 10) + '...' : node.name}
                </text>

                {/* Runtime label */}
                <text
                  x={pos.x}
                  y={pos.y + 10}
                  textAnchor="middle"
                  dominantBaseline="middle"
                  fill={isSelected ? 'rgba(255,255,255,0.7)' : 'var(--muted-foreground)'}
                  fontSize="8"
                >
                  {node.runtime?.replace('python', 'py').replace('nodejs', 'node') || ''}
                </text>
              </g>
            )
          })}
        </g>
      </svg>

      {/* Legend */}
      <div className="absolute bottom-4 left-4 bg-card/95 backdrop-blur-sm rounded-lg border border-border p-3 text-xs">
        <div className="space-y-2">
          <div className="font-medium text-foreground mb-2">图例</div>
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
          <div className="flex items-center gap-4 pt-2 border-t border-border">
            <div className="flex items-center gap-1.5">
              <div className="w-4 h-0.5 bg-purple-500" />
              <span className="text-muted-foreground">直接调用</span>
            </div>
            <div className="flex items-center gap-1.5">
              <div className="w-4 h-0.5 bg-orange-500" />
              <span className="text-muted-foreground">工作流</span>
            </div>
          </div>
          <div className="flex items-center gap-2 pt-2 border-t border-border text-muted-foreground">
            <span>线条粗细 = 调用次数</span>
          </div>
        </div>
      </div>

      {/* Hovered node info */}
      {hoveredNode && positions[hoveredNode] && (
        <div
          className="absolute bg-card/95 backdrop-blur-sm rounded-lg border border-border p-2 text-xs pointer-events-none z-20"
          style={{
            left: positions[hoveredNode].x * zoom + pan.x + 40,
            top: positions[hoveredNode].y * zoom + pan.y - 20,
          }}
        >
          {(() => {
            const node = graph.nodes.find(n => n.id === hoveredNode)
            if (!node) return null
            const incoming = graph.edges.filter(e => e.target === hoveredNode)
            const outgoing = graph.edges.filter(e => e.source === hoveredNode)
            return (
              <div className="space-y-1">
                <div className="font-medium text-foreground">{node.name}</div>
                <div className="text-muted-foreground">{node.runtime}</div>
                <div className="text-muted-foreground">
                  {outgoing.length} 调用 / {incoming.length} 被调用
                </div>
              </div>
            )
          })()}
        </div>
      )}
    </div>
  )
}
