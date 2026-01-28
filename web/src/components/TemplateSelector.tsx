import { useState, useEffect } from 'react'
import { Globe, BarChart3, Clock, Link, Rocket, Check, Loader2, Star } from 'lucide-react'
import { templateService } from '../services'
import type {
  Template,
  TemplateCategory,
} from '../types'
import { RUNTIME_LABELS } from '../types'
import { cn } from '../utils'

// 分类图标映射
const CategoryIcon = ({ category }: { category: TemplateCategory }) => {
  const iconClass = 'w-5 h-5'
  switch (category) {
    case 'web-api':
      return <Globe className={iconClass} />
    case 'data-processing':
      return <BarChart3 className={iconClass} />
    case 'scheduled':
      return <Clock className={iconClass} />
    case 'webhook':
      return <Link className={iconClass} />
    case 'starter':
      return <Rocket className={iconClass} />
    default:
      return <Rocket className={iconClass} />
  }
}

// 分类标签
const categoryLabels: Record<TemplateCategory, string> = {
  'web-api': 'Web API',
  'data-processing': '数据处理',
  'scheduled': '定时任务',
  'webhook': 'Webhook',
  'starter': '入门示例',
}

// 分类颜色
const categoryColors: Record<TemplateCategory, string> = {
  'web-api': 'text-blue-500 bg-blue-500/10 border-blue-500/30 hover:border-blue-500/50',
  'data-processing': 'text-purple-500 bg-purple-500/10 border-purple-500/30 hover:border-purple-500/50',
  'scheduled': 'text-amber-500 bg-amber-500/10 border-amber-500/30 hover:border-amber-500/50',
  'webhook': 'text-green-500 bg-green-500/10 border-green-500/30 hover:border-green-500/50',
  'starter': 'text-cyan-500 bg-cyan-500/10 border-cyan-500/30 hover:border-cyan-500/50',
}

interface TemplateSelectorProps {
  onSelect: (template: Template) => void
  selectedTemplate?: Template | null
}

export default function TemplateSelector({ onSelect, selectedTemplate }: TemplateSelectorProps) {
  const [templates, setTemplates] = useState<Template[]>([])
  const [loading, setLoading] = useState(true)
  const [selectedCategory, setSelectedCategory] = useState<TemplateCategory | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    loadTemplates()
  }, [])

  const loadTemplates = async () => {
    try {
      setLoading(true)
      setError(null)
      const response = await templateService.list({ limit: 100 })
      setTemplates(response.templates || [])
    } catch (err) {
      console.error('Failed to load templates:', err)
      setError('加载模板失败，请稍后重试')
    } finally {
      setLoading(false)
    }
  }

  // 获取所有分类
  const categories = Array.from(new Set(templates.map((t) => t.category))) as TemplateCategory[]

  // 按分类过滤模板
  const filteredTemplates = selectedCategory
    ? templates.filter((t) => t.category === selectedCategory)
    : templates

  // 获取热门模板
  const popularTemplates = templates.filter((t) => t.popular)

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="w-8 h-8 animate-spin text-muted-foreground" />
      </div>
    )
  }

  if (error) {
    return (
      <div className="text-center py-12">
        <p className="text-destructive mb-4">{error}</p>
        <button
          onClick={loadTemplates}
          className="px-4 py-2 bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors"
        >
          重试
        </button>
      </div>
    )
  }

  if (templates.length === 0) {
    return (
      <div className="text-center py-12">
        <p className="text-muted-foreground">暂无可用模板</p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* 分类选择 */}
      <div>
        <h3 className="text-sm font-medium text-muted-foreground mb-3">选择分类</h3>
        <div className="flex flex-wrap gap-2">
          <button
            onClick={() => setSelectedCategory(null)}
            className={cn(
              'flex items-center gap-2 px-4 py-2 rounded-lg border transition-all',
              selectedCategory === null
                ? 'border-accent bg-accent/10 text-accent'
                : 'border-border hover:border-accent/50 text-muted-foreground hover:text-foreground'
            )}
          >
            <Star className="w-4 h-4" />
            全部
          </button>
          {categories.map((category) => (
            <button
              key={category}
              onClick={() => setSelectedCategory(category)}
              className={cn(
                'flex items-center gap-2 px-4 py-2 rounded-lg border transition-all',
                selectedCategory === category
                  ? categoryColors[category]
                  : 'border-border hover:border-accent/50 text-muted-foreground hover:text-foreground'
              )}
            >
              <CategoryIcon category={category} />
              {categoryLabels[category]}
            </button>
          ))}
        </div>
      </div>

      {/* 热门模板 (仅在"全部"分类下显示) */}
      {!selectedCategory && popularTemplates.length > 0 && (
        <div>
          <h3 className="text-sm font-medium text-muted-foreground mb-3 flex items-center gap-2">
            <Star className="w-4 h-4 text-amber-500" />
            热门模板
          </h3>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            {popularTemplates.map((template) => (
              <TemplateCard
                key={template.id}
                template={template}
                isSelected={selectedTemplate?.id === template.id}
                onSelect={() => onSelect(template)}
              />
            ))}
          </div>
        </div>
      )}

      {/* 模板列表 */}
      <div>
        <h3 className="text-sm font-medium text-muted-foreground mb-3">
          {selectedCategory ? categoryLabels[selectedCategory] : '所有模板'}
          <span className="ml-2 text-xs">({filteredTemplates.length})</span>
        </h3>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          {filteredTemplates.map((template) => (
            <TemplateCard
              key={template.id}
              template={template}
              isSelected={selectedTemplate?.id === template.id}
              onSelect={() => onSelect(template)}
            />
          ))}
        </div>
      </div>
    </div>
  )
}

