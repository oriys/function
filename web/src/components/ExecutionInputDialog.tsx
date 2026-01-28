/**
 * 工作流执行参数输入对话框
 * 支持 JSON 格式的输入参数
 */

import { useState, useEffect, useRef } from 'react'
import { X, Play, AlertCircle } from 'lucide-react'
import { cn } from '../utils'

interface ExecutionInputDialogProps {
  workflowName: string
  isOpen: boolean
  onConfirm: (input: Record<string, unknown>) => void
  onClose: () => void
}

export function ExecutionInputDialog({
  workflowName,
  isOpen,
  onConfirm,
  onClose,
}: ExecutionInputDialogProps) {
  const [inputText, setInputText] = useState('{\n  \n}')
  const [error, setError] = useState<string | null>(null)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const dialogRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (isOpen) {
      setInputText('{\n  \n}')
      setError(null)
      setIsSubmitting(false)
      // 延迟聚焦并设置光标位置
      setTimeout(() => {
        if (textareaRef.current) {
          textareaRef.current.focus()
          textareaRef.current.setSelectionRange(4, 4)
        }
      }, 50)
    }
  }, [isOpen])

  // ESC 关闭
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) onClose()
    }
    document.addEventListener('keydown', handler)
    return () => document.removeEventListener('keydown', handler)
  }, [onClose, isOpen])

  // 点击背景关闭
  const handleBackdropClick = (e: React.MouseEvent) => {
    if (e.target === e.currentTarget) {
      onClose()
    }
  }

  const validateAndParse = (): Record<string, unknown> | null => {
    try {
      const trimmed = inputText.trim()
      if (!trimmed || trimmed === '{}') {
        return {}
      }
      const parsed = JSON.parse(trimmed)
      if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
        setError('输入必须是一个 JSON 对象')
        return null
      }
      setError(null)
      return parsed
    } catch (e) {
      setError('JSON 格式错误: ' + (e as Error).message)
      return null
    }
  }

  const handleSubmit = async () => {
    const parsed = validateAndParse()
    if (parsed === null) return

    setIsSubmitting(true)
    try {
      await onConfirm(parsed)
    } finally {
      setIsSubmitting(false)
    }
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    // Cmd/Ctrl + Enter 提交
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      e.preventDefault()
      handleSubmit()
    }
    // Tab 插入空格
    if (e.key === 'Tab') {
      e.preventDefault()
      const textarea = textareaRef.current
      if (textarea) {
        const start = textarea.selectionStart
        const end = textarea.selectionEnd
        const newValue = inputText.substring(0, start) + '  ' + inputText.substring(end)
        setInputText(newValue)
        setTimeout(() => {
          textarea.selectionStart = textarea.selectionEnd = start + 2
        }, 0)
      }
    }
  }

  const handleInputChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setInputText(e.target.value)
    setError(null)
  }

  if (!isOpen) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm"
      onClick={handleBackdropClick}
    >
      <div
        ref={dialogRef}
        className="w-[500px] max-h-[80vh] bg-card border border-border rounded-xl shadow-2xl overflow-hidden"
      >
        {/* 头部 */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border bg-muted/30">
          <div className="flex items-center gap-2">
            <Play className="w-4 h-4 text-accent" />
            <span className="font-medium text-foreground">启动执行</span>
            <span className="text-sm text-muted-foreground">- {workflowName}</span>
          </div>
          <button
            onClick={onClose}
            className="p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted rounded-lg transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* 内容 */}
        <div className="p-4 space-y-4">
          <div>
            <label className="block text-sm font-medium text-foreground mb-2">
              输入参数 (JSON)
            </label>
            <textarea
              ref={textareaRef}
              value={inputText}
              onChange={handleInputChange}
              onKeyDown={handleKeyDown}
              placeholder='{"key": "value"}'
              spellCheck={false}
              className={cn(
                'w-full h-48 bg-input border rounded-lg px-3 py-2 text-sm font-mono',
                'outline-none focus:ring-2 focus:ring-accent/50 transition-all resize-none',
                error ? 'border-red-500' : 'border-border'
              )}
            />
            {error && (
              <div className="flex items-center gap-1.5 mt-2 text-xs text-red-500">
                <AlertCircle className="w-3.5 h-3.5" />
                {error}
              </div>
            )}
            <p className="mt-2 text-xs text-muted-foreground">
              输入将作为工作流的初始数据传入。留空或 {'{ }'} 表示无输入。
              <br />
              <span className="text-muted-foreground/70">
                快捷键: Cmd/Ctrl + Enter 执行
              </span>
            </p>
          </div>

          {/* 示例 */}
          <div className="p-3 bg-muted/50 rounded-lg">
            <p className="text-xs font-medium text-muted-foreground mb-1.5">示例:</p>
            <pre className="text-xs font-mono text-muted-foreground">
{`{
  "name": "张三",
  "orderId": "ORD-001",
  "items": [{"name": "商品A", "qty": 2}]
}`}
            </pre>
          </div>
        </div>

        {/* 底部按钮 */}
        <div className="flex items-center justify-end gap-3 px-4 py-3 border-t border-border bg-muted/30">
          <button
            onClick={onClose}
            disabled={isSubmitting}
            className="px-4 py-2 text-sm text-muted-foreground hover:text-foreground transition-colors rounded-lg"
          >
            取消
          </button>
          <button
            onClick={handleSubmit}
            disabled={isSubmitting}
            className={cn(
              'inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg transition-colors',
              'bg-accent text-accent-foreground hover:bg-accent/90',
              isSubmitting && 'opacity-50 cursor-not-allowed'
            )}
          >
            <Play className="w-4 h-4" />
            {isSubmitting ? '启动中...' : '启动执行'}
          </button>
        </div>
      </div>
    </div>
  )
}

export default ExecutionInputDialog
