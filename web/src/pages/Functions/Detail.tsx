import { useState, useEffect } from 'react'
import { useParams, useNavigate, useSearchParams, Link } from 'react-router-dom'
import {
  ArrowLeft,
  Save,
  Play,
  Clock,
  Cpu,
  CheckCircle2,
  XCircle,
  RefreshCw,
  Zap,
  ExternalLink,
  AlertCircle,
  Terminal,
  Download,
  Copy,
  X,
  Activity,
  PauseCircle,
  PlayCircle,
  Loader2,
  GitCompare,
  Trash2,
  Edit3,
  Layers,
  Tag,
  RotateCcw,
  Link2,
  Key,
  Eye,
  EyeOff,
  Power,
  PowerOff,
} from 'lucide-react'
import Editor, { DiffEditor } from '@monaco-editor/react'
import ReactECharts from 'echarts-for-react'
import LogStreamViewer from '../../components/LogViewer/LogStreamViewer'
import { functionService, invocationService, metricsService, layerService } from '../../services'
import type { Function, Runtime, InvokeResponse, FunctionVersion, FunctionAlias, FunctionLayer, FunctionEnvConfig, FunctionStatus, Layer, UpdateFunctionEnvConfigRequest } from '../../types'
import type { Invocation } from '../../types/invocation'
import type { FunctionStats, TrendDataPoint, LatencyDistribution } from '../../types/metrics'
import { RUNTIME_LABELS, STATUS_LABELS } from '../../types'
import { formatDate, formatDuration, formatJson, cn, formatNumber, formatPercent } from '../../utils'
import { validateCron, describeCron } from '../../utils/cron'
import { useToast } from '../../components/Toast'

const languageMap: Record<string, string> = {
  'python3.11': 'python',
  'nodejs20': 'javascript',
  'go1.24': 'go',
  'wasm': 'rust',
  'rust1.75': 'rust',
}

// 运行时徽章颜色
const RUNTIME_BADGE_COLORS: Record<Runtime, string> = {
  'python3.11': 'text-blue-400 bg-blue-400/10',
  'nodejs20': 'text-green-400 bg-green-400/10',
  'go1.24': 'text-cyan-400 bg-cyan-400/10',
  'wasm': 'text-purple-400 bg-purple-400/10',
  'rust1.75': 'text-orange-400 bg-orange-400/10',
}

const getDebugConfig = (fn: Function) => {
  const configs: Record<string, any> = {
    'python3.11': {
      port: 5678,
      fileName: 'handler.py',
      cmd: `nimbus debug ${fn.name} --runtime python3.11 --file handler.py --port 5678`,
      vscode: {
        name: "Nimbus: Python Attach",
        type: "python",
        request: "attach",
        connect: { host: "localhost", port: 5678 },
        pathMappings: [{ localRoot: "${workspaceFolder}", remoteRoot: "/var/task" }]
      }
    },
    'nodejs20': {
      port: 9229,
      fileName: 'index.js',
      cmd: `nimbus debug ${fn.name} --runtime nodejs20 --file index.js --port 9229`,
      vscode: {
        name: "Nimbus: Node Attach",
        type: "node",
        request: "attach",
        port: 9229,
        address: "localhost",
        localRoot: "${workspaceFolder}",
        remoteRoot: "/var/task"
      }
    }
  }
  return configs[fn.runtime] || configs['python3.11']
}

type Tab = 'code' | 'config' | 'test' | 'logs' | 'invocations' | 'analytics' | 'versions' | 'aliases' | 'layers' | 'environments'

