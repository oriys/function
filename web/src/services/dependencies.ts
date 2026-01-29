import api from './api'

export type DependencyType = 'direct_call' | 'workflow' | 'http'

export interface FunctionDependency {
  source_id: string
  source_name: string
  target_id: string
  target_name: string
  type: DependencyType
  call_count: number
  last_called_at?: string
}

export interface DependencyNode {
  id: string
  name: string
  type: string
  runtime?: string
  status: string
}

export interface DependencyEdge {
  source: string
  target: string
  type: DependencyType
  call_count: number
}

export interface DependencyGraph {
  nodes: DependencyNode[]
  edges: DependencyEdge[]
}

export interface ImpactAnalysis {
  function_id: string
  function_name: string
  direct_dependents: DependencyNode[]
  indirect_dependents: DependencyNode[]
  affected_workflows: string[]
  total_impact_count: number
}

export interface FunctionDependencies {
  function_id: string
  function_name: string
  calls_to: FunctionDependency[]
  called_by: FunctionDependency[]
}

export const dependenciesService = {
  async getGraph(): Promise<DependencyGraph> {
    return api.get('/v1/dependencies/graph') as unknown as DependencyGraph
  },

  async getFunctionDependencies(functionId: string): Promise<FunctionDependencies> {
    return api.get(`/v1/functions/${functionId}/dependencies`) as unknown as FunctionDependencies
  },

  async getImpactAnalysis(functionId: string): Promise<ImpactAnalysis> {
    return api.get(`/v1/functions/${functionId}/impact`) as unknown as ImpactAnalysis
  },
}
