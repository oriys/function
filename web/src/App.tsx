import { Routes, Route, Navigate } from 'react-router-dom'
import { lazy, Suspense } from 'react'
import Layout from './components/Layout/Layout'
import { Skeleton } from './components/Skeleton'

// Eager load lightweight pages
import Dashboard from './pages/Dashboard'
import FunctionList from './pages/Functions/List'
import FunctionCreate from './pages/Functions/Create'
import InvocationList from './pages/Invocations/List'
import InvocationDetail from './pages/Invocations/Detail'
import DLQ from './pages/DLQ'
import Logs from './pages/Logs'
import Settings from './pages/Settings'
import Layers from './pages/Layers'
import Environments from './pages/Environments'
import AuditLogs from './pages/AuditLogs'
import Quota from './pages/Quota'
import Alerts from './pages/Alerts'
import Dependencies from './pages/Dependencies'
import Warming from './pages/Warming'

// Lazy load heavy pages (Monaco Editor, ECharts, ReactFlow)
const FunctionDetail = lazy(() => import('./pages/Functions/Detail'))
const FunctionWorkbench = lazy(() => import('./pages/Functions/Workbench'))
const Metrics = lazy(() => import('./pages/Metrics'))

// Lazy load workflow pages (ReactFlow)
const WorkflowList = lazy(() => import('./pages/Workflows/List'))
const WorkflowDetail = lazy(() => import('./pages/Workflows/Detail'))
const WorkflowEditor = lazy(() => import('./pages/Workflows/Editor'))
const ExecutionDetail = lazy(() => import('./pages/Workflows/ExecutionDetail'))

// Loading fallback for lazy loaded pages
function PageLoading() {
  return (
    <div className="space-y-6 p-6">
      <Skeleton className="h-8 w-64" />
      <Skeleton className="h-32" />
      <Skeleton className="h-64" />
    </div>
  )
}

function App() {
  return (
    <Routes>
      <Route path="/" element={<Layout />}>
        <Route index element={<Navigate to="/dashboard" replace />} />
        <Route path="dashboard" element={<Dashboard />} />
        <Route path="functions" element={<FunctionList />} />
        <Route path="functions/create" element={<FunctionCreate />} />
        <Route path="functions/:id" element={
          <Suspense fallback={<PageLoading />}>
            <FunctionDetail />
          </Suspense>
        } />
        <Route path="functions/:id/workbench" element={
          <Suspense fallback={<PageLoading />}>
            <FunctionWorkbench />
          </Suspense>
        } />
        <Route path="invocations" element={<InvocationList />} />
        <Route path="invocations/:id" element={<InvocationDetail />} />
        <Route path="workflows" element={
          <Suspense fallback={<PageLoading />}>
            <WorkflowList />
          </Suspense>
        } />
        <Route path="workflows/create" element={
          <Suspense fallback={<PageLoading />}>
            <WorkflowEditor />
          </Suspense>
        } />
        <Route path="workflows/:id" element={
          <Suspense fallback={<PageLoading />}>
            <WorkflowDetail />
          </Suspense>
        } />
        <Route path="workflows/:id/edit" element={
          <Suspense fallback={<PageLoading />}>
            <WorkflowEditor />
          </Suspense>
        } />
        <Route path="workflows/:id/executions/:executionId" element={
          <Suspense fallback={<PageLoading />}>
            <ExecutionDetail />
          </Suspense>
        } />
        <Route path="layers" element={<Layers />} />
        <Route path="environments" element={<Environments />} />
        <Route path="dlq" element={<DLQ />} />
        <Route path="logs" element={<Logs />} />
        <Route path="metrics" element={
          <Suspense fallback={<PageLoading />}>
            <Metrics />
          </Suspense>
        } />
        <Route path="settings" element={<Settings />} />
        <Route path="audit" element={<AuditLogs />} />
        <Route path="quota" element={<Quota />} />
        <Route path="alerts" element={<Alerts />} />
        <Route path="dependencies" element={<Dependencies />} />
        <Route path="warming" element={<Warming />} />
      </Route>
    </Routes>
  )
}

export default App
