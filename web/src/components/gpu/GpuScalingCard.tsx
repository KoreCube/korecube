import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Badge, Button, Card, EmptyState, HudSelect, Input, Modal } from '@/components/ui'
import { useCluster } from '@/store/cluster'
import { decideScaling, getScalingPlans, proposeScaling, type ScalingPlan, type ScalingRequest } from '@/api/gpuOps'

const statusTone: Record<ScalingPlan['status'], 'accent' | 'ok' | 'neutral' | 'danger'> = {
  pending: 'accent',
  applied: 'ok',
  rejected: 'neutral',
  failed: 'danger',
}

export function GpuScalingCard({ readOnly }: { readOnly: boolean }) {
  const { t, i18n } = useTranslation()
  const lang = i18n.language?.toLowerCase().startsWith('zh') ? 'zh' : 'en'
  const { clusterId } = useCluster()
  const qc = useQueryClient()
  const [params] = useSearchParams()
  const highlight = params.get('proposal')
  const highlightRef = useRef<HTMLDivElement | null>(null)

  const plansQ = useQuery({
    queryKey: ['gpu-scaling', clusterId],
    queryFn: getScalingPlans,
    enabled: Boolean(clusterId),
    refetchInterval: 15_000,
  })
  const plans = plansQ.data || []

  const [kind, setKind] = useState<'keda' | 'karpenter'>('keda')
  const [namespace, setNamespace] = useState('llm-inference')
  const [target, setTarget] = useState('')
  const [maxReplicas, setMaxReplicas] = useState('8')
  const [threshold, setThreshold] = useState('10')
  const [pool, setPool] = useState('gpu')
  const [gpuLimit, setGpuLimit] = useState('16')
  const [reviewing, setReviewing] = useState<ScalingPlan | null>(null)

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['gpu-scaling'] })
    void qc.invalidateQueries({ queryKey: ['gpu-decisions'] })
  }
  const propose = useMutation({
    mutationFn: () => {
      const req: ScalingRequest =
        kind === 'keda'
          ? { kind, namespace, target: target || undefined, maxReplicas: Number(maxReplicas) || undefined, minReplicas: 1, threshold }
          : { kind, pool, gpuLimit: Number(gpuLimit) || undefined }
      return proposeScaling(req, lang)
    },
    onSuccess: refresh,
  })
  const decide = useMutation({
    mutationFn: (vars: { id: string; approve: boolean }) => decideScaling(vars.id, vars.approve),
    onSettled: () => {
      setReviewing(null)
      refresh()
    },
  })

  useEffect(() => {
    if (!highlight || !highlightRef.current) return
    // cards above this one are still loading and shift the layout
    const timer = window.setTimeout(
      () => highlightRef.current?.scrollIntoView({ block: 'center', behavior: 'smooth' }),
      600,
    )
    return () => window.clearTimeout(timer)
  }, [highlight, plans.length])

  const err = propose.error || decide.error
  return (
    <Card className="flex min-w-0 flex-col gap-3 p-5">
      <div className="flex flex-col gap-1">
        <h2 className="text-sm font-medium">{t('gpuops.scaling')}</h2>
        <p className="text-xs opacity-70">{t('gpuops.scalingHint')}</p>
      </div>

      <div className="flex flex-wrap items-end gap-2">
        <HudSelect
          className="w-36"
          value={kind}
          onChange={(v) => setKind(v as 'keda' | 'karpenter')}
          options={[
            { value: 'keda', label: 'KEDA' },
            { value: 'karpenter', label: 'Karpenter' },
          ]}
        />
        {kind === 'keda' ? (
          <>
            <Field label="Namespace" value={namespace} onChange={setNamespace} />
            <Field label="Deployment" value={target} onChange={setTarget} placeholder={t('gpuops.auto')} />
            <Field label={t('gpuops.maxReplicas')} value={maxReplicas} onChange={setMaxReplicas} narrow />
            <Field label={t('gpuops.queuePerReplica')} value={threshold} onChange={setThreshold} narrow />
          </>
        ) : (
          <>
            <Field label="NodePool" value={pool} onChange={setPool} />
            <Field label={t('gpuops.gpuLimit')} value={gpuLimit} onChange={setGpuLimit} narrow />
          </>
        )}
        <Button type="button" variant="outline" disabled={propose.isPending} onClick={() => propose.mutate()}>
          {t('gpuops.draft')}
        </Button>
      </div>
      {err && <p className="text-sm text-red-400">{err instanceof Error ? err.message : String(err)}</p>}

      {plans.length === 0 ? (
        <EmptyState>{t('gpuops.noPlans')}</EmptyState>
      ) : (
        plans.map((p) => (
          <div
            key={p.id}
            ref={p.id === highlight ? highlightRef : undefined}
            className={`flex flex-col gap-2 rounded border p-3 text-sm ${p.id === highlight ? 'border-cyan' : 'border-line'}`}
          >
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{p.id}</span>
              <Badge tone="neutral">{p.kind}</Badge>
              <Badge tone={statusTone[p.status]}>{p.status}</Badge>
              <span className="text-xs opacity-70">
                {p.mode} · {p.namespace ? `${p.namespace}/` : ''}
                {p.name}
              </span>
            </div>
            <p className="opacity-80">{p.summary}</p>
            {(p.warnings || []).map((w) => (
              <p key={w} className="text-xs text-warn">
                ! {w}
              </p>
            ))}
            {p.detail && <p className="text-xs opacity-70">{p.detail}</p>}
            <details>
              <summary className="cursor-pointer text-xs opacity-70">YAML</summary>
              <pre className="mt-2 overflow-x-auto rounded bg-panel-solid p-3 text-xs">{p.yaml}</pre>
            </details>
            {p.status === 'pending' && (
              <div className="flex gap-2">
                <Button
                  type="button"
                  className="px-3 py-1.5 text-xs"
                  disabled={readOnly || decide.isPending}
                  onClick={() => setReviewing(p)}
                >
                  {t('gpuops.approve')}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  className="px-3 py-1.5 text-xs"
                  disabled={decide.isPending}
                  onClick={() => decide.mutate({ id: p.id, approve: false })}
                >
                  {t('gpuops.reject')}
                </Button>
              </div>
            )}
          </div>
        ))
      )}

      <Modal
        open={reviewing !== null}
        title={`${t('gpuops.approve')} · ${reviewing?.id ?? ''}`}
        subtitle={reviewing ? `${reviewing.mode} ${reviewing.kind} ${reviewing.name}` : undefined}
        onClose={() => setReviewing(null)}
      >
        {reviewing && (
          <div className="flex flex-col gap-3 p-5 text-sm">
            <p>{reviewing.summary}</p>
            {(reviewing.warnings || []).map((w) => (
              <p key={w} className="text-xs text-warn">
                ! {w}
              </p>
            ))}
            <pre className="max-h-80 overflow-auto rounded bg-panel-solid p-3 text-xs">{reviewing.yaml}</pre>
            <div className="flex justify-end gap-2">
              <Button type="button" variant="ghost" onClick={() => setReviewing(null)}>
                {t('common.cancel')}
              </Button>
              <Button
                type="button"
                disabled={decide.isPending}
                onClick={() => decide.mutate({ id: reviewing.id, approve: true })}
              >
                {t('gpuops.applyNow')}
              </Button>
            </div>
          </div>
        )}
      </Modal>
    </Card>
  )
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  narrow,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
  narrow?: boolean
}) {
  return (
    <label className={`flex flex-col gap-1 text-xs opacity-90 ${narrow ? 'w-28' : 'w-44'}`}>
      {label}
      <Input value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} className="py-1.5" />
    </label>
  )
}
