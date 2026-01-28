import { useState, useEffect } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Plus, Play, Edit2, Trash2, GitBranch, Clock } from 'lucide-react'
import { workflowService } from '../../services/workflows'
import type { Workflow } from '../../types/workflow'
import { WORKFLOW_STATUS_COLORS, WORKFLOW_STATUS_LABELS } from '../../types/workflow'
import { cn } from '../../utils/format'
import Pagination from '../../components/Pagination'
import EmptyState from '../../components/EmptyState'
import { Skeleton } from '../../components/Skeleton'
import { useToast } from '../../components/Toast'
import ExecutionInputDialog from '../../components/ExecutionInputDialog'

export default function WorkflowList() {
  const navigate = useNavigate()
  const toast = useToast()
  const [workflows, setWorkflows] = useState<Workflow[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [page, setPage] = useState(1)
  const limit = 10

  // 执行对话框状态
  const [selectedWorkflow, setSelectedWorkflow] = useState<Workflow | null>(null)
  const [showExecutionDialog, setShowExecutionDialog] = useState(false)

  const fetchWorkflows = async () => {
    setLoading(true)
    try {
      const response = await workflowService.list({
        offset: (page - 1) * limit,
        limit,
      })
      setWorkflows(response.workflows || [])
      setTotal(response.total)
    } catch (error) {
      console.error('Failed to fetch workflows:', error)
      toast.error('获取工作流列表失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchWorkflows()
  }, [page])

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`确定要删除工作流 "${name}" 吗？此操作不可撤销。`)) {
      return
    }

    try {
      await workflowService.delete(id)
      toast.success('工作流已删除')
      fetchWorkflows()
    } catch (error) {
      console.error('Failed to delete workflow:', error)
      toast.error('删除工作流失败')
    }
  }

  const handleOpenExecutionDialog = (workflow: Workflow) => {
    setSelectedWorkflow(workflow)
    setShowExecutionDialog(true)
  }

  const handleStartExecution = async (input: Record<string, unknown>) => {
    if (!selectedWorkflow) return

    try {
      const execution = await workflowService.startExecution(selectedWorkflow.id, { input })
      toast.success('执行已启动')
      setShowExecutionDialog(false)
      navigate(`/workflows/${selectedWorkflow.id}/executions/${execution.id}`)
    } catch (error) {
      console.error('Failed to start execution:', error)
      toast.error('启动执行失败')
    }
  }

  if (loading && workflows.length === 0) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <Skeleton className="h-8 w-32" />
          <Skeleton className="h-10 w-32" />
        </div>
        <div className="grid gap-4">
          {[...Array(3)].map((_, i) => (
            <Skeleton key={i} className="h-24" />
          ))}
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-display font-bold text-foreground">工作流</h1>
          <p className="text-sm text-muted-foreground mt-1">
            编排多个函数组成复杂业务流程
          </p>
        </div>
        <Link
          to="/workflows/create"
          className="inline-flex items-center gap-2 px-4 py-2 bg-accent text-accent-foreground rounded-lg hover:bg-accent/90 transition-colors"
        >
          <Plus className="w-4 h-4" />
          创建工作流
        </Link>
      </div>

      {/* Workflow List */}
      {workflows.length === 0 ? (
        <EmptyState
          type="general"
          title="暂无工作流"
          description="创建您的第一个工作流，将多个函数编排成业务流程"
          actionLabel="创建工作流"
          actionTo="/workflows/create"
        />
      ) : (
        <div className="space-y-4">
          {workflows.map((workflow) => (
            <div
              key={workflow.id}
              className="bg-card border border-border rounded-lg p-4 hover:shadow-md transition-shadow"
            >
              <div className="flex items-start justify-between">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-3">
                    <Link
                      to={`/workflows/${workflow.id}`}
                      className="text-lg font-semibold text-foreground hover:text-accent transition-colors truncate"
                    >
                      {workflow.name}
                    </Link>
                    <span
                      className={cn(
                        'px-2 py-0.5 text-xs font-medium rounded-full',
                        WORKFLOW_STATUS_COLORS[workflow.status]
                      )}
                    >
                      {WORKFLOW_STATUS_LABELS[workflow.status]}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      v{workflow.version}
                    </span>
                  </div>
                  {workflow.description && (
                    <p className="text-sm text-muted-foreground mt-1 line-clamp-2">
                      {workflow.description}
                    </p>
                  )}
                  <div className="flex items-center gap-4 mt-2 text-xs text-muted-foreground">
                    <span className="flex items-center gap-1">
                      <GitBranch className="w-3.5 h-3.5" />
                      {Object.keys(workflow.definition.states).length} 个状态
                    </span>
                    <span className="flex items-center gap-1">
                      <Clock className="w-3.5 h-3.5" />
                      超时 {workflow.timeout_sec}s
                    </span>
                    <span>
                      创建于 {new Date(workflow.created_at).toLocaleDateString()}
                    </span>
                  </div>
                </div>
                <div className="flex items-center gap-2 ml-4">
                  <button
                    onClick={() => handleOpenExecutionDialog(workflow)}
                    disabled={workflow.status !== 'active'}
                    className={cn(
                      'p-2 rounded-lg transition-colors',
                      workflow.status === 'active'
                        ? 'text-green-600 hover:bg-green-100 dark:hover:bg-green-900/30'
                        : 'text-muted-foreground cursor-not-allowed'
                    )}
                    title={workflow.status === 'active' ? '启动执行' : '工作流未激活'}
                  >
                    <Play className="w-4 h-4" />
                  </button>
                  <Link
                    to={`/workflows/${workflow.id}/edit`}
                    className="p-2 text-muted-foreground hover:text-foreground hover:bg-muted rounded-lg transition-colors"
                    title="编辑工作流"
                  >
                    <Edit2 className="w-4 h-4" />
                  </Link>
                  <button
                    onClick={() => handleDelete(workflow.id, workflow.name)}
                    className="p-2 text-muted-foreground hover:text-red-600 hover:bg-red-100 dark:hover:bg-red-900/30 rounded-lg transition-colors"
                    title="删除工作流"
                  >
                    <Trash2 className="w-4 h-4" />
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Pagination */}
      {total > limit && (
        <Pagination
          page={page}
          pageSize={limit}
          total={total}
          onChange={setPage}
        />
      )}

      {/* Execution Input Dialog */}
      <ExecutionInputDialog
        workflowName={selectedWorkflow?.name || ''}
        isOpen={showExecutionDialog}
        onConfirm={handleStartExecution}
        onClose={() => setShowExecutionDialog(false)}
      />
    </div>
  )
}
