import { Routes, Route, Navigate } from 'react-router-dom'
import { lazy, Suspense } from 'react'
import Layout from './components/Layout/Layout'
import { Skeleton } from './components/Skeleton'

// Lazy load all pages for better code splitting
const Dashboard = lazy(() => import('./pages/Dashboard'))
const FunctionList = lazy(() => import('./pages/Functions/List'))
const FunctionCreate = lazy(() => import('./pages/Functions/Create'))
const FunctionDetail = lazy(() => import('./pages/Functions/Detail'))
const FunctionWorkbench = lazy(() => import('./pages/Functions/Workbench'))
const InvocationList = lazy(() => import('./pages/Invocations/List'))
const InvocationDetail = lazy(() => import('./pages/Invocations/Detail'))
const WorkflowList = lazy(() => import('./pages/Workflows/List'))
const WorkflowDetail = lazy(() => import('./pages/Workflows/Detail'))
const WorkflowEditor = lazy(() => import('./pages/Workflows/Editor'))
const ExecutionDetail = lazy(() => import('./pages/Workflows/ExecutionDetail'))
const Layers = lazy(() => import('./pages/Layers'))
const Environments = lazy(() => import('./pages/Environments'))
const DLQ = lazy(() => import('./pages/DLQ'))
const Logs = lazy(() => import('./pages/Logs'))
const Metrics = lazy(() => import('./pages/Metrics'))
const Settings = lazy(() => import('./pages/Settings'))
const AuditLogs = lazy(() => import('./pages/AuditLogs'))
const Quota = lazy(() => import('./pages/Quota'))
const Alerts = lazy(() => import('./pages/Alerts'))
const Dependencies = lazy(() => import('./pages/Dependencies'))
const Warming = lazy(() => import('./pages/Warming'))

// Loading fallback for lazy loaded pages
function PageLoading() {
  return (
    <div className="space-y-6 p-6">
      <div className="flex items-center space-x-4">
        <Skeleton className="h-8 w-64" />
      </div>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        <Skeleton className="h-32" />
        <Skeleton className="h-32" />
        <Skeleton className="h-32" />
      </div>
      <Skeleton className="h-96 w-full" />
    </div>
  )
}

function App() {
  return (
    <Suspense fallback={<PageLoading />}>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<Navigate to="/dashboard" replace />} />
          <Route path="dashboard" element={<Dashboard />} />
          <Route path="functions" element={<FunctionList />} />
          <Route path="functions/create" element={<FunctionCreate />} />
          <Route path="functions/:id" element={<FunctionDetail />} />
          <Route path="functions/:id/workbench" element={<FunctionWorkbench />} />
          <Route path="invocations" element={<InvocationList />} />
          <Route path="invocations/:id" element={<InvocationDetail />} />
          <Route path="workflows" element={<WorkflowList />} />
          <Route path="workflows/create" element={<WorkflowEditor />} />
          <Route path="workflows/:id" element={<WorkflowDetail />} />
          <Route path="workflows/:id/edit" element={<WorkflowEditor />} />
          <Route path="workflows/:id/executions/:executionId" element={<ExecutionDetail />} />
          <Route path="layers" element={<Layers />} />
          <Route path="environments" element={<Environments />} />
          <Route path="dlq" element={<DLQ />} />
          <Route path="logs" element={<Logs />} />
          <Route path="metrics" element={<Metrics />} />
          <Route path="settings" element={<Settings />} />
          <Route path="audit" element={<AuditLogs />} />
          <Route path="quota" element={<Quota />} />
          <Route path="alerts" element={<Alerts />} />
          <Route path="dependencies" element={<Dependencies />} />
          <Route path="warming" element={<Warming />} />
        </Route>
      </Routes>
    </Suspense>
  )
}

export default App