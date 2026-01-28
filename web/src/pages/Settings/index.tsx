import { useState, useEffect } from 'react'
import { Key, Copy, Plus, Trash2, Check, RefreshCw, AlertCircle, Save, Settings as SettingsIcon } from 'lucide-react'
import { copyToClipboard, cn } from '../../utils'
import { apiKeyService, type ApiKey } from '../../services/apikeys'
import {
  settingsService,
  settingGroups,
  defaultSettings,
} from '../../services/settings'
import { useToast } from '../../components/Toast'

export default function Settings() {
  const toast = useToast()
  const [activeTab, setActiveTab] = useState<'general' | 'apikeys'>('general')

  // System settings state
  const [settings, setSettings] = useState<Record<string, string>>({})
  const [originalSettings, setOriginalSettings] = useState<Record<string, string>>({})
  const [loadingSettings, setLoadingSettings] = useState(true)
  const [savingSettings, setSavingSettings] = useState(false)
  const [activeGroup, setActiveGroup] = useState<string>('general')

  // API Keys state
  const [apiKeys, setApiKeys] = useState<ApiKey[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showCreateModal, setShowCreateModal] = useState(false)
  const [newKeyName, setNewKeyName] = useState('')
  const [creating, setCreating] = useState(false)
  const [copiedId, setCopiedId] = useState<string | null>(null)
  const [newlyCreatedKey, setNewlyCreatedKey] = useState<string | null>(null)

  // Load settings on mount
  useEffect(() => {
    loadSettings()
  }, [])

  // Load API keys when tab changes to apikeys
  useEffect(() => {
    if (activeTab === 'apikeys') {
      loadApiKeys()
    }
  }, [activeTab])

  const loadSettings = async () => {
    try {
      setLoadingSettings(true)
      const settingsList = await settingsService.list()
      const settingsMap: Record<string, string> = {}

      // Initialize with defaults
      for (const [key, value] of Object.entries(defaultSettings)) {
        settingsMap[key] = value
      }

      // Override with actual values from API
      for (const setting of settingsList) {
        settingsMap[setting.key] = setting.value
      }

      setSettings(settingsMap)
      setOriginalSettings({ ...settingsMap })
    } catch (err) {
      console.error('Failed to load settings:', err)
      // Use defaults on error
      setSettings({ ...defaultSettings })
      setOriginalSettings({ ...defaultSettings })
    } finally {
      setLoadingSettings(false)
    }
  }

  const handleSettingChange = (key: string, value: string) => {
    setSettings((prev) => ({ ...prev, [key]: value }))
  }

  const hasChanges = () => {
    return Object.keys(settings).some((key) => settings[key] !== originalSettings[key])
  }

  const getChangedSettings = (): string[] => {
    return Object.keys(settings).filter((key) => settings[key] !== originalSettings[key])
  }

  const handleSaveSettings = async () => {
    const changedKeys = getChangedSettings()
    if (changedKeys.length === 0) return

    setSavingSettings(true)
    let successCount = 0

    try {
      for (const key of changedKeys) {
        try {
          await settingsService.update(key, settings[key])
          successCount++
        } catch (err) {
          console.error(`Failed to save setting ${key}:`, err)
        }
      }

      if (successCount === changedKeys.length) {
        toast.success('设置已保存')
        setOriginalSettings({ ...settings })
      } else if (successCount > 0) {
        toast.warning(`已保存 ${successCount}/${changedKeys.length} 项设置`)
        await loadSettings() // Reload to get actual values
      } else {
        toast.error('保存设置失败')
      }
    } finally {
      setSavingSettings(false)
    }
  }

  const loadApiKeys = async () => {
    try {
      setLoading(true)
      setError(null)
      const keys = await apiKeyService.list()
      setApiKeys(keys)
    } catch (err) {
      console.error('Failed to load API keys:', err)
      setError('加载 API Key 列表失败')
    } finally {
      setLoading(false)
    }
  }

  const handleCopyKey = async (key: string, id: string) => {
    const success = await copyToClipboard(key)
    if (success) {
      setCopiedId(id)
      setTimeout(() => setCopiedId(null), 2000)
    }
  }

  const handleCreateKey = async () => {
    if (!newKeyName.trim()) return
    try {
      setCreating(true)
      const result = await apiKeyService.create(newKeyName)
      setNewlyCreatedKey(result.api_key)
      await loadApiKeys()
      setNewKeyName('')
    } catch (err) {
      console.error('Failed to create API key:', err)
      toast.error('创建 API Key 失败')
    } finally {
      setCreating(false)
    }
  }

  const handleDeleteKey = async (id: string) => {
    if (!confirm('确定要删除这个 API Key 吗？删除后无法恢复。')) return
    try {
      await apiKeyService.delete(id)
      setApiKeys(apiKeys.filter((k) => k.id !== id))
      toast.success('API Key 已删除')
    } catch (err) {
      console.error('Failed to delete API key:', err)
      toast.error('删除 API Key 失败')
    }
  }

  const handleCloseCreateModal = () => {
    setShowCreateModal(false)
    setNewKeyName('')
    setNewlyCreatedKey(null)
  }

  // Render setting input based on type
  const renderSettingInput = (setting: { key: string; type: string; options?: { value: string; label: string }[]; unit?: string; readonly?: boolean }) => {
    const value = settings[setting.key] || ''
    const isChanged = settings[setting.key] !== originalSettings[setting.key]

    if (setting.readonly) {
      return (
        <input
          type="text"
          value={value}
          readOnly
          className="w-full max-w-xs px-4 py-2 bg-secondary border border-border rounded-lg text-muted-foreground"
        />
      )
    }

    if (setting.type === 'select' && setting.options) {
      return (
        <div className="flex items-center gap-2">
          <select
            value={value}
            onChange={(e) => handleSettingChange(setting.key, e.target.value)}
            className={cn(
              'w-full max-w-xs px-4 py-2 bg-input border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring transition-all',
              isChanged ? 'border-accent' : 'border-border'
            )}
          >
            {setting.options.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
          {isChanged && <span className="text-xs text-accent">已修改</span>}
        </div>
      )
    }

    if (setting.type === 'number') {
      return (
        <div className="flex items-center gap-2">
          <input
            type="number"
            value={value}
            onChange={(e) => handleSettingChange(setting.key, e.target.value)}
            className={cn(
              'w-32 px-4 py-2 bg-input border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring transition-all',
              isChanged ? 'border-accent' : 'border-border'
            )}
          />
          {setting.unit && <span className="text-sm text-muted-foreground">{setting.unit}</span>}
          {isChanged && <span className="text-xs text-accent">已修改</span>}
        </div>
      )
    }

    return (
      <div className="flex items-center gap-2">
        <input
          type="text"
          value={value}
          onChange={(e) => handleSettingChange(setting.key, e.target.value)}
          className={cn(
            'w-full max-w-md px-4 py-2 bg-input border rounded-lg text-foreground focus:outline-none focus:ring-2 focus:ring-ring transition-all',
            isChanged ? 'border-accent' : 'border-border'
          )}
        />
        {isChanged && <span className="text-xs text-accent">已修改</span>}
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-display font-bold text-foreground">设置</h1>
          <p className="text-muted-foreground mt-1">管理系统配置和 API 密钥</p>
        </div>
        {activeTab === 'general' && hasChanges() && (
          <button
            onClick={handleSaveSettings}
            disabled={savingSettings}
            className={cn(
              'inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg transition-colors',
              'bg-accent text-accent-foreground hover:bg-accent/90',
              savingSettings && 'opacity-50 cursor-not-allowed'
            )}
          >
            <Save className={cn('w-4 h-4', savingSettings && 'animate-pulse')} />
            {savingSettings ? '保存中...' : '保存更改'}
          </button>
        )}
      </div>

      {/* Tabs */}
      <div className="border-b border-border">
        <nav className="flex space-x-8">
          <button
            onClick={() => setActiveTab('general')}
            className={cn(
              'py-4 px-1 border-b-2 font-medium text-sm transition-colors',
              activeTab === 'general'
                ? 'border-accent text-accent'
                : 'border-transparent text-muted-foreground hover:text-foreground hover:border-border'
            )}
          >
            系统设置
          </button>
          <button
            onClick={() => setActiveTab('apikeys')}
            className={cn(
              'py-4 px-1 border-b-2 font-medium text-sm transition-colors',
              activeTab === 'apikeys'
                ? 'border-accent text-accent'
                : 'border-transparent text-muted-foreground hover:text-foreground hover:border-border'
            )}
          >
            API Keys
          </button>
        </nav>
      </div>

      {/* System Settings */}
      {activeTab === 'general' && (
        <div className="flex gap-6">
          {/* Settings Groups Sidebar */}
          <div className="w-48 flex-shrink-0">
            <nav className="space-y-1">
              {settingGroups.map((group) => (
                <button
                  key={group.id}
                  onClick={() => setActiveGroup(group.id)}
                  className={cn(
                    'w-full text-left px-3 py-2 text-sm rounded-lg transition-colors',
                    activeGroup === group.id
                      ? 'bg-accent text-accent-foreground font-medium'
                      : 'text-muted-foreground hover:text-foreground hover:bg-muted'
                  )}
                >
                  {group.label}
                </button>
              ))}
            </nav>
          </div>

          {/* Settings Content */}
          <div className="flex-1">
            {loadingSettings ? (
              <div className="bg-card rounded-xl border border-border p-6">
                <div className="flex items-center justify-center py-8">
                  <RefreshCw className="w-6 h-6 text-accent animate-spin" />
                </div>
              </div>
            ) : (
              settingGroups
                .filter((group) => group.id === activeGroup)
                .map((group) => (
                  <div key={group.id} className="bg-card rounded-xl border border-border p-6">
                    <div className="flex items-center gap-3 mb-6">
                      <div className="p-2 bg-accent/10 rounded-lg">
                        <SettingsIcon className="w-5 h-5 text-accent" />
                      </div>
                      <div>
                        <h2 className="text-lg font-semibold text-foreground">{group.label}</h2>
                        <p className="text-sm text-muted-foreground">{group.description}</p>
                      </div>
                    </div>
                    <div className="space-y-6">
                      {group.settings.map((setting) => (
                        <div key={setting.key}>
                          <label className="block text-sm font-medium text-foreground mb-1">
                            {setting.label}
                          </label>
                          <p className="text-xs text-muted-foreground mb-2">{setting.description}</p>
                          {renderSettingInput(setting)}
                        </div>
                      ))}
                    </div>
                  </div>
                ))
            )}
          </div>
        </div>
      )}

      {/* API Keys */}
      {activeTab === 'apikeys' && (
        <div className="space-y-6">
          {/* Error */}
          {error && (
            <div className="bg-destructive/10 border border-destructive/30 rounded-xl p-4 flex items-center text-destructive">
              <AlertCircle className="w-5 h-5 mr-2" />
              <span className="text-sm">{error}</span>
              <button
                onClick={loadApiKeys}
                className="ml-auto text-sm text-destructive hover:text-destructive/80 underline transition-colors"
              >
                重试
              </button>
            </div>
          )}

          <div className="bg-card rounded-xl border border-border p-6">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-lg font-semibold text-foreground">API Keys</h2>
              <div className="flex items-center gap-2">
                <button
                  onClick={loadApiKeys}
                  disabled={loading}
                  className="p-2 text-muted-foreground hover:text-foreground hover:bg-secondary rounded-lg transition-colors"
                >
                  <RefreshCw className={cn('w-4 h-4', loading && 'animate-spin')} />
                </button>
                <button
                  onClick={() => setShowCreateModal(true)}
                  className="flex items-center px-4 py-2 bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors"
                >
                  <Plus className="w-4 h-4 mr-2" />
                  创建 Key
                </button>
              </div>
            </div>

            {loading ? (
              <div className="flex items-center justify-center py-8">
                <RefreshCw className="w-6 h-6 text-accent animate-spin" />
              </div>
            ) : apiKeys.length === 0 ? (
              <div className="text-center py-8 text-muted-foreground">
                <Key className="w-12 h-12 mx-auto mb-2 text-muted-foreground/30" />
                <p>暂无 API Key</p>
                <p className="text-sm mt-1">点击"创建 Key"按钮创建一个新的 API Key</p>
              </div>
            ) : (
              <div className="space-y-4">
                {apiKeys.map((key) => (
                  <div
                    key={key.id}
                    className="flex items-center justify-between p-4 border border-border rounded-lg bg-secondary/30"
                  >
                    <div className="flex items-center">
                      <Key className="w-5 h-5 text-muted-foreground mr-3" />
                      <div>
                        <p className="font-medium text-foreground">{key.name}</p>
                        <p className="text-sm text-muted-foreground">
                          创建于 {new Date(key.created_at).toLocaleString()}
                        </p>
                      </div>
                    </div>
                    <div className="flex items-center space-x-2">
                      <button
                        onClick={() => handleDeleteKey(key.id)}
                        className="p-2 text-destructive hover:text-destructive/80 hover:bg-destructive/10 rounded-lg transition-colors"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Create Key Modal */}
          {showCreateModal && (
            <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
              <div className="bg-card rounded-xl border border-border shadow-xl p-6 w-full max-w-md">
                {newlyCreatedKey ? (
                  <>
                    <h3 className="text-lg font-semibold text-foreground mb-4">API Key 已创建</h3>
                    <div className="mb-4">
                      <p className="text-sm text-muted-foreground mb-2">
                        请保存您的 API Key，它只会显示一次：
                      </p>
                      <div className="flex items-center bg-secondary rounded-lg p-3">
                        <code className="flex-1 text-sm font-mono text-foreground break-all">
                          {newlyCreatedKey}
                        </code>
                        <button
                          onClick={() => handleCopyKey(newlyCreatedKey, 'new')}
                          className="ml-2 p-2 text-muted-foreground hover:text-foreground hover:bg-card rounded-lg flex-shrink-0 transition-colors"
                        >
                          {copiedId === 'new' ? (
                            <Check className="w-4 h-4 text-green-400" />
                          ) : (
                            <Copy className="w-4 h-4" />
                          )}
                        </button>
                      </div>
                      <p className="text-xs text-orange-400 mt-2">
                        警告：关闭此对话框后将无法再次查看完整的 API Key
                      </p>
                    </div>
                    <div className="flex justify-end">
                      <button
                        onClick={handleCloseCreateModal}
                        className="px-4 py-2 bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors"
                      >
                        完成
                      </button>
                    </div>
                  </>
                ) : (
                  <>
                    <h3 className="text-lg font-semibold text-foreground mb-4">创建 API Key</h3>
                    <div className="mb-4">
                      <label className="block text-sm font-medium text-foreground mb-1">名称</label>
                      <input
                        type="text"
                        value={newKeyName}
                        onChange={(e) => setNewKeyName(e.target.value)}
                        placeholder="例如: Production"
                        className="w-full px-4 py-2 bg-input border border-border rounded-lg text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring transition-all"
                        disabled={creating}
                      />
                    </div>
                    <div className="flex justify-end space-x-3">
                      <button
                        onClick={handleCloseCreateModal}
                        className="px-4 py-2 border border-border rounded-lg text-foreground hover:bg-secondary transition-colors"
                        disabled={creating}
                      >
                        取消
                      </button>
                      <button
                        onClick={handleCreateKey}
                        disabled={creating || !newKeyName.trim()}
                        className="px-4 py-2 bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 disabled:opacity-50 transition-colors"
                      >
                        {creating ? '创建中...' : '创建'}
                      </button>
                    </div>
                  </>
                )}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
