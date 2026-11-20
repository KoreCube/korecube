import { api, apiGet, type ApiEnvelope } from '@/lib/api'

export type GPUSignal = {
  node: string
  gpu?: string
  kind: string
  code?: string
  recoverable: boolean
  message: string
  source: string
}

export type GPUIncident = {
  node: string
  severity: string
  action: string
  tier: string
  autoEligible: boolean
  executable: boolean
  needsConfirm: boolean
  confidence: number
  summary: string
  runbook: string
  signals: GPUSignal[]
  affectedNamespaces: string[]
}

export type GPUHealth = {
  source: string
  policy: string
  readOnly: boolean
  signalCount: number
  incidents: GPUIncident[]
}

export type GPUNamespaceUse = {
  namespace: string
  allocatedGpu: number
  exclusivePods: number
  utilPercent: number
  utilKnown: boolean
  waste: boolean
  suggestion: string
}

export type GPUWaste = {
  source: string
  threshold: number
  namespaces: GPUNamespaceUse[]
}

export type StormGroup = {
  key: string
  kind: string
  name: string
  node?: string
  count: number
  reasons: string[]
  sample: string
}

export type GPUStorm = {
  source: string
  events: number
  groups: number
  suppressed: number
  items: StormGroup[]
}

export type GPUDecision = {
  at: string
  user: string
  node: string
  action: string
  tier: string
  outcome: string
  detail: string
}

export type GPUAnomaly = {
  entity: string
  kind: 'gpu' | 'pod' | 'node'
  node?: string
  gpu?: string
  namespace?: string
  pod?: string
  score: number
  streak: number
  since: string
  latest: Record<string, number>
  baseline: Record<string, number>
}

export type GPUAnomalyReport = {
  source: string
  at: string
  threshold: number
  persist: number
  entities: number
  trained: number
  transient: number
  persistent: GPUAnomaly[] | null
  unavailable?: string
}

export type GPUNodeCapacity = {
  node: string
  product?: string
  allocatable: number
  requested: number
  free: number
  health: 'ok' | 'warning' | 'critical'
}

export type GPUCapacityRisk = {
  severity: 'warning' | 'critical'
  kind: string
  target?: string
  message: string
}

export type GPUCapacity = {
  source: string
  allocatable: number
  requested: number
  usable: number
  free: number
  pendingPods: number
  pendingGpu: number
  nodes: GPUNodeCapacity[] | null
  risks: GPUCapacityRisk[] | null
}

export type ScalingRequest = {
  kind: 'keda' | 'karpenter'
  namespace?: string
  target?: string
  trigger?: string
  query?: string
  threshold?: string
  minReplicas?: number
  maxReplicas?: number
  pool?: string
  gpuLimit?: number
}

export type ScalingPlan = {
  id: string
  kind: string
  apiVersion: string
  resource: string
  namespace?: string
  name: string
  mode: 'create' | 'update'
  summary: string
  yaml: string
  warnings?: string[]
  status: 'pending' | 'applied' | 'rejected' | 'failed'
  createdAt: string
  decidedAt?: string
  decidedBy?: string
  detail?: string
}

async function postGPU<T>(path: string, body: unknown, lang?: string) {
  const res = await api.post<ApiEnvelope<T>>(path, body, { params: lang ? { lang } : undefined })
  if (res.data.code !== 0 && res.data.code !== 200) {
    throw new Error(res.data.message || 'Request failed')
  }
  return res.data.data
}

export function getGPUAnomalies() {
  return apiGet<GPUAnomalyReport>('/api/v1/gpu/anomalies')
}

export function getGPUCapacity(lang: string) {
  return apiGet<GPUCapacity>('/api/v1/gpu/capacity', { lang })
}

export function getScalingPlans() {
  return apiGet<ScalingPlan[]>('/api/v1/gpu/scaling')
}

export function proposeScaling(req: ScalingRequest, lang: string) {
  return postGPU<ScalingPlan>('/api/v1/gpu/proposals', req, lang)
}

export function decideScaling(id: string, approve: boolean) {
  return postGPU<ScalingPlan>('/api/v1/gpu/scaling', { id, approve })
}

export function getGPUHealth(lang: string) {
  return apiGet<GPUHealth>('/api/v1/gpu/health', { lang })
}

export function getGPUWaste() {
  return apiGet<GPUWaste>('/api/v1/gpu/waste')
}

export function getGPUStorm() {
  return apiGet<GPUStorm>('/api/v1/gpu/storm')
}

export function getGPUDecisions() {
  return apiGet<GPUDecision[]>('/api/v1/gpu/decisions')
}

export async function runGPUAction(node: string, action: 'cordon' | 'drain', confirm: boolean, lang: string) {
  const res = await api.post<ApiEnvelope<GPUDecision>>(
    '/api/v1/gpu/actions',
    { node, action, confirm },
    { params: { lang }, timeout: 130000 },
  )
  if (res.data.code !== 0 && res.data.code !== 200) {
    throw new Error(res.data.message || 'Request failed')
  }
  return res.data.data
}