// 模板卡片组件
interface TemplateCardProps {
  template: Template
  isSelected: boolean
  onSelect: () => void
}

function TemplateCard({ template, isSelected, onSelect }: TemplateCardProps) {
  return (
    <button
      onClick={onSelect}
      className={cn(
        'relative flex flex-col items-start p-4 rounded-lg border text-left transition-all',
        isSelected
          ? 'border-accent bg-accent/10 ring-2 ring-accent/30'
          : 'border-border hover:border-accent/50 bg-card hover:bg-secondary/50'
      )}
    >
      {/* 选中指示器 */}
      {isSelected && (
        <div className="absolute top-3 right-3 w-5 h-5 rounded-full bg-accent flex items-center justify-center">
          <Check className="w-3 h-3 text-accent-foreground" />
        </div>
      )}

      {/* 热门标记 */}
      {template.popular && (
        <div className="absolute top-3 right-3">
          <Star className="w-4 h-4 text-amber-500 fill-amber-500" />
        </div>
      )}

      {/* 模板图标和名称 */}
      <div className="flex items-center gap-2 mb-2">
        <div className={cn('p-1.5 rounded', categoryColors[template.category])}>
          <CategoryIcon category={template.category} />
        </div>
        <div>
          <h4 className="font-medium text-foreground">{template.display_name}</h4>
          <p className="text-xs text-muted-foreground">{RUNTIME_LABELS[template.runtime]}</p>
        </div>
      </div>

      {/* 描述 */}
      {template.description && (
        <p className="text-sm text-muted-foreground line-clamp-2 mb-2">{template.description}</p>
      )}

      {/* 标签 */}
      {template.tags && template.tags.length > 0 && (
        <div className="flex flex-wrap gap-1 mt-auto">
          {template.tags.slice(0, 3).map((tag) => (
            <span
              key={tag}
              className="px-2 py-0.5 text-xs rounded bg-secondary text-muted-foreground"
            >
              {tag}
            </span>
          ))}
        </div>
      )}
    </button>
  )
}
