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