// 格式化延迟显示
function formatLatency(ms: number): string {
  if (ms < 1) return '<1ms'
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(2)}s`
}

// 状态徽章组件
function StatusBadge({ status }: { status: string }) {
  if (status === 'success' || status === 'completed') {
    return (
      <div className="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium text-green-400 bg-green-400/10">
        <CheckCircle2 className="h-3 w-3" />
        <span>成功</span>
      </div>
    )
  }
  if (status === 'failed') {
    return (
      <div className="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium text-red-400 bg-red-400/10">
        <XCircle className="h-3 w-3" />
        <span>失败</span>
      </div>
    )
  }
  return (
    <div className="inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium text-muted-foreground bg-secondary">
      <span>{status}</span>
    </div>
  )
}

// 别名模态框组件
interface AliasModalProps {
  functionId: string
  alias: FunctionAlias | null
  versions: FunctionVersion[]
  onClose: () => void
  onSave: () => void
}

function AliasModal({ functionId, alias, versions, onClose, onSave }: AliasModalProps) {
  const [name, setName] = useState(alias?.name || '')
  const [description, setDescription] = useState(alias?.description || '')
  const [weights, setWeights] = useState<Array<{ version: number; weight: number }>>(
    alias?.routing_config.weights || [{ version: versions[0]?.version || 1, weight: 100 }]
  )
  const [saving, setSaving] = useState(false)

  const addWeight = () => {
    const usedVersions = weights.map(w => w.version)
    const availableVersion = versions.find(v => !usedVersions.includes(v.version))
    if (availableVersion) {
      setWeights([...weights, { version: availableVersion.version, weight: 0 }])
    }
  }

  const removeWeight = (index: number) => {
    if (weights.length > 1) {
      setWeights(weights.filter((_, i) => i !== index))
    }
  }

  const updateWeight = (index: number, field: 'version' | 'weight', value: number) => {
    setWeights(weights.map((w, i) => i === index ? { ...w, [field]: value } : w))
  }

  const totalWeight = weights.reduce((sum, w) => sum + w.weight, 0)

  const handleSave = async () => {
    if (!name) {
      alert('请输入别名名称')
      return
    }
    if (totalWeight !== 100) {
      alert('权重总和必须等于 100%')
      return
    }
    try {
      setSaving(true)
      if (alias) {
        await functionService.updateAlias(functionId, alias.name, {
          description: description || undefined,
          routing_config: { weights }
        })
      } else {
        await functionService.createAlias(functionId, {
          name,
          description: description || undefined,
          routing_config: { weights }
        })
      }
      onSave()
    } catch (error) {
      console.error('Failed to save alias:', error)
      alert('保存失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-background/80 backdrop-blur-sm">
      <div className="bg-card border border-border rounded-2xl shadow-xl w-full max-w-lg overflow-hidden">
        <div className="px-6 py-4 border-b border-border flex items-center justify-between">
          <h3 className="text-lg font-semibold text-foreground">
            {alias ? '编辑别名' : '创建别名'}
          </h3>
          <button onClick={onClose} className="p-1 hover:bg-secondary rounded-lg transition-colors">
            <X className="w-5 h-5 text-muted-foreground" />
          </button>
        </div>
        <div className="p-6 space-y-4">
          <div>
            <label className="block text-sm font-medium text-muted-foreground mb-2">别名名称 *</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={!!alias}
              placeholder="例如: prod, canary, latest"
              className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring disabled:opacity-50"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-muted-foreground mb-2">描述</label>
            <input
              type="text"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="可选描述"
              className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
            />
          </div>
          <div>
            <div className="flex items-center justify-between mb-2">
              <label className="text-sm font-medium text-muted-foreground">流量分配</label>
              <span className={cn(
                'text-xs',
                totalWeight === 100 ? 'text-green-400' : 'text-red-400'
              )}>
                总计: {totalWeight}%
              </span>
            </div>
            <div className="space-y-2">
              {weights.map((w, idx) => (
                <div key={idx} className="flex items-center gap-3">
                  <select
                    value={w.version}
                    onChange={(e) => updateWeight(idx, 'version', Number(e.target.value))}
                    className="flex-1 px-3 py-2 bg-input border border-border rounded-lg text-foreground text-sm focus:outline-none focus:ring-2 focus:ring-ring"
                  >
                    {versions.map(v => (
                      <option key={v.version} value={v.version}>v{v.version}</option>
                    ))}
                  </select>
                  <input
                    type="number"
                    value={w.weight}
                    onChange={(e) => updateWeight(idx, 'weight', Number(e.target.value))}
                    min={0}
                    max={100}
                    className="w-20 px-3 py-2 bg-input border border-border rounded-lg text-foreground text-sm focus:outline-none focus:ring-2 focus:ring-ring"
                  />
                  <span className="text-sm text-muted-foreground">%</span>
                  {weights.length > 1 && (
                    <button
                      onClick={() => removeWeight(idx)}
                      className="p-1 text-red-400 hover:bg-red-400/10 rounded"
                    >
                      <X className="w-4 h-4" />
                    </button>
                  )}
                </div>
              ))}
            </div>
            {weights.length < versions.length && (
              <button
                onClick={addWeight}
                className="mt-2 text-sm text-accent hover:underline"
              >
                + 添加版本
              </button>
            )}
          </div>
        </div>
        <div className="px-6 py-4 bg-secondary/30 flex justify-end gap-3">
          <button
            onClick={onClose}
            className="px-4 py-2 text-sm text-muted-foreground hover:text-foreground rounded-lg hover:bg-secondary transition-colors"
          >
            取消
          </button>
          <button
            onClick={handleSave}
            disabled={saving || totalWeight !== 100}
            className="px-4 py-2 text-sm bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors disabled:opacity-50"
          >
            {saving ? '保存中...' : '保存'}
          </button>
        </div>
      </div>
    </div>
  )
}

// 版本对比模态框组件
interface VersionDiffModalProps {
  functionId: string
  versions: FunctionVersion[]
  runtime: string
  onClose: () => void
}

function VersionDiffModal({ functionId, versions, runtime, onClose }: VersionDiffModalProps) {
  const [leftVersion, setLeftVersion] = useState(versions.length > 1 ? versions[1].version : versions[0]?.version || 1)
  const [rightVersion, setRightVersion] = useState(versions[0]?.version || 1)
  const [leftCode, setLeftCode] = useState('')
  const [rightCode, setRightCode] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    loadVersionCodes()
  }, [leftVersion, rightVersion])

  const loadVersionCodes = async () => {
    try {
      setLoading(true)
      const [leftData, rightData] = await Promise.all([
        functionService.getVersion(functionId, leftVersion),
        functionService.getVersion(functionId, rightVersion),
      ])
      setLeftCode(leftData.code || '')
      setRightCode(rightData.code || '')
    } catch (error) {
      console.error('Failed to load version codes:', error)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-background/80 backdrop-blur-sm">
      <div className="bg-card border border-border rounded-2xl shadow-xl w-full max-w-6xl h-[80vh] flex flex-col overflow-hidden">
        <div className="px-6 py-4 border-b border-border flex items-center justify-between">
          <h3 className="text-lg font-semibold text-foreground flex items-center">
            <GitCompare className="w-5 h-5 mr-2 text-accent" />
            版本对比
          </h3>
          <button onClick={onClose} className="p-1 hover:bg-secondary rounded-lg transition-colors">
            <X className="w-5 h-5 text-muted-foreground" />
          </button>
        </div>
        <div className="px-6 py-3 border-b border-border flex items-center gap-4">
          <div className="flex items-center gap-2">
            <label className="text-sm text-muted-foreground">旧版本:</label>
            <select
              value={leftVersion}
              onChange={(e) => setLeftVersion(Number(e.target.value))}
              className="px-3 py-1.5 bg-input border border-border rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-ring"
            >
              {versions.map(v => (
                <option key={v.version} value={v.version}>v{v.version}</option>
              ))}
            </select>
          </div>
          <span className="text-muted-foreground">→</span>
          <div className="flex items-center gap-2">
            <label className="text-sm text-muted-foreground">新版本:</label>
            <select
              value={rightVersion}
              onChange={(e) => setRightVersion(Number(e.target.value))}
              className="px-3 py-1.5 bg-input border border-border rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-ring"
            >
              {versions.map(v => (
                <option key={v.version} value={v.version}>v{v.version}</option>
              ))}
            </select>
          </div>
        </div>
        <div className="flex-1 overflow-hidden">
          {loading ? (
            <div className="flex items-center justify-center h-full">
              <RefreshCw className="w-8 h-8 text-accent animate-spin" />
            </div>
          ) : (
            <DiffEditor
              height="100%"
              language={languageMap[runtime] || 'plaintext'}
              original={leftCode}
              modified={rightCode}
              theme="vs-dark"
              options={{
                readOnly: true,
                minimap: { enabled: false },
                fontSize: 14,
                scrollBeyondLastLine: false,
                renderSideBySide: true,
              }}
            />
          )}
        </div>
      </div>
    </div>
  )
}

// 层绑定模态框组件
interface LayerBindingModalProps {
  functionId: string
  functionRuntime: string
  currentLayers: FunctionLayer[]
  onClose: () => void
  onSave: () => void
}

function LayerBindingModal({ functionId, functionRuntime, currentLayers, onClose, onSave }: LayerBindingModalProps) {
  const [availableLayers, setAvailableLayers] = useState<Layer[]>([])
  const [selectedLayers, setSelectedLayers] = useState<Array<{ layer_id: string; layer_version: number; order: number }>>(
    currentLayers.map(l => ({ layer_id: l.layer_id, layer_version: l.layer_version, order: l.order }))
  )
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    loadAvailableLayers()
  }, [])

  const loadAvailableLayers = async () => {
    try {
      setLoading(true)
      const result = await layerService.list()
      // Filter layers compatible with function runtime
      const compatible = (result.layers || []).filter(l =>
        l.compatible_runtimes.includes(functionRuntime)
      )
      setAvailableLayers(compatible)
    } catch (error) {
      console.error('Failed to load layers:', error)
    } finally {
      setLoading(false)
    }
  }

  const addLayer = (layerId: string) => {
    const layer = availableLayers.find(l => l.id === layerId)
    if (!layer || selectedLayers.some(l => l.layer_id === layerId)) return
    setSelectedLayers([
      ...selectedLayers,
      { layer_id: layerId, layer_version: layer.latest_version, order: selectedLayers.length }
    ])
  }

  const removeLayer = (layerId: string) => {
    setSelectedLayers(selectedLayers.filter(l => l.layer_id !== layerId).map((l, idx) => ({ ...l, order: idx })))
  }

  const updateLayerVersion = (layerId: string, version: number) => {
    setSelectedLayers(selectedLayers.map(l =>
      l.layer_id === layerId ? { ...l, layer_version: version } : l
    ))
  }

  const moveLayer = (index: number, direction: 'up' | 'down') => {
    const newIndex = direction === 'up' ? index - 1 : index + 1
    if (newIndex < 0 || newIndex >= selectedLayers.length) return
    const newLayers = [...selectedLayers]
    ;[newLayers[index], newLayers[newIndex]] = [newLayers[newIndex], newLayers[index]]
    setSelectedLayers(newLayers.map((l, idx) => ({ ...l, order: idx })))
  }

  const handleSave = async () => {
    try {
      setSaving(true)
      await functionService.setFunctionLayers(functionId, selectedLayers)
      onSave()
    } catch (error) {
      console.error('Failed to save layers:', error)
      alert('保存失败')
    } finally {
      setSaving(false)
    }
  }

  const getLayerName = (layerId: string) => {
    return availableLayers.find(l => l.id === layerId)?.name || layerId
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-background/80 backdrop-blur-sm">
      <div className="bg-card border border-border rounded-2xl shadow-xl w-full max-w-lg overflow-hidden">
        <div className="px-6 py-4 border-b border-border flex items-center justify-between">
          <h3 className="text-lg font-semibold text-foreground flex items-center">
            <Layers className="w-5 h-5 mr-2 text-accent" />
            配置依赖层
          </h3>
          <button onClick={onClose} className="p-1 hover:bg-secondary rounded-lg transition-colors">
            <X className="w-5 h-5 text-muted-foreground" />
          </button>
        </div>
        <div className="p-6 space-y-4">
          {loading ? (
            <div className="flex items-center justify-center py-8">
              <RefreshCw className="w-6 h-6 text-accent animate-spin" />
            </div>
          ) : (
            <>
              {/* Add layer */}
              <div>
                <label className="block text-sm font-medium text-muted-foreground mb-2">添加层</label>
                <select
                  onChange={(e) => {
                    if (e.target.value) addLayer(e.target.value)
                    e.target.value = ''
                  }}
                  className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                >
                  <option value="">选择要添加的层...</option>
                  {availableLayers
                    .filter(l => !selectedLayers.some(sl => sl.layer_id === l.id))
                    .map(layer => (
                      <option key={layer.id} value={layer.id}>
                        {layer.name} (v{layer.latest_version})
                      </option>
                    ))
                  }
                </select>
                {availableLayers.length === 0 && (
                  <p className="mt-2 text-xs text-muted-foreground">
                    没有与 {functionRuntime} 兼容的层
                  </p>
                )}
              </div>

              {/* Selected layers */}
              <div>
                <label className="block text-sm font-medium text-muted-foreground mb-2">
                  已配置的层 ({selectedLayers.length})
                </label>
                {selectedLayers.length === 0 ? (
                  <p className="text-sm text-muted-foreground py-4 text-center">暂未配置任何层</p>
                ) : (
                  <div className="space-y-2">
                    {selectedLayers.map((layer, idx) => (
                      <div key={layer.layer_id} className="flex items-center gap-2 p-3 bg-secondary/50 rounded-lg">
                        <span className="text-xs text-muted-foreground w-6">{idx + 1}.</span>
                        <span className="flex-1 text-sm font-medium text-foreground">
                          {getLayerName(layer.layer_id)}
                        </span>
                        <select
                          value={layer.layer_version}
                          onChange={(e) => updateLayerVersion(layer.layer_id, Number(e.target.value))}
                          className="px-2 py-1 text-xs bg-input border border-border rounded focus:outline-none"
                        >
                          {Array.from({ length: availableLayers.find(l => l.id === layer.layer_id)?.latest_version || 1 }, (_, i) => i + 1).map(v => (
                            <option key={v} value={v}>v{v}</option>
                          ))}
                        </select>
                        <div className="flex items-center gap-1">
                          <button
                            onClick={() => moveLayer(idx, 'up')}
                            disabled={idx === 0}
                            className="p-1 text-muted-foreground hover:text-foreground disabled:opacity-30"
                          >
                            ↑
                          </button>
                          <button
                            onClick={() => moveLayer(idx, 'down')}
                            disabled={idx === selectedLayers.length - 1}
                            className="p-1 text-muted-foreground hover:text-foreground disabled:opacity-30"
                          >
                            ↓
                          </button>
                        </div>
                        <button
                          onClick={() => removeLayer(layer.layer_id)}
                          className="p-1 text-red-400 hover:text-red-300"
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </>
          )}
        </div>
        <div className="px-6 py-4 bg-secondary/30 flex justify-end gap-3">
          <button
            onClick={onClose}
            className="px-4 py-2 text-sm text-muted-foreground hover:text-foreground rounded-lg hover:bg-secondary transition-colors"
          >
            取消
          </button>
          <button
            onClick={handleSave}
            disabled={saving}
            className="px-4 py-2 text-sm bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors disabled:opacity-50"
          >
            {saving ? '保存中...' : '保存'}
          </button>
        </div>
      </div>
    </div>
  )
}

// 环境配置编辑模态框组件
interface EnvConfigModalProps {
  functionId: string
  config: FunctionEnvConfig
  aliases: FunctionAlias[]
  fn: Function
  onClose: () => void
  onSave: () => void
}

function EnvConfigModal({ functionId, config, aliases, fn, onClose, onSave }: EnvConfigModalProps) {
  const [form, setForm] = useState<UpdateFunctionEnvConfigRequest>({
    memory_mb: config.memory_mb || fn.memory_mb,
    timeout_sec: config.timeout_sec || fn.timeout_sec,
    active_alias: config.active_alias || '',
    env_vars: config.env_vars || {},
  })
  const [envVarsText, setEnvVarsText] = useState(
    Object.entries(config.env_vars || {}).map(([k, v]) => `${k}=${v}`).join('\n')
  )
  const [saving, setSaving] = useState(false)

  const parseEnvVars = (text: string): Record<string, string> => {
    const vars: Record<string, string> = {}
    text.split('\n').forEach(line => {
      const trimmed = line.trim()
      if (!trimmed || trimmed.startsWith('#')) return
      const idx = trimmed.indexOf('=')
      if (idx > 0) {
        vars[trimmed.slice(0, idx)] = trimmed.slice(idx + 1)
      }
    })
    return vars
  }

  const handleSave = async () => {
    try {
      setSaving(true)
      const envVars = parseEnvVars(envVarsText)
      await functionService.updateFunctionEnvConfig(functionId, config.environment_name || config.environment_id, {
        memory_mb: form.memory_mb,
        timeout_sec: form.timeout_sec,
        active_alias: form.active_alias || undefined,
        env_vars: envVars,
      })
      onSave()
    } catch (error) {
      console.error('Failed to save env config:', error)
      alert('保存失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-background/80 backdrop-blur-sm">
      <div className="bg-card border border-border rounded-2xl shadow-xl w-full max-w-lg overflow-hidden">
        <div className="px-6 py-4 border-b border-border flex items-center justify-between">
          <h3 className="text-lg font-semibold text-foreground">
            编辑环境配置 - {config.environment_name || config.environment_id}
          </h3>
          <button onClick={onClose} className="p-1 hover:bg-secondary rounded-lg transition-colors">
            <X className="w-5 h-5 text-muted-foreground" />
          </button>
        </div>
        <div className="p-6 space-y-4 max-h-[60vh] overflow-y-auto">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-muted-foreground mb-2">内存 (MB)</label>
              <select
                value={form.memory_mb}
                onChange={(e) => setForm({ ...form, memory_mb: Number(e.target.value) })}
                className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              >
                <option value={128}>128 MB</option>
                <option value={256}>256 MB</option>
                <option value={512}>512 MB</option>
                <option value={1024}>1024 MB</option>
                <option value={2048}>2048 MB</option>
              </select>
            </div>
            <div>
              <label className="block text-sm font-medium text-muted-foreground mb-2">超时 (秒)</label>
              <input
                type="number"
                value={form.timeout_sec}
                onChange={(e) => setForm({ ...form, timeout_sec: Number(e.target.value) })}
                min={1}
                max={900}
                className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              />
            </div>
          </div>

          <div>
            <label className="block text-sm font-medium text-muted-foreground mb-2">活跃别名</label>
            <select
              value={form.active_alias || ''}
              onChange={(e) => setForm({ ...form, active_alias: e.target.value })}
              className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
            >
              <option value="">无 (使用最新版本)</option>
              {aliases.map(alias => (
                <option key={alias.id} value={alias.name}>{alias.name}</option>
              ))}
            </select>
            <p className="mt-1 text-xs text-muted-foreground">
              选择该环境使用的别名进行流量分配
            </p>
          </div>

          <div>
            <label className="block text-sm font-medium text-muted-foreground mb-2">环境变量</label>
            <textarea
              value={envVarsText}
              onChange={(e) => setEnvVarsText(e.target.value)}
              placeholder="KEY=value&#10;ANOTHER_KEY=another_value"
              rows={6}
              className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground font-mono text-sm focus:outline-none focus:ring-2 focus:ring-ring"
            />
            <p className="mt-1 text-xs text-muted-foreground">
              每行一个环境变量，格式: KEY=value
            </p>
          </div>
        </div>
        <div className="px-6 py-4 bg-secondary/30 flex justify-end gap-3">
          <button
            onClick={onClose}
            className="px-4 py-2 text-sm text-muted-foreground hover:text-foreground rounded-lg hover:bg-secondary transition-colors"
          >
            取消
          </button>
          <button
            onClick={handleSave}
            disabled={saving}
            className="px-4 py-2 text-sm bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors disabled:opacity-50"
          >
            {saving ? '保存中...' : '保存'}
          </button>
        </div>
      </div>
    </div>
  )
}

export default function FunctionDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const [fn, setFn] = useState<Function | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [activeTab, setActiveTab] = useState<Tab>((searchParams.get('tab') as Tab) || 'code')

  // 代码编辑状态
  const [code, setCode] = useState('')
  const [originalCode, setOriginalCode] = useState('')
  const [configForm, setConfigForm] = useState<{
    memory_mb: number
    timeout_sec: number
    max_concurrency: number
    cron_expression: string
    http_path: string
    http_methods: string[]
  }>({
    memory_mb: 128,
    timeout_sec: 30,
    max_concurrency: 0,
    cron_expression: '',
    http_path: '',
    http_methods: [],
  })
  const hasChanges = code !== originalCode

  // 测试状态
  const [testPayload, setTestPayload] = useState('{}')
  const [testResult, setTestResult] = useState<InvokeResponse | null>(null)
  const [testing, setTesting] = useState(false)

  // 调用记录状态
  const [invocations, setInvocations] = useState<Invocation[]>([])
  const [invocationsLoading, setInvocationsLoading] = useState(false)
  const [invocationsTotal, setInvocationsTotal] = useState(0)
  const [replayingId, setReplayingId] = useState<string | null>(null)
  const toast = useToast()

  // 调试模态框状态
  const [showDebugModal, setShowDebugModal] = useState(false)

  // 分析数据状态
  const [analyticsPeriod, setAnalyticsPeriod] = useState('24h')
  const [analyticsStats, setAnalyticsStats] = useState<FunctionStats | null>(null)
  const [analyticsTrends, setAnalyticsTrends] = useState<TrendDataPoint[]>([])
  const [latencyDistribution, setLatencyDistribution] = useState<LatencyDistribution[]>([])
  const [analyticsLoading, setAnalyticsLoading] = useState(false)

  // 版本管理状态
  const [versions, setVersions] = useState<FunctionVersion[]>([])
  const [versionsLoading, setVersionsLoading] = useState(false)

  // 别名管理状态
  const [aliases, setAliases] = useState<FunctionAlias[]>([])
  const [aliasesLoading, setAliasesLoading] = useState(false)
  const [showAliasModal, setShowAliasModal] = useState(false)
  const [editingAlias, setEditingAlias] = useState<FunctionAlias | null>(null)

  // 层管理状态
  const [functionLayers, setFunctionLayers] = useState<FunctionLayer[]>([])
  const [layersLoading, setLayersLoading] = useState(false)

  // 环境配置状态
  const [envConfigs, setEnvConfigs] = useState<FunctionEnvConfig[]>([])
  const [envConfigsLoading, setEnvConfigsLoading] = useState(false)

  // 版本对比状态
  const [showVersionDiff, setShowVersionDiff] = useState(false)

  // Webhook 管理状态
  const [webhookUrl, setWebhookUrl] = useState<string>('')
  const [webhookKey, setWebhookKey] = useState<string>('')
  const [webhookOperating, setWebhookOperating] = useState(false)
  const [showWebhookKey, setShowWebhookKey] = useState(false)

  // 层绑定模态框状态
  const [showLayerBindingModal, setShowLayerBindingModal] = useState(false)

  // 环境配置编辑状态
  const [showEnvConfigModal, setShowEnvConfigModal] = useState(false)
  const [editingEnvConfig, setEditingEnvConfig] = useState<FunctionEnvConfig | null>(null)

  // 标签编辑状态
  const [editingTags, setEditingTags] = useState(false)
  const [tagsInput, setTagsInput] = useState('')
  const [savingTags, setSavingTags] = useState(false)

  const loadFunction = async () => {
    if (!id) return
    try {
      setLoading(true)
      const data = await functionService.get(id)
      setFn(data)
      setCode(data.code)
      setOriginalCode(data.code)
      setConfigForm({
        memory_mb: data.memory_mb,
        timeout_sec: data.timeout_sec,
        max_concurrency: data.max_concurrency || 0,
        cron_expression: data.cron_expression || '',
        http_path: data.http_path || '',
        http_methods: data.http_methods || [],
      })
    } catch (error) {
      console.error('Failed to load function:', error)
    } finally {
      setLoading(false)
    }
  }

  const handleSave = async () => {
    if (!id || !fn) return
    try {
      setSaving(true)
      await functionService.update(id, {
        code,
        memory_mb: configForm.memory_mb,
        timeout_sec: configForm.timeout_sec,
        max_concurrency: configForm.max_concurrency,
        cron_expression: configForm.cron_expression || undefined,
        http_path: configForm.http_path || undefined,
        http_methods: configForm.http_methods.length > 0 ? configForm.http_methods : undefined,
      })
      setOriginalCode(code)
      // Reload function to get updated version
      await loadFunction()
    } catch (error) {
      console.error('Failed to save function:', error)
      alert('保存失败')
    } finally {
      setSaving(false)
    }
  }

  const handleSaveTags = async () => {
    if (!id || !fn) return
    try {
      setSavingTags(true)
      const tags = tagsInput
        .split(',')
        .map(t => t.trim())
        .filter(t => t.length > 0)
      await functionService.update(id, { tags })
      await loadFunction()
      setEditingTags(false)
    } catch (error) {
      console.error('Failed to save tags:', error)
      alert('保存标签失败')
    } finally {
      setSavingTags(false)
    }
  }

  const startEditingTags = () => {
    setTagsInput(fn?.tags?.join(', ') || '')
    setEditingTags(true)
  }

  const handleTabChange = (tab: Tab) => {
    setActiveTab(tab)
    setSearchParams({ tab })
  }

  const loadInvocations = async () => {
    if (!id) return
    try {
      setInvocationsLoading(true)
      const result = await invocationService.list({ function_id: id, limit: 50 })
      setInvocations(result.invocations)
      setInvocationsTotal(result.total)
    } catch (error) {
      console.error('Failed to load invocations:', error)
    } finally {
      setInvocationsLoading(false)
    }
  }

  const handleReplayInvocation = async (invocationId: string) => {
    try {
      setReplayingId(invocationId)
      const result = await invocationService.replay(invocationId)
      toast.success('重放成功', `执行耗时 ${result.duration_ms}ms`)
      // 刷新调用记录列表
      loadInvocations()
    } catch (error) {
      console.error('Failed to replay invocation:', error)
      toast.error('重放失败', '请稍后重试')
    } finally {
      setReplayingId(null)
    }
  }

  const loadAnalytics = async () => {
    if (!id) return
    try {
      setAnalyticsLoading(true)
      const [statsData, trendsData, distData] = await Promise.all([
        metricsService.getFunctionStats(id, analyticsPeriod),
        metricsService.getFunctionTrends(id, analyticsPeriod),
        metricsService.getFunctionLatencyDistribution(id, analyticsPeriod),
      ])
      setAnalyticsStats(statsData)
      setAnalyticsTrends(trendsData)
      setLatencyDistribution(distData)
    } catch (error) {
      console.error('Failed to load analytics:', error)
    } finally {
      setAnalyticsLoading(false)
    }
  }

  const loadVersions = async () => {
    if (!id) return
    try {
      setVersionsLoading(true)
      const data = await functionService.listVersions(id)
      setVersions(data)
    } catch (error) {
      console.error('Failed to load versions:', error)
    } finally {
      setVersionsLoading(false)
    }
  }

  const loadAliases = async () => {
    if (!id) return
    try {
      setAliasesLoading(true)
      const data = await functionService.listAliases(id)
      setAliases(data)
    } catch (error) {
      console.error('Failed to load aliases:', error)
    } finally {
      setAliasesLoading(false)
    }
  }

  const loadFunctionLayers = async () => {
    if (!id) return
    try {
      setLayersLoading(true)
      const data = await functionService.getFunctionLayers(id)
      setFunctionLayers(data)
    } catch (error) {
      console.error('Failed to load function layers:', error)
    } finally {
      setLayersLoading(false)
    }
  }

  const loadEnvConfigs = async () => {
    if (!id) return
    try {
      setEnvConfigsLoading(true)
      const data = await functionService.getFunctionEnvConfigs(id)
      setEnvConfigs(data)
    } catch (error) {
      console.error('Failed to load environment configs:', error)
    } finally {
      setEnvConfigsLoading(false)
    }
  }

  const handleRollback = async (version: number) => {
    if (!id) return
    if (!confirm(`确定要回滚到版本 ${version} 吗？此操作将创建一个新版本。`)) return
    try {
      await functionService.rollback(id, version)
      await loadFunction()
      await loadVersions()
    } catch (error) {
      console.error('Failed to rollback:', error)
      alert('回滚失败')
    }
  }

  const handleDeleteAlias = async (name: string) => {
    if (!id) return
    if (!confirm(`确定要删除别名 "${name}" 吗？`)) return
    try {
      await functionService.deleteAlias(id, name)
      await loadAliases()
    } catch (error) {
      console.error('Failed to delete alias:', error)
      alert('删除失败')
    }
  }

  // Webhook 操作
  const handleEnableWebhook = async () => {
    if (!id) return
    try {
      setWebhookOperating(true)
      const result = await functionService.enableWebhook(id)
      setWebhookUrl(result.webhook_url)
      setWebhookKey(result.webhook_key)
      await loadFunction()
      toast.success('Webhook 已启用')
    } catch (error) {
      console.error('Failed to enable webhook:', error)
      toast.error('启用 Webhook 失败')
    } finally {
      setWebhookOperating(false)
    }
  }

  const handleDisableWebhook = async () => {
    if (!id) return
    if (!confirm('确定要禁用 Webhook 吗？禁用后外部将无法通过 Webhook URL 调用此函数。')) return
    try {
      setWebhookOperating(true)
      await functionService.disableWebhook(id)
      setWebhookUrl('')
      setWebhookKey('')
      setShowWebhookKey(false)
      await loadFunction()
      toast.success('Webhook 已禁用')
    } catch (error) {
      console.error('Failed to disable webhook:', error)
      toast.error('禁用 Webhook 失败')
    } finally {
      setWebhookOperating(false)
    }
  }

  const handleRegenerateWebhookKey = async () => {
    if (!id) return
    if (!confirm('确定要重新生成 Webhook 密钥吗？旧密钥将立即失效。')) return
    try {
      setWebhookOperating(true)
      const result = await functionService.regenerateWebhookKey(id)
      setWebhookKey(result.webhook_key)
      setShowWebhookKey(true)
      toast.success('Webhook 密钥已重新生成')
    } catch (error) {
      console.error('Failed to regenerate webhook key:', error)
      toast.error('重新生成密钥失败')
    } finally {
      setWebhookOperating(false)
    }
  }

  // 加载函数基础数据
  useEffect(() => {
    loadFunction()
  }, [id])

  // 根据 activeTab 加载对应数据
  useEffect(() => {
    if (!id || loading) return
    switch (activeTab) {
      case 'invocations':
        loadInvocations()
        break
      case 'analytics':
        loadAnalytics()
        break
      case 'versions':
        loadVersions()
        break
      case 'aliases':
        loadAliases()
        break
      case 'layers':
        loadFunctionLayers()
        break
      case 'environments':
        loadEnvConfigs()
        break
    }
  }, [id, loading, activeTab])

  // 分析周期变化时重新加载
  useEffect(() => {
    if (activeTab === 'analytics' && id && !loading) {
      loadAnalytics()
    }
  }, [analyticsPeriod])

  const handleTest = async () => {
    if (!id) return
    try {
      setTesting(true)
      setTestResult(null)
      let payload: unknown
      try {
        payload = JSON.parse(testPayload)
      } catch {
        payload = testPayload
      }
      const result = await functionService.invoke(id, payload)
      setTestResult(result)
    } catch (error) {
      console.error('Failed to invoke function:', error)
      setTestResult({
        request_id: '',
        status_code: 500,
        error: String(error),
        duration_ms: 0,
        cold_start: false,
        billed_time_ms: 0,
      })
    } finally {
      setTesting(false)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <RefreshCw className="w-8 h-8 text-accent animate-spin" />
      </div>
    )
  }

  if (!fn) {
    return (
      <div className="text-center py-12">
        <AlertCircle className="w-12 h-12 text-muted-foreground mx-auto mb-4" />
        <p className="text-muted-foreground">函数不存在</p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* 页头 */}
      <div className="flex items-center justify-between">
        <div className="flex items-center">
          <button
            onClick={() => navigate('/functions')}
            className="p-2 text-muted-foreground hover:text-foreground hover:bg-secondary rounded-lg mr-4 transition-colors"
          >
            <ArrowLeft className="w-5 h-5" />
          </button>
          <div>
            <div className="flex items-center">
              <h1 className="text-2xl font-bold text-foreground">{fn.name}</h1>
              <span className={cn(
                'ml-3 px-2.5 py-1 rounded-full text-xs font-medium',
                RUNTIME_BADGE_COLORS[fn.runtime as Runtime]
              )}>
                {RUNTIME_LABELS[fn.runtime as Runtime]}
              </span>
              {/* 函数状态徽章 */}
              <span className={cn(
                'ml-2 px-2.5 py-1 rounded-full text-xs font-medium inline-flex items-center gap-1',
                fn.status === 'active' ? 'text-green-400 bg-green-400/10' :
                fn.status === 'offline' ? 'text-gray-400 bg-gray-400/10' :
                fn.status === 'creating' || fn.status === 'updating' || fn.status === 'building' ? 'text-blue-400 bg-blue-400/10' :
                fn.status === 'failed' ? 'text-red-400 bg-red-400/10' : 'text-gray-400 bg-gray-400/10'
              )}>
                {(fn.status === 'creating' || fn.status === 'updating' || fn.status === 'building') && (
                  <Loader2 className="w-3 h-3 animate-spin" />
                )}
                {fn.status === 'active' && <CheckCircle2 className="w-3 h-3" />}
                {fn.status === 'offline' && <PauseCircle className="w-3 h-3" />}
                {fn.status === 'failed' && <XCircle className="w-3 h-3" />}
                {STATUS_LABELS[fn.status as FunctionStatus] || fn.status}
              </span>
            </div>
            <p className="text-muted-foreground mt-1">
              <span className="font-mono text-sm">{fn.handler}</span>
              <span className="mx-2">·</span>
              更新于 {formatDate(fn.updated_at)}
              {fn.status_message && (
                <>
                  <span className="mx-2">·</span>
                  <span className={fn.status === 'failed' ? 'text-red-400' : ''}>{fn.status_message}</span>
                </>
              )}
            </p>
            {/* 标签区域 */}
            <div className="flex items-center gap-2 mt-2">
              <Tag className="w-4 h-4 text-muted-foreground" />
              {editingTags ? (
                <div className="flex items-center gap-2">
                  <input
                    type="text"
                    value={tagsInput}
                    onChange={(e) => setTagsInput(e.target.value)}
                    placeholder="用逗号分隔标签，如: api, web, 核心"
                    className="px-2 py-1 text-sm bg-input border border-border rounded text-foreground focus:outline-none focus:ring-1 focus:ring-accent/50 w-64"
                    autoFocus
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') handleSaveTags()
                      if (e.key === 'Escape') setEditingTags(false)
                    }}
                  />
                  <button
                    onClick={handleSaveTags}
                    disabled={savingTags}
                    className="px-2 py-1 text-xs bg-accent text-accent-foreground rounded hover:bg-accent/90 disabled:opacity-50"
                  >
                    {savingTags ? '...' : '保存'}
                  </button>
                  <button
                    onClick={() => setEditingTags(false)}
                    className="px-2 py-1 text-xs text-muted-foreground hover:text-foreground"
                  >
                    取消
                  </button>
                </div>
              ) : (
                <div className="flex items-center gap-2">
                  {fn.tags && fn.tags.length > 0 ? (
                    fn.tags.map(tag => (
                      <span key={tag} className="inline-flex items-center px-2 py-0.5 text-xs rounded bg-secondary text-muted-foreground">
                        {tag}
                      </span>
                    ))
                  ) : (
                    <span className="text-xs text-muted-foreground">无标签</span>
                  )}
                  <button
                    onClick={startEditingTags}
                    className="p-1 text-muted-foreground hover:text-foreground hover:bg-secondary rounded transition-colors"
                    title="编辑标签"
                  >
                    <Edit3 className="w-3 h-3" />
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>
        <div className="flex items-center space-x-3">
          {/* 上线/下线按钮 */}
          {fn.status === 'active' && (
            <button
              onClick={async () => {
                if (!confirm('确定要下线该函数吗？下线后函数将无法被调用。')) return
                try {
                  await functionService.offline(id!)
                  await loadFunction()
                } catch (error) {
                  console.error('Failed to offline function:', error)
                  alert('下线失败')
                }
              }}
              className="flex items-center px-4 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700 transition-colors"
            >
              <PauseCircle className="w-5 h-5 mr-2" />
              下线
            </button>
          )}
          {fn.status === 'offline' && (
            <button
              onClick={async () => {
                try {
                  await functionService.online(id!)
                  await loadFunction()
                } catch (error) {
                  console.error('Failed to online function:', error)
                  alert('上线失败')
                }
              }}
              className="flex items-center px-4 py-2 bg-green-600 text-white rounded-lg hover:bg-green-700 transition-colors"
            >
              <PlayCircle className="w-5 h-5 mr-2" />
              上线
            </button>
          )}
          <button
            onClick={() => navigate(`/functions/${id}/workbench`)}
            className="flex items-center px-4 py-2 bg-orange-600 text-white rounded-lg hover:bg-orange-700 transition-colors"
          >
            <Terminal className="w-5 h-5 mr-2" />
            工作台
          </button>
          {hasChanges && (
            <span className="text-sm text-orange-400">有未保存的更改</span>
          )}
          <button
            onClick={handleSave}
            disabled={saving}
            className="flex items-center px-4 py-2 bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors disabled:opacity-50"
          >
            <Save className="w-5 h-5 mr-2" />
            {saving ? '保存中...' : '保存'}
          </button>
        </div>
      </div>

      {/* 标签页 */}
      <div className="border-b border-border">
        <nav className="flex space-x-8">
          {[
            { id: 'code', label: '代码' },
            { id: 'test', label: '测试' },
            { id: 'analytics', label: '分析' },
            { id: 'logs', label: '实时日志' },
            { id: 'config', label: '配置' },
            { id: 'invocations', label: '调用记录' },
            { id: 'versions', label: '版本' },
            { id: 'aliases', label: '别名' },
            { id: 'layers', label: '层' },
            { id: 'environments', label: '环境' },
          ].map((tab) => (
            <button
              key={tab.id}
              onClick={() => handleTabChange(tab.id as Tab)}
              className={cn(
                'py-4 px-1 border-b-2 font-medium text-sm transition-colors',
                activeTab === tab.id
                  ? 'border-accent text-accent'
                  : 'border-transparent text-muted-foreground hover:text-foreground hover:border-border'
              )}
            >
              {tab.label}
            </button>
          ))}
        </nav>
      </div>

      {/* 标签页内容 */}
      {activeTab === 'code' && (
        <div className="bg-card rounded-xl border border-border overflow-hidden h-[600px]">
          <Editor
            height="100%"
            language={languageMap[fn.runtime] || 'plaintext'}
            value={code}
            onChange={(value) => setCode(value || '')}
            theme="vs-dark"
            options={{
              minimap: { enabled: false },
              fontSize: 14,
              lineNumbers: 'on',
              scrollBeyondLastLine: false,
              wordWrap: 'on',
              tabSize: 2,
            }}
          />
        </div>
      )}

      {activeTab === 'test' && (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* 请求面板 */}
          <div className="bg-card rounded-xl border border-border overflow-hidden">
            <div className="px-4 py-3 bg-secondary/50 border-b border-border flex items-center justify-between">
              <h3 className="text-sm font-medium text-foreground">请求</h3>
              <button
                onClick={handleTest}
                disabled={testing}
                className="flex items-center px-3 py-1.5 bg-accent text-accent-foreground text-sm rounded-lg hover:bg-accent/90 disabled:opacity-50 transition-colors"
              >
                <Play className="w-4 h-4 mr-1" />
                {testing ? '执行中...' : '执行'}
              </button>
            </div>
            <Editor
              height="400px"
              language="json"
              value={testPayload}
              onChange={(value) => setTestPayload(value || '')}
              theme="vs-dark"
              options={{
                minimap: { enabled: false },
                fontSize: 14,
                lineNumbers: 'on',
                scrollBeyondLastLine: false,
              }}
            />
          </div>

          {/* 响应面板 */}
          <div className="bg-card rounded-xl border border-border overflow-hidden">
            <div className="px-4 py-3 bg-secondary/50 border-b border-border">
              <div className="flex items-center justify-between">
                <h3 className="text-sm font-medium text-foreground">响应</h3>
                {testResult && (
                  <div className="flex items-center space-x-4 text-sm">
                    {testResult.status_code >= 200 && testResult.status_code < 300 ? (
                      <span className="flex items-center text-green-400">
                        <CheckCircle2 className="w-4 h-4 mr-1" />
                        成功
                      </span>
                    ) : (
                      <span className="flex items-center text-red-400">
                        <XCircle className="w-4 h-4 mr-1" />
                        失败
                      </span>
                    )}
                    <span className="flex items-center text-muted-foreground">
                      <Cpu className="w-4 h-4 mr-1" />
                      {testResult.status_code}
                    </span>
                    <span className="flex items-center text-muted-foreground">
                      <Clock className="w-4 h-4 mr-1" />
                      {formatDuration(testResult.duration_ms)}
                    </span>
                    {testResult.cold_start && (
                      <span className="text-orange-400 flex items-center">
                        <Zap className="w-4 h-4 mr-1" />
                        冷启动
                      </span>
                    )}
                  </div>
                )}
              </div>
            </div>
            <div className="h-[400px] overflow-auto">
              {testing ? (
                <div className="flex items-center justify-center h-full">
                  <RefreshCw className="w-8 h-8 text-accent animate-spin" />
                </div>
              ) : testResult ? (
                <pre className="p-4 text-sm font-mono whitespace-pre-wrap text-foreground h-full">
                  {testResult.error
                    ? testResult.error
                    : formatJson(testResult.body ?? {})}
                </pre>
              ) : (
                <div className="flex items-center justify-center h-full text-muted-foreground">
                  点击"执行"按钮测试函数
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {activeTab === 'analytics' && (
        <div className="space-y-4">
          {/* 时间范围选择 */}
          <div className="flex items-center justify-between">
            <h3 className="text-lg font-semibold text-foreground">函数性能分析</h3>
            <div className="flex items-center gap-2">
              <select
                value={analyticsPeriod}
                onChange={(e) => setAnalyticsPeriod(e.target.value)}
                className="px-3 py-1.5 bg-card border border-border rounded-lg text-sm focus:outline-none focus:ring-1 focus:ring-accent/50"
              >
                <option value="1h">1 小时</option>
                <option value="6h">6 小时</option>
                <option value="24h">24 小时</option>
                <option value="7d">7 天</option>
              </select>
              <button
                onClick={loadAnalytics}
                disabled={analyticsLoading}
                className="p-1.5 text-muted-foreground hover:text-foreground rounded-lg hover:bg-secondary transition-colors disabled:opacity-50"
              >
                <RefreshCw className={cn('w-4 h-4', analyticsLoading && 'animate-spin')} />
              </button>
            </div>
          </div>

          {analyticsLoading && !analyticsStats ? (
            <div className="flex items-center justify-center h-64">
              <RefreshCw className="w-6 h-6 text-accent animate-spin" />
            </div>
          ) : (
            <>
              {/* 核心指标卡片 */}
              <div className="grid grid-cols-4 gap-3">
                <div className="bg-card rounded-xl border border-border p-4">
                  <div className="flex items-center gap-3">
                    <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-violet-500/10">
                      <Activity className="w-4 h-4 text-violet-500" />
                    </div>
                    <div>
                      <p className="text-xs text-muted-foreground">调用次数</p>
                      <p className="text-xl font-bold text-foreground">{formatNumber(analyticsStats?.total_invocations || 0)}</p>
                    </div>
                  </div>
                </div>
                <div className="bg-card rounded-xl border border-border p-4">
                  <div className="flex items-center gap-3">
                    <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-emerald-500/10">
                      <CheckCircle2 className="w-4 h-4 text-emerald-500" />
                    </div>
                    <div>
                      <p className="text-xs text-muted-foreground">成功率</p>
                      <p className="text-xl font-bold text-foreground">{formatPercent(analyticsStats?.success_rate || 0)}</p>
                    </div>
                  </div>
                </div>
                <div className="bg-card rounded-xl border border-border p-4">
                  <div className="flex items-center gap-3">
                    <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-rose-500/10">
                      <XCircle className="w-4 h-4 text-rose-500" />
                    </div>
                    <div>
                      <p className="text-xs text-muted-foreground">错误数</p>
                      <p className="text-xl font-bold text-foreground">{formatNumber((analyticsStats?.failed_count || 0) + (analyticsStats?.timeout_count || 0))}</p>
                    </div>
                  </div>
                </div>
                <div className="bg-card rounded-xl border border-border p-4">
                  <div className="flex items-center gap-3">
                    <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-amber-500/10">
                      <Zap className="w-4 h-4 text-amber-500" />
                    </div>
                    <div>
                      <p className="text-xs text-muted-foreground">冷启动率</p>
                      <p className="text-xl font-bold text-foreground">{formatPercent(analyticsStats?.cold_start_rate || 0)}</p>
                    </div>
                  </div>
                </div>
              </div>

              {/* 延迟指标 */}
              <div className="grid grid-cols-6 gap-3">
                <div className="bg-card rounded-xl border border-border p-4">
                  <p className="text-xs text-muted-foreground mb-1">平均延迟</p>
                  <p className="text-lg font-bold text-foreground">{formatLatency(analyticsStats?.avg_latency_ms || 0)}</p>
                </div>
                <div className="bg-card rounded-xl border border-border p-4">
                  <p className="text-xs text-muted-foreground mb-1">P50</p>
                  <p className="text-lg font-bold text-foreground">{formatLatency(analyticsStats?.p50_latency_ms || 0)}</p>
                </div>
                <div className="bg-card rounded-xl border border-border p-4">
                  <p className="text-xs text-muted-foreground mb-1">P95</p>
                  <p className="text-lg font-bold text-foreground">{formatLatency(analyticsStats?.p95_latency_ms || 0)}</p>
                </div>
                <div className="bg-card rounded-xl border border-border p-4">
                  <p className="text-xs text-muted-foreground mb-1">P99</p>
                  <p className="text-lg font-bold text-foreground">{formatLatency(analyticsStats?.p99_latency_ms || 0)}</p>
                </div>
                <div className="bg-card rounded-xl border border-border p-4">
                  <p className="text-xs text-muted-foreground mb-1">最小</p>
                  <p className="text-lg font-bold text-foreground">{formatLatency(analyticsStats?.min_latency_ms || 0)}</p>
                </div>
                <div className="bg-card rounded-xl border border-border p-4">
                  <p className="text-xs text-muted-foreground mb-1">最大</p>
                  <p className="text-lg font-bold text-foreground">{formatLatency(analyticsStats?.max_latency_ms || 0)}</p>
                </div>
              </div>

              {/* 图表区域 */}
              <div className="grid grid-cols-2 gap-4">
                {/* 趋势图 */}
                <div className="bg-card rounded-xl border border-border p-4">
                  <h4 className="text-sm font-medium text-foreground mb-3">调用趋势</h4>
                  {analyticsTrends.length > 0 ? (
                    <ReactECharts
                      option={{
                        backgroundColor: 'transparent',
                        tooltip: {
                          trigger: 'axis',
                          backgroundColor: 'rgba(17, 17, 17, 0.95)',
                          borderColor: 'rgba(255, 255, 255, 0.1)',
                          textStyle: { color: '#fff', fontSize: 12 },
                        },
                        legend: {
                          data: ['调用数', '错误数'],
                          bottom: 0,
                          textStyle: { color: 'rgba(255, 255, 255, 0.5)', fontSize: 10 },
                        },
                        grid: { left: '3%', right: '3%', bottom: '15%', top: '5%', containLabel: true },
                        xAxis: {
                          type: 'category',
                          boundaryGap: false,
                          data: analyticsTrends.map(d => {
                            const date = new Date(d.timestamp)
                            return `${date.getHours()}:${String(date.getMinutes()).padStart(2, '0')}`
                          }),
                          axisLine: { lineStyle: { color: 'rgba(255, 255, 255, 0.1)' } },
                          axisLabel: { color: 'rgba(255, 255, 255, 0.4)', fontSize: 10 },
                        },
                        yAxis: {
                          type: 'value',
                          axisLine: { show: false },
                          splitLine: { lineStyle: { color: 'rgba(255, 255, 255, 0.05)' } },
                          axisLabel: { color: 'rgba(255, 255, 255, 0.4)', fontSize: 10 },
                        },
                        series: [
                          {
                            name: '调用数',
                            type: 'line',
                            smooth: true,
                            symbol: 'none',
                            data: analyticsTrends.map(d => d.invocations),
                            itemStyle: { color: '#10b981' },
                            areaStyle: {
                              color: { type: 'linear', x: 0, y: 0, x2: 0, y2: 1,
                                colorStops: [{ offset: 0, color: 'rgba(16, 185, 129, 0.3)' }, { offset: 1, color: 'rgba(16, 185, 129, 0.02)' }],
                              },
                            },
                          },
                          {
                            name: '错误数',
                            type: 'line',
                            smooth: true,
                            symbol: 'none',
                            data: analyticsTrends.map(d => d.errors),
                            itemStyle: { color: '#ef4444' },
                          },
                        ],
                      }}
                      style={{ height: '200px', width: '100%' }}
                      opts={{ renderer: 'svg' }}
                    />
                  ) : (
                    <div className="h-48 flex items-center justify-center text-muted-foreground text-sm">暂无数据</div>
                  )}
                </div>

                {/* 延迟分布 */}
                <div className="bg-card rounded-xl border border-border p-4">
                  <h4 className="text-sm font-medium text-foreground mb-3">延迟分布</h4>
                  {latencyDistribution.length > 0 ? (
                    <ReactECharts
                      option={{
                        backgroundColor: 'transparent',
                        tooltip: {
                          trigger: 'axis',
                          backgroundColor: 'rgba(17, 17, 17, 0.95)',
                          borderColor: 'rgba(255, 255, 255, 0.1)',
                          textStyle: { color: '#fff', fontSize: 12 },
                        },
                        grid: { left: '3%', right: '3%', bottom: '10%', top: '5%', containLabel: true },
                        xAxis: {
                          type: 'category',
                          data: latencyDistribution.map(d => d.bucket),
                          axisLine: { lineStyle: { color: 'rgba(255, 255, 255, 0.1)' } },
                          axisLabel: { color: 'rgba(255, 255, 255, 0.4)', fontSize: 9, rotate: 30 },
                        },
                        yAxis: {
                          type: 'value',
                          axisLine: { show: false },
                          splitLine: { lineStyle: { color: 'rgba(255, 255, 255, 0.05)' } },
                          axisLabel: { color: 'rgba(255, 255, 255, 0.4)', fontSize: 10 },
                        },
                        series: [{
                          type: 'bar',
                          data: latencyDistribution.map(d => d.count),
                          itemStyle: {
                            color: {
                              type: 'linear',
                              x: 0, y: 0, x2: 0, y2: 1,
                              colorStops: [
                                { offset: 0, color: '#3b82f6' },
                                { offset: 1, color: '#1d4ed8' },
                              ],
                            },
                            borderRadius: [4, 4, 0, 0],
                          },
                        }],
                      }}
                      style={{ height: '200px', width: '100%' }}
                      opts={{ renderer: 'svg' }}
                    />
                  ) : (
                    <div className="h-48 flex items-center justify-center text-muted-foreground text-sm">暂无数据</div>
                  )}
                </div>
              </div>

              {/* 冷启动和计费信息 */}
              <div className="grid grid-cols-3 gap-4">
                <div className="bg-card rounded-xl border border-border p-4">
                  <h4 className="text-sm font-medium text-foreground mb-3">冷启动</h4>
                  <div className="grid grid-cols-2 gap-4">
                    <div>
                      <p className="text-xs text-muted-foreground">冷启动次数</p>
                      <p className="text-lg font-bold text-foreground">{formatNumber(analyticsStats?.cold_start_count || 0)}</p>
                    </div>
                    <div>
                      <p className="text-xs text-muted-foreground">平均冷启动耗时</p>
                      <p className="text-lg font-bold text-foreground">{formatLatency(analyticsStats?.avg_cold_start_ms || 0)}</p>
                    </div>
                  </div>
                </div>
                <div className="bg-card rounded-xl border border-border p-4">
                  <h4 className="text-sm font-medium text-foreground mb-3">错误分析</h4>
                  <div className="grid grid-cols-2 gap-4">
                    <div>
                      <p className="text-xs text-muted-foreground">失败次数</p>
                      <p className="text-lg font-bold text-rose-500">{formatNumber(analyticsStats?.failed_count || 0)}</p>
                    </div>
                    <div>
                      <p className="text-xs text-muted-foreground">超时次数</p>
                      <p className="text-lg font-bold text-amber-500">{formatNumber(analyticsStats?.timeout_count || 0)}</p>
                    </div>
                  </div>
                </div>
                <div className="bg-card rounded-xl border border-border p-4">
                  <h4 className="text-sm font-medium text-foreground mb-3">计费时长</h4>
                  <div>
                    <p className="text-xs text-muted-foreground">总执行时间</p>
                    <p className="text-lg font-bold text-foreground">{formatLatency(analyticsStats?.total_duration_ms || 0)}</p>
                    <p className="text-xs text-muted-foreground mt-1">
                      约 {((analyticsStats?.total_duration_ms || 0) / 1000 / 3600).toFixed(4)} 小时
                    </p>
                  </div>
                </div>
              </div>
            </>
          )}
        </div>
      )}

      {activeTab === 'logs' && (
        <div className="h-[600px] flex flex-col">
          <LogStreamViewer functionId={fn.id} className="flex-1" />
        </div>
      )}

      {activeTab === 'config' && (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* 基本信息 */}
          <div className="bg-card rounded-xl border border-border p-6">
            <h3 className="text-lg font-semibold text-foreground mb-4">基本信息</h3>
            <dl className="space-y-4">
              <div>
                <dt className="text-sm font-medium text-muted-foreground">函数 ID</dt>
                <dd className="mt-1 text-sm text-foreground font-mono">{fn.id}</dd>
              </div>
              <div>
                <dt className="text-sm font-medium text-muted-foreground">运行时</dt>
                <dd className="mt-1 text-sm text-foreground">{RUNTIME_LABELS[fn.runtime as Runtime]}</dd>
              </div>
              <div>
                <dt className="text-sm font-medium text-muted-foreground">处理函数</dt>
                <dd className="mt-1 text-sm text-foreground font-mono">{fn.handler}</dd>
              </div>
              <div>
                <dt className="text-sm font-medium text-muted-foreground">版本</dt>
                <dd className="mt-1 text-sm text-foreground">v{fn.version}</dd>
              </div>
              <div>
                <dt className="text-sm font-medium text-muted-foreground">创建时间</dt>
                <dd className="mt-1 text-sm text-foreground">{formatDate(fn.created_at)}</dd>
              </div>
            </dl>
          </div>

          {/* 资源配置 */}
          <div className="bg-card rounded-xl border border-border p-6">
            <h3 className="text-lg font-semibold text-foreground mb-4">资源配置</h3>
            <div className="space-y-6">
              <div className="grid grid-cols-2 gap-6">
                <div>
                  <label className="block text-sm font-medium text-muted-foreground mb-2">
                    内存 (MB)
                  </label>
                  <select
                    value={configForm.memory_mb}
                    onChange={(e) =>
                      setConfigForm({ ...configForm, memory_mb: Number(e.target.value) })
                    }
                    className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring transition-all"
                  >
                    <option value={128}>128 MB</option>
                    <option value={256}>256 MB</option>
                    <option value={512}>512 MB</option>
                    <option value={1024}>1024 MB</option>
                    <option value={2048}>2048 MB</option>
                  </select>
                </div>
                <div>
                  <label className="block text-sm font-medium text-muted-foreground mb-2">
                    超时时间 (秒)
                  </label>
                  <input
                    type="number"
                    value={configForm.timeout_sec}
                    onChange={(e) =>
                      setConfigForm({ ...configForm, timeout_sec: Number(e.target.value) })
                    }
                    min={1}
                    max={900}
                    className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring transition-all"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-muted-foreground mb-2">
                    最大并发数
                  </label>
                  <input
                    type="number"
                    value={configForm.max_concurrency}
                    onChange={(e) =>
                      setConfigForm({ ...configForm, max_concurrency: Number(e.target.value) })
                    }
                    min={0}
                    max={1000}
                    className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring transition-all"
                  />
                  <p className="mt-1 text-xs text-muted-foreground">
                    0 表示不限制
                  </p>
                </div>
              </div>

              <div>
                <label className="block text-sm font-medium text-muted-foreground mb-2">
                  定时触发 (Cron 表达式)
                </label>
                <input
                  type="text"
                  value={configForm.cron_expression}
                  onChange={(e) =>
                    setConfigForm({ ...configForm, cron_expression: e.target.value })
                  }
                  placeholder="*/5 * * * * *"
                  className={cn(
                    "w-full px-4 py-2 bg-input border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring transition-all font-mono text-sm",
                    configForm.cron_expression && !validateCron(configForm.cron_expression).valid
                      ? "border-destructive"
                      : "border-border"
                  )}
                />
                {configForm.cron_expression ? (
                  validateCron(configForm.cron_expression).valid ? (
                    <div className="mt-2 flex items-center gap-2 text-xs text-emerald-500">
                      <Clock className="w-3.5 h-3.5" />
                      {describeCron(configForm.cron_expression)}
                    </div>
                  ) : (
                    <div className="mt-2 flex items-center gap-2 text-xs text-destructive">
                      <AlertCircle className="w-3.5 h-3.5" />
                      {validateCron(configForm.cron_expression).error}
                    </div>
                  )
                ) : (
                  <p className="mt-1 text-xs text-muted-foreground">
                    支持秒级 Cron (秒 分 时 日 月 周)，例如 "*/10 * * * * *" (每10秒)。留空则不启用。
                  </p>
                )}
              </div>

              <div className="w-full h-px bg-border my-2" />

              <div>
                <label className="block text-sm font-medium text-muted-foreground mb-2">
                  HTTP 路径 (可选)
                </label>
                <input
                  type="text"
                  value={configForm.http_path}
                  onChange={(e) =>
                    setConfigForm({ ...configForm, http_path: e.target.value })
                  }
                  placeholder="/api/hello"
                  className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring transition-all font-mono text-sm"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-muted-foreground mb-2">
                  HTTP 方法 (可选)
                </label>
                <input
                  type="text"
                  value={configForm.http_methods.join(',')}
                  onChange={(e) =>
                    setConfigForm({
                      ...configForm,
                      http_methods: e.target.value
                        .split(',')
                        .filter((m) => m)
                        .map((m) => m.trim().toUpperCase()),
                    })
                  }
                  placeholder="GET,POST"
                  className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring transition-all font-mono text-sm"
                />
                <p className="mt-1 text-xs text-muted-foreground">
                  使用逗号分隔，例如 "GET,POST"。留空则不限制方法。
                </p>
              </div>
            </div>
          </div>

          {/* Webhook 配置 */}
          <div className="bg-card rounded-xl border border-border p-6 lg:col-span-2">
            <div className="flex items-center justify-between mb-4">
              <div className="flex items-center gap-3">
                <div className={cn(
                  'p-2 rounded-lg',
                  fn.webhook_enabled ? 'bg-green-500/10' : 'bg-muted'
                )}>
                  <Link2 className={cn(
                    'w-5 h-5',
                    fn.webhook_enabled ? 'text-green-500' : 'text-muted-foreground'
                  )} />
                </div>
                <div>
                  <h3 className="text-lg font-semibold text-foreground">Webhook</h3>
                  <p className="text-sm text-muted-foreground">
                    {fn.webhook_enabled ? '已启用 - 外部可通过 URL 触发函数' : '未启用'}
                  </p>
                </div>
              </div>
              <button
                onClick={fn.webhook_enabled ? handleDisableWebhook : handleEnableWebhook}
                disabled={webhookOperating}
                className={cn(
                  'inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg transition-colors',
                  fn.webhook_enabled
                    ? 'bg-red-500/10 text-red-500 hover:bg-red-500/20'
                    : 'bg-green-500/10 text-green-500 hover:bg-green-500/20',
                  webhookOperating && 'opacity-50 cursor-not-allowed'
                )}
              >
                {webhookOperating ? (
                  <Loader2 className="w-4 h-4 animate-spin" />
                ) : fn.webhook_enabled ? (
                  <PowerOff className="w-4 h-4" />
                ) : (
                  <Power className="w-4 h-4" />
                )}
                {fn.webhook_enabled ? '禁用' : '启用'}
              </button>
            </div>

            {fn.webhook_enabled && (
              <div className="space-y-4 pt-4 border-t border-border">
                {/* Webhook URL */}
                <div>
                  <label className="block text-sm font-medium text-muted-foreground mb-2">
                    Webhook URL
                  </label>
                  <div className="flex items-center gap-2">
                    <input
                      type="text"
                      value={webhookUrl || `${window.location.origin}/api/v1/webhook/${fn.id}`}
                      readOnly
                      className="flex-1 px-4 py-2 bg-secondary border border-border rounded-lg text-foreground font-mono text-sm"
                    />
                    <button
                      onClick={async () => {
                        const url = webhookUrl || `${window.location.origin}/api/v1/webhook/${fn.id}`
                        await navigator.clipboard.writeText(url)
                        toast.success('URL 已复制')
                      }}
                      className="p-2 text-muted-foreground hover:text-foreground hover:bg-muted rounded-lg transition-colors"
                      title="复制 URL"
                    >
                      <Copy className="w-4 h-4" />
                    </button>
                    <a
                      href={webhookUrl || `${window.location.origin}/api/v1/webhook/${fn.id}`}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="p-2 text-muted-foreground hover:text-foreground hover:bg-muted rounded-lg transition-colors"
                      title="在新窗口打开"
                    >
                      <ExternalLink className="w-4 h-4" />
                    </a>
                  </div>
                </div>

                {/* Webhook 密钥 */}
                <div>
                  <label className="block text-sm font-medium text-muted-foreground mb-2">
                    Webhook 密钥
                  </label>
                  <div className="flex items-center gap-2">
                    <div className="flex-1 flex items-center gap-2 px-4 py-2 bg-secondary border border-border rounded-lg">
                      <Key className="w-4 h-4 text-muted-foreground" />
                      <input
                        type={showWebhookKey ? 'text' : 'password'}
                        value={webhookKey || fn.webhook_key || '••••••••••••••••'}
                        readOnly
                        className="flex-1 bg-transparent text-foreground font-mono text-sm outline-none"
                      />
                    </div>
                    <button
                      onClick={() => setShowWebhookKey(!showWebhookKey)}
                      className="p-2 text-muted-foreground hover:text-foreground hover:bg-muted rounded-lg transition-colors"
                      title={showWebhookKey ? '隐藏密钥' : '显示密钥'}
                    >
                      {showWebhookKey ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                    </button>
                    <button
                      onClick={async () => {
                        const key = webhookKey || fn.webhook_key
                        if (key) {
                          await navigator.clipboard.writeText(key)
                          toast.success('密钥已复制')
                        }
                      }}
                      className="p-2 text-muted-foreground hover:text-foreground hover:bg-muted rounded-lg transition-colors"
                      title="复制密钥"
                    >
                      <Copy className="w-4 h-4" />
                    </button>
                    <button
                      onClick={handleRegenerateWebhookKey}
                      disabled={webhookOperating}
                      className="p-2 text-orange-500 hover:text-orange-400 hover:bg-orange-500/10 rounded-lg transition-colors"
                      title="重新生成密钥"
                    >
                      <RefreshCw className={cn('w-4 h-4', webhookOperating && 'animate-spin')} />
                    </button>
                  </div>
                  <p className="mt-2 text-xs text-muted-foreground">
                    使用密钥进行请求验证。在请求头中添加 <code className="px-1 py-0.5 bg-muted rounded text-foreground">X-Webhook-Key</code> 或查询参数 <code className="px-1 py-0.5 bg-muted rounded text-foreground">key</code>
                  </p>
                </div>

                {/* 使用说明 */}
                <div className="p-4 bg-muted/50 rounded-lg">
                  <h4 className="text-sm font-medium text-foreground mb-2">使用说明</h4>
                  <div className="space-y-2 text-xs text-muted-foreground">
                    <p>通过 POST 请求调用 Webhook URL，请求体将作为函数输入：</p>
                    <pre className="p-2 bg-background rounded text-foreground font-mono overflow-x-auto">
{`curl -X POST \\
  -H "X-Webhook-Key: YOUR_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"message": "hello"}' \\
  ${webhookUrl || `${window.location.origin}/api/v1/webhook/${fn.id}`}`}
                    </pre>
                  </div>
                </div>
              </div>
            )}
          </div>
        </div>
      )}

      {activeTab === 'invocations' && (
        <div className="bg-card rounded-xl border border-border overflow-hidden">
          <div className="px-6 py-4 border-b border-border flex items-center justify-between">
            <div>
              <h3 className="text-lg font-semibold text-foreground">调用记录</h3>
              <p className="text-sm text-muted-foreground mt-1">共 {invocationsTotal} 条记录</p>
            </div>
            <button
              onClick={loadInvocations}
              disabled={invocationsLoading}
              className="flex items-center px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground hover:bg-secondary rounded-lg transition-colors"
            >
              <RefreshCw className={cn('w-4 h-4 mr-1', invocationsLoading && 'animate-spin')} />
              刷新
            </button>
          </div>

          {invocationsLoading ? (
            <div className="flex items-center justify-center py-12">
              <RefreshCw className="w-8 h-8 text-accent animate-spin" />
            </div>
          ) : invocations.length === 0 ? (
            <div className="text-center py-12 text-muted-foreground">
              暂无调用记录
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="min-w-full divide-y divide-border">
                <thead className="bg-secondary/50">
                  <tr>
                    <th className="px-6 py-3 text-left text-xs font-medium text-muted-foreground uppercase tracking-wider">
                      状态
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-medium text-muted-foreground uppercase tracking-wider">
                      请求 ID
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-medium text-muted-foreground uppercase tracking-wider">
                      执行时间
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-medium text-muted-foreground uppercase tracking-wider">
                      冷启动
                    </th>
                    <th className="px-6 py-3 text-left text-xs font-medium text-muted-foreground uppercase tracking-wider">
                      创建时间
                    </th>
                    <th className="px-6 py-3 text-right text-xs font-medium text-muted-foreground uppercase tracking-wider">
                      操作
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {invocations.map((inv) => (
                    <tr key={inv.id} className="hover:bg-secondary/30 transition-colors">
                      <td className="px-6 py-4 whitespace-nowrap">
                        <StatusBadge status={inv.status} />
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <span className="text-sm font-mono text-muted-foreground">
                          {inv.id.slice(0, 8)}...
                        </span>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        <span className="text-sm text-foreground flex items-center">
                          <Clock className="w-4 h-4 mr-1 text-muted-foreground" />
                          {formatDuration(inv.duration_ms)}
                        </span>
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap">
                        {inv.cold_start ? (
                          <span className="inline-flex items-center text-orange-400 text-sm">
                            <Zap className="w-4 h-4 mr-1" />
                            是
                          </span>
                        ) : (
                          <span className="text-sm text-muted-foreground">否</span>
                        )}
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-sm text-muted-foreground">
                        {formatDate(inv.created_at)}
                      </td>
                      <td className="px-6 py-4 whitespace-nowrap text-right text-sm">
                        <div className="flex items-center justify-end gap-2">
                          <button
                            onClick={() => handleReplayInvocation(inv.id)}
                            disabled={replayingId === inv.id}
                            className="text-accent hover:text-accent/80 inline-flex items-center transition-colors disabled:opacity-50"
                            title="重放此调用"
                          >
                            {replayingId === inv.id ? (
                              <Loader2 className="w-4 h-4 animate-spin" />
                            ) : (
                              <RotateCcw className="w-4 h-4" />
                            )}
                          </button>
                          <Link
                            to={`/invocations/${inv.id}`}
                            className="text-accent hover:text-accent/80 inline-flex items-center transition-colors"
                          >
                            详情
                            <ExternalLink className="w-3 h-3 ml-1" />
                          </Link>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* 版本标签页 */}
      {activeTab === 'versions' && (
        <div className="bg-card rounded-xl border border-border overflow-hidden">
          <div className="px-6 py-4 border-b border-border flex items-center justify-between">
            <div>
              <h3 className="text-lg font-semibold text-foreground">版本历史</h3>
              <p className="text-sm text-muted-foreground mt-1">共 {versions.length} 个版本</p>
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={loadVersions}
                disabled={versionsLoading}
                className="flex items-center px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground hover:bg-secondary rounded-lg transition-colors"
              >
                <RefreshCw className={cn('w-4 h-4 mr-1', versionsLoading && 'animate-spin')} />
                刷新
              </button>
              {versions.length >= 2 && (
                <button
                  onClick={() => setShowVersionDiff(true)}
                  className="flex items-center px-3 py-1.5 text-sm bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors"
                >
                  <GitCompare className="w-4 h-4 mr-1" />
                  版本对比
                </button>
              )}
            </div>
          </div>

          {versionsLoading ? (
            <div className="flex items-center justify-center py-12">
              <RefreshCw className="w-8 h-8 text-accent animate-spin" />
            </div>
          ) : versions.length === 0 ? (
            <div className="text-center py-12 text-muted-foreground">
              暂无版本历史
            </div>
          ) : (
            <div className="divide-y divide-border">
              {versions.map((version) => (
                <div key={version.id} className="px-6 py-4 hover:bg-secondary/30 transition-colors">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-4">
                      <span className={cn(
                        'px-2.5 py-1 rounded-full text-xs font-medium',
                        version.version === fn?.version
                          ? 'bg-green-400/10 text-green-400'
                          : 'bg-secondary text-muted-foreground'
                      )}>
                        v{version.version}
                        {version.version === fn?.version && ' (当前)'}
                      </span>
                      <span className="text-sm font-mono text-muted-foreground">
                        {version.code_hash.slice(0, 8)}
                      </span>
                    </div>
                    <div className="flex items-center gap-4">
                      <span className="text-sm text-muted-foreground">
                        {formatDate(version.created_at)}
                      </span>
                      {version.version !== fn?.version && (
                        <button
                          onClick={() => handleRollback(version.version)}
                          className="px-3 py-1.5 text-sm bg-orange-600 text-white rounded-lg hover:bg-orange-700 transition-colors"
                        >
                          回滚
                        </button>
                      )}
                    </div>
                  </div>
                  {version.description && (
                    <p className="mt-2 text-sm text-muted-foreground">{version.description}</p>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* 别名标签页 */}
      {activeTab === 'aliases' && (
        <div className="bg-card rounded-xl border border-border overflow-hidden">
          <div className="px-6 py-4 border-b border-border flex items-center justify-between">
            <div>
              <h3 className="text-lg font-semibold text-foreground">别名管理</h3>
              <p className="text-sm text-muted-foreground mt-1">配置流量分配和灰度发布</p>
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={loadAliases}
                disabled={aliasesLoading}
                className="flex items-center px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground hover:bg-secondary rounded-lg transition-colors"
              >
                <RefreshCw className={cn('w-4 h-4 mr-1', aliasesLoading && 'animate-spin')} />
                刷新
              </button>
              <button
                onClick={() => {
                  setEditingAlias(null)
                  setShowAliasModal(true)
                }}
                className="flex items-center px-3 py-1.5 text-sm bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors"
              >
                创建别名
              </button>
            </div>
          </div>

          {aliasesLoading ? (
            <div className="flex items-center justify-center py-12">
              <RefreshCw className="w-8 h-8 text-accent animate-spin" />
            </div>
          ) : aliases.length === 0 ? (
            <div className="text-center py-12 text-muted-foreground">
              暂无别名，点击"创建别名"开始配置流量分配
            </div>
          ) : (
            <div className="divide-y divide-border">
              {aliases.map((alias) => (
                <div key={alias.id} className="px-6 py-4 hover:bg-secondary/30 transition-colors">
                  <div className="flex items-center justify-between mb-3">
                    <div className="flex items-center gap-3">
                      <span className="px-2.5 py-1 rounded-full text-xs font-medium bg-accent/10 text-accent">
                        {alias.name}
                      </span>
                      {alias.description && (
                        <span className="text-sm text-muted-foreground">{alias.description}</span>
                      )}
                    </div>
                    <div className="flex items-center gap-2">
                      <button
                        onClick={() => {
                          setEditingAlias(alias)
                          setShowAliasModal(true)
                        }}
                        className="px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground hover:bg-secondary rounded-lg transition-colors"
                      >
                        编辑
                      </button>
                      <button
                        onClick={() => handleDeleteAlias(alias.name)}
                        className="px-3 py-1.5 text-sm text-red-400 hover:text-red-300 hover:bg-red-400/10 rounded-lg transition-colors"
                      >
                        删除
                      </button>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    {alias.routing_config.weights.map((w, idx) => (
                      <div key={idx} className="flex items-center gap-2">
                        <span className="text-sm text-foreground">v{w.version}</span>
                        <div className="w-24 h-2 bg-secondary rounded-full overflow-hidden">
                          <div
                            className="h-full bg-accent rounded-full"
                            style={{ width: `${w.weight}%` }}
                          />
                        </div>
                        <span className="text-sm text-muted-foreground">{w.weight}%</span>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* 层标签页 */}
      {activeTab === 'layers' && (
        <div className="bg-card rounded-xl border border-border overflow-hidden">
          <div className="px-6 py-4 border-b border-border flex items-center justify-between">
            <div>
              <h3 className="text-lg font-semibold text-foreground">依赖层</h3>
              <p className="text-sm text-muted-foreground mt-1">管理函数的共享依赖</p>
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={loadFunctionLayers}
                disabled={layersLoading}
                className="flex items-center px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground hover:bg-secondary rounded-lg transition-colors"
              >
                <RefreshCw className={cn('w-4 h-4 mr-1', layersLoading && 'animate-spin')} />
                刷新
              </button>
              <button
                onClick={() => setShowLayerBindingModal(true)}
                className="flex items-center px-3 py-1.5 text-sm bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors"
              >
                <Layers className="w-4 h-4 mr-1" />
                配置层
              </button>
            </div>
          </div>

          {layersLoading ? (
            <div className="flex items-center justify-center py-12">
              <RefreshCw className="w-8 h-8 text-accent animate-spin" />
            </div>
          ) : functionLayers.length === 0 ? (
            <div className="text-center py-12 text-muted-foreground">
              <Layers className="w-12 h-12 mx-auto mb-3 opacity-30" />
              <p>此函数暂未配置依赖层</p>
              <button
                onClick={() => setShowLayerBindingModal(true)}
                className="mt-3 text-sm text-accent hover:underline"
              >
                点击配置依赖层
              </button>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {functionLayers.map((layer) => (
                <div key={layer.layer_id} className="px-6 py-4 hover:bg-secondary/30 transition-colors">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3">
                      <span className="text-sm font-medium text-foreground">{layer.layer_name}</span>
                      <span className="px-2 py-0.5 rounded text-xs bg-secondary text-muted-foreground">
                        v{layer.layer_version}
                      </span>
                    </div>
                    <span className="text-sm text-muted-foreground">加载顺序: {layer.order}</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* 环境配置标签页 */}
      {activeTab === 'environments' && (
        <div className="bg-card rounded-xl border border-border overflow-hidden">
          <div className="px-6 py-4 border-b border-border flex items-center justify-between">
            <div>
              <h3 className="text-lg font-semibold text-foreground">环境配置</h3>
              <p className="text-sm text-muted-foreground mt-1">管理不同环境下的函数配置</p>
            </div>
            <button
              onClick={loadEnvConfigs}
              disabled={envConfigsLoading}
              className="flex items-center px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground hover:bg-secondary rounded-lg transition-colors"
            >
              <RefreshCw className={cn('w-4 h-4 mr-1', envConfigsLoading && 'animate-spin')} />
              刷新
            </button>
          </div>

          {envConfigsLoading ? (
            <div className="flex items-center justify-center py-12">
              <RefreshCw className="w-8 h-8 text-accent animate-spin" />
            </div>
          ) : envConfigs.length === 0 ? (
            <div className="text-center py-12 text-muted-foreground">
              <p>暂无环境配置</p>
              <p className="text-sm mt-2">系统会自动为新函数创建默认环境配置</p>
            </div>
          ) : (
            <div className="divide-y divide-border">
              {envConfigs.map((config) => (
                <div key={config.environment_id} className="px-6 py-4 hover:bg-secondary/30 transition-colors">
                  <div className="flex items-center justify-between mb-3">
                    <div className="flex items-center gap-3">
                      <span className="px-2.5 py-1 rounded-full text-xs font-medium bg-blue-400/10 text-blue-400">
                        {config.environment_name || config.environment_id}
                      </span>
                      {config.active_alias && (
                        <span className="text-sm text-muted-foreground">
                          别名: {config.active_alias}
                        </span>
                      )}
                    </div>
                    <div className="flex items-center gap-2">
                      <span className="text-sm text-muted-foreground">
                        更新于 {formatDate(config.updated_at)}
                      </span>
                      <button
                        onClick={() => {
                          setEditingEnvConfig(config)
                          setShowEnvConfigModal(true)
                        }}
                        className="flex items-center px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground hover:bg-secondary rounded-lg transition-colors"
                      >
                        <Edit3 className="w-4 h-4 mr-1" />
                        编辑
                      </button>
                    </div>
                  </div>
                  <div className="grid grid-cols-3 gap-4 text-sm">
                    <div>
                      <span className="text-muted-foreground">内存: </span>
                      <span className="text-foreground">{config.memory_mb || fn?.memory_mb} MB</span>
                    </div>
                    <div>
                      <span className="text-muted-foreground">超时: </span>
                      <span className="text-foreground">{config.timeout_sec || fn?.timeout_sec} 秒</span>
                    </div>
                    <div>
                      <span className="text-muted-foreground">环境变量: </span>
                      <span className="text-foreground">{Object.keys(config.env_vars || {}).length} 个</span>
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* 调试模态框 */}
      {showDebugModal && fn && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-background/80 backdrop-blur-sm">
          <div className="bg-card border border-border rounded-2xl shadow-xl w-full max-w-2xl overflow-hidden">
            <div className="px-6 py-4 border-b border-border flex items-center justify-between">
              <h3 className="text-lg font-semibold text-foreground flex items-center">
                <Terminal className="w-5 h-5 mr-2 text-accent" />
                本地调试指导
              </h3>
              <button
                onClick={() => setShowDebugModal(false)}
                className="p-1 hover:bg-secondary rounded-lg transition-colors"
              >
                <X className="w-5 h-5 text-muted-foreground" />
              </button>
            </div>

            <div className="p-6 space-y-6">
              <p className="text-sm text-muted-foreground">
                由于安全限制，浏览器无法直接调试代码。请按照以下步骤使用 Nimbus CLI 在本地进行调试。
              </p>

              <div className="space-y-4">
                <div className="flex items-start gap-4">
                  <div className="flex-shrink-0 w-6 h-6 rounded-full bg-accent/10 text-accent text-xs flex items-center justify-center font-bold">
                    1
                  </div>
                  <div className="flex-1">
                    <h4 className="text-sm font-medium text-foreground mb-1">下载代码</h4>
                    <button
                      onClick={() => {
                        const blob = new Blob([code], { type: 'text/plain' })
                        const url = URL.createObjectURL(blob)
                        const a = document.createElement('a')
                        a.href = url
                        a.download = getDebugConfig(fn).fileName
                        a.click()
                      }}
                      className="inline-flex items-center text-xs text-accent hover:underline"
                    >
                      <Download className="w-3 h-3 mr-1" />
                      点击下载 {getDebugConfig(fn).fileName}
                    </button>
                  </div>
                </div>

                <div className="flex items-start gap-4">
                  <div className="flex-shrink-0 w-6 h-6 rounded-full bg-accent/10 text-accent text-xs flex items-center justify-center font-bold">
                    2
                  </div>
                  <div className="flex-1">
                    <h4 className="text-sm font-medium text-foreground mb-1">
                      运行调试命令
                    </h4>
                    <div className="relative group">
                      <pre className="p-3 bg-secondary rounded-lg text-xs font-mono text-foreground overflow-x-auto">
                        {getDebugConfig(fn).cmd}
                      </pre>
                      <button
                        onClick={() =>
                          navigator.clipboard.writeText(getDebugConfig(fn).cmd)
                        }
                        className="absolute top-2 right-2 p-1.5 bg-background border border-border rounded opacity-0 group-hover:opacity-100 transition-opacity"
                      >
                        <Copy className="w-3 h-3 text-muted-foreground" />
                      </button>
                    </div>
                  </div>
                </div>

                <div className="flex items-start gap-4">
                  <div className="flex-shrink-0 w-6 h-6 rounded-full bg-accent/10 text-accent text-xs flex items-center justify-center font-bold">
                    3
                  </div>
                  <div className="flex-1">
                    <h4 className="text-sm font-medium text-foreground mb-1">
                      配置 IDE (VS Code)
                    </h4>
                    <p className="text-xs text-muted-foreground mb-2">
                      在 .vscode/launch.json 中添加以下配置并按 F5 开始调试：
                    </p>
                    <div className="relative group">
                      <pre className="p-3 bg-secondary rounded-lg text-xs font-mono text-foreground overflow-x-auto max-h-40 overflow-y-auto">
                        {JSON.stringify(getDebugConfig(fn).vscode, null, 2)}
                      </pre>
                      <button
                        onClick={() =>
                          navigator.clipboard.writeText(
                            JSON.stringify(getDebugConfig(fn).vscode, null, 2)
                          )
                        }
                        className="absolute top-2 right-2 p-1.5 bg-background border border-border rounded opacity-0 group-hover:opacity-100 transition-opacity"
                      >
                        <Copy className="w-3 h-3 text-muted-foreground" />
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            </div>

            <div className="px-6 py-4 bg-secondary/30 text-right">
              <button
                onClick={() => setShowDebugModal(false)}
                className="px-4 py-2 bg-foreground text-background rounded-lg hover:opacity-90 transition-opacity text-sm font-medium"
              >
                我知道了
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 别名模态框 */}
      {showAliasModal && (
        <AliasModal
          functionId={id || ''}
          alias={editingAlias}
          versions={versions}
          onClose={() => {
            setShowAliasModal(false)
            setEditingAlias(null)
          }}
          onSave={async () => {
            setShowAliasModal(false)
            setEditingAlias(null)
            await loadAliases()
          }}
        />
      )}

      {/* 版本对比模态框 */}
      {showVersionDiff && fn && versions.length >= 2 && (
        <VersionDiffModal
          functionId={id || ''}
          versions={versions}
          runtime={fn.runtime}
          onClose={() => setShowVersionDiff(false)}
        />
      )}

      {/* 层绑定模态框 */}
      {showLayerBindingModal && fn && (
        <LayerBindingModal
          functionId={id || ''}
          functionRuntime={fn.runtime}
          currentLayers={functionLayers}
          onClose={() => setShowLayerBindingModal(false)}
          onSave={async () => {
            setShowLayerBindingModal(false)
            await loadFunctionLayers()
          }}
        />
      )}

      {/* 环境配置编辑模态框 */}
      {showEnvConfigModal && editingEnvConfig && fn && (
        <EnvConfigModal
          functionId={id || ''}
          config={editingEnvConfig}
          aliases={aliases}
          fn={fn}
          onClose={() => {
            setShowEnvConfigModal(false)
            setEditingEnvConfig(null)
          }}
          onSave={async () => {
            setShowEnvConfigModal(false)
            setEditingEnvConfig(null)
            await loadEnvConfigs()
          }}
        />
      )}
    </div>
  )
}
