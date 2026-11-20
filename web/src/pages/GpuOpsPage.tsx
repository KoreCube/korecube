import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Badge, Button, Card, EmptyState, Modal, PageHeader, StatCard } from '@/components/ui'
import { GpuAnomalyCard } from '@/components/gpu/GpuAnomalyCard'
import { GpuCapacityCard } from '@/components/gpu/GpuCapacityCard'
import { GpuScalingCard } from '@/components/gpu/GpuScalingCard'
import { upsertCustomSkill } from '@/lib/aiSkills'
import { useCluster } from '@/store/cluster'
import {
  getGPUDecisions,
  getGPUHealth,
  getGPUStorm,
  getGPUWaste,
  runGPUAction,
  type GPUIncident,
} from '@/api/gpuOps'

const suggestionText: Record<string, { zh: string; en: string }> = {
  mig_or_timeslice: {
    zh: '低利用率且独占整卡，建议 MIG 或 time-slicing',
    en: 'Exclusive card below the threshold: consider MIG or time-slicing',
  },
  need_dcgm: {
    zh: '独占整卡，但没有 DCGM 利用率',
    en: 'Exclusive card, utilization unknown until DCGM is connected',
  },
  none: { zh: '无需调整', en: 'No change' },
}

export function GpuOpsPage() {
  const { t, i18n } = useTranslation()
  const { clusterId } = useCluster()
  const qc = useQueryClient()
  const lang = i18n.language?.toLowerCase().startsWith('zh') ? 'zh' : 'en'
  const zh = lang === 'zh'

  const healthQ = useQuery({
    queryKey: ['gpu-health', clusterId, lang],
    queryFn: () => getGPUHealth(lang),
    enabled: Boolean(clusterId),
    refetchInterval: 30_000,
  })
  const wasteQ = useQuery({
    queryKey: ['gpu-waste', clusterId],
    queryFn: getGPUWaste,
    enabled: Boolean(clusterId),
    refetchInterval: 30_000,
  })
  const stormQ = useQuery({
    queryKey: ['gpu-storm', clusterId],
    queryFn: getGPUStorm,
    enabled: Boolean(clusterId),
    refetchInterval: 30_000,
  })
  const decisionsQ = useQuery({
    queryKey: ['gpu-decisions', clusterId],
    queryFn: getGPUDecisions,
    enabled: Boolean(clusterId),
  })

  const [pending, setPending] = useState<{ inc: GPUIncident; action: 'cordon' | 'drain' } | null>(null)
  const [savedSkills, setSavedSkills] = useState<Record<string, boolean>>({})

  const saveRunbook = (inc: GPUIncident) => {
    const signals = inc.signals.map((s) => `${s.kind}${s.code ? ` ${s.code}` : ''}`).join(', ')
    const prompt = zh
      ? `节点 ${inc.node} 出现 GPU 故障（${signals}）。先调用 get_gpu_health 和 get_gpu_anomalies 确认现状，再按下面的 Runbook 逐步排查；只给建议，不要执行变更。\nRunbook：${inc.runbook}`
      : `GPU fault on node ${inc.node} (${signals}). Call get_gpu_health and get_gpu_anomalies to confirm the current state, then walk through this runbook. Recommend only; do not change the cluster.\nRunbook: ${inc.runbook}`
    const saved = upsertCustomSkill({ label: `GPU ${inc.node}`, blurb: inc.summary, prompt }, lang)
    if (saved) setSavedSkills((m) => ({ ...m, [inc.node]: true }))
  }

  const act = useMutation({
    mutationFn: (vars: { node: string; action: 'cordon' | 'drain'; confirm: boolean }) =>
      runGPUAction(vars.node, vars.action, vars.confirm, lang),
    onSettled: () => {
      setPending(null)
      void qc.invalidateQueries({ queryKey: ['gpu-decisions'] })
      void qc.invalidateQueries({ queryKey: ['gpu-health'] })
    },
  })

  const health = healthQ.data
  const incidents = health?.incidents || []
  const waste = wasteQ.data
  const storm = stormQ.data
  const decisions = decisionsQ.data || []

  return (
    <div className="flex w-full min-w-0 flex-col gap-4">
      <PageHeader
        title={t('gpuops.title')}
        subtitle={t('gpuops.subtitle')}
        action={
          <Badge tone={health?.readOnly ? 'warn' : health?.policy === 'auto' ? 'danger' : 'accent'}>
            {health?.readOnly ? t('gpuops.readOnly') : health?.policy || 'approve'}
          </Badge>
        }
      />

      <div className="grid w-full grid-cols-2 gap-4 lg:grid-cols-4">
        <StatCard label={t('gpuops.health')} value={incidents.length} />
        <StatCard label={t('gpuops.waste')} value={waste?.namespaces.filter((n) => n.waste).length ?? '-'} />
        <StatCard
          label={t('gpuops.suppressed')}
          value={storm ? `${Math.round(storm.suppressed * 100)}%` : '-'}
        />
        <StatCard label={t('gpuops.decisions')} value={decisions.length} />
      </div>

      {healthQ.isError && <ErrorLine error={healthQ.error} />}
      {act.isError && <ErrorLine error={act.error} />}

      <Card className="flex min-w-0 flex-col gap-3 p-5">
        <h2 className="text-sm font-medium">{t('gpuops.health')}</h2>
        {incidents.length === 0 ? (
          <EmptyState>{t('gpuops.noIncidents')}</EmptyState>
        ) : (
          incidents.map((inc) => (
            <IncidentRow
              key={inc.node}
              inc={inc}
              busy={act.isPending}
              cordonLabel={t('gpuops.cordon')}
              drainLabel={t('gpuops.drain')}
              skillLabel={savedSkills[inc.node] ? t('gpuops.skillSaved') : t('gpuops.saveSkill')}
              skillSaved={Boolean(savedSkills[inc.node])}
              onSaveSkill={() => saveRunbook(inc)}
              onRun={(action) =>
                inc.needsConfirm
                  ? setPending({ inc, action })
                  : act.mutate({ node: inc.node, action, confirm: false })
              }
            />
          ))
        )}
      </Card>

      <GpuAnomalyCard />
      <GpuCapacityCard />

      <Modal
        open={pending !== null}
        title={`${pending?.action === 'drain' ? t('gpuops.drain') : t('gpuops.cordon')} · ${pending?.inc.node ?? ''}`}
        subtitle={pending ? `${pending.inc.tier} · ${Math.round(pending.inc.confidence * 100)}%` : undefined}
        onClose={() => setPending(null)}
      >
        {pending && (
          <div className="flex flex-col gap-3 p-5 text-sm">
            <p>{pending.inc.runbook}</p>
            <ul className="list-disc pl-5 opacity-80">
              {pending.inc.signals.map((s, i) => (
                <li key={`${s.kind}-${s.gpu ?? ''}-${i}`}>
                  {s.source} · {s.kind}
                  {s.code ? ` ${s.code}` : ''} · {s.message}
                </li>
              ))}
            </ul>
            {pending.inc.affectedNamespaces.length > 0 && (
              <p className="opacity-80">
                {t('gpuops.affected')}: {pending.inc.affectedNamespaces.join(', ')}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <Button type="button" variant="ghost" onClick={() => setPending(null)}>
                {t('common.cancel')}
              </Button>
              <Button
                type="button"
                variant={pending.action === 'drain' ? 'danger' : 'primary'}
                disabled={act.isPending}
                onClick={() => act.mutate({ node: pending.inc.node, action: pending.action, confirm: true })}
              >
                {t('gpuops.confirm')}
              </Button>
            </div>
          </div>
        )}
      </Modal>

      <Card className="flex min-w-0 flex-col gap-3 p-5">
        <h2 className="text-sm font-medium">{t('gpuops.waste')}</h2>
        {(waste?.namespaces.length || 0) === 0 ? (
          <EmptyState>{t('gpuops.noWaste')}</EmptyState>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead className="text-xs opacity-70">
                <tr>
                  <th className="py-1 pr-3">Namespace</th>
                  <th className="py-1 pr-3">{t('gpuops.allocated')}</th>
                  <th className="py-1 pr-3">{t('gpuops.util')}</th>
                  <th className="py-1">{t('gpuops.suggestion')}</th>
                </tr>
              </thead>
              <tbody>
                {waste?.namespaces.map((row) => (
                    <tr key={row.namespace} className="border-t border-line">
                    <td className="py-2 pr-3">{row.namespace}</td>
                    <td className="py-2 pr-3">{row.allocatedGpu.toFixed(2)}</td>
                    <td className="py-2 pr-3">{row.utilKnown ? `${Math.round(row.utilPercent)}%` : '—'}</td>
                    <td className="py-2">{(zh ? suggestionText[row.suggestion]?.zh : suggestionText[row.suggestion]?.en) || row.suggestion}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card className="flex min-w-0 flex-col gap-3 p-5">
        <h2 className="text-sm font-medium">
          {t('gpuops.storm')}
          {storm ? ` · ${storm.events} → ${storm.groups}` : ''}
        </h2>
        {(storm?.items.length || 0) === 0 ? (
          <EmptyState>{t('gpuops.noStorm')}</EmptyState>
        ) : (
          storm?.items.map((g) => (
            <div key={g.key} className="text-sm">
              <span className="font-medium">
                {g.kind}/{g.name}
              </span>{' '}
              <Badge tone="neutral">x{g.count}</Badge>{' '}
              <span className="opacity-70">{g.reasons.join(', ')}</span>
            </div>
          ))
        )}
      </Card>

      <GpuScalingCard readOnly={Boolean(health?.readOnly)} />

      <Card className="flex min-w-0 flex-col gap-2 p-5">
        <h2 className="text-sm font-medium">{t('gpuops.decisions')}</h2>
        {decisions.length === 0 ? (
          <EmptyState>{t('gpuops.noDecisions')}</EmptyState>
        ) : (
          [...decisions].reverse().slice(0, 12).map((d, i) => (
            <div key={`${d.at}-${i}`} className="text-sm">
              <Badge tone={d.outcome === 'executed' ? 'ok' : 'warn'}>{d.outcome}</Badge>{' '}
              {d.node} {d.action} {d.tier} · {d.detail}
            </div>
          ))
        )}
      </Card>
    </div>
  )
}

function IncidentRow({
  inc,
  busy,
  cordonLabel,
  drainLabel,
  skillLabel,
  skillSaved,
  onSaveSkill,
  onRun,
}: {
  inc: GPUIncident
  busy: boolean
  cordonLabel: string
  drainLabel: string
  skillLabel: string
  skillSaved: boolean
  onSaveSkill: () => void
  onRun: (action: 'cordon' | 'drain') => void
}) {
  const tone = inc.severity === 'critical' ? 'danger' : 'warn'
  return (
    <div className="flex flex-col gap-2 border-t border-line py-3 first:border-0">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{inc.node}</span>
        <Badge tone={tone}>{inc.severity}</Badge>
        <Badge tone="accent">{inc.tier}</Badge>
        <span className="text-xs opacity-70">{Math.round(inc.confidence * 100)}%</span>
      </div>
      <p className="text-sm opacity-80">{inc.runbook}</p>
      <div className="flex flex-wrap gap-2">
        {inc.executable && (
          <>
            <Button type="button" variant="outline" className="px-3 py-1.5 text-xs" disabled={busy} onClick={() => onRun('cordon')}>
              {cordonLabel}
            </Button>
            {inc.action === 'cordon_and_drain' && (
              <Button type="button" variant="danger" className="px-3 py-1.5 text-xs" disabled={busy} onClick={() => onRun('drain')}>
                {drainLabel}
              </Button>
            )}
          </>
        )}
        <Button type="button" variant="ghost" className="px-3 py-1.5 text-xs" disabled={skillSaved} onClick={onSaveSkill}>
          {skillLabel}
        </Button>
      </div>
    </div>
  )
}

function ErrorLine({ error }: { error: unknown }) {
  const message = error instanceof Error ? error.message : String(error)
  return <p className="text-sm text-red-400">{message}</p>
}
