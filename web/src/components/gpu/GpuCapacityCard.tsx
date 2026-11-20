import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Badge, Card, EmptyState } from '@/components/ui'
import { useCluster } from '@/store/cluster'
import { getGPUCapacity } from '@/api/gpuOps'

const HEALTH_TONE = { ok: 'ok', warning: 'warn', critical: 'danger' } as const

export function GpuCapacityCard() {
  const { t, i18n } = useTranslation()
  const { clusterId } = useCluster()
  const lang = i18n.language
  const q = useQuery({
    queryKey: ['gpu-capacity', clusterId, lang],
    queryFn: () => getGPUCapacity(lang),
    enabled: Boolean(clusterId),
    refetchInterval: 60_000,
  })
  const rep = q.data
  const nodes = rep?.nodes || []
  const risks = rep?.risks || []

  return (
    <Card className="flex min-w-0 flex-col gap-3 p-5">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-sm font-medium">{t('gpuops.capacity')}</h2>
        {rep && nodes.length > 0 && (
          <span className="text-xs opacity-70">
            {t('gpuops.capacityMeta', {
              free: rep.free,
              usable: rep.usable,
              allocatable: rep.allocatable,
              pods: rep.pendingPods,
              pending: rep.pendingGpu,
            })}
          </span>
        )}
      </div>
      {q.isError && <p className="text-sm text-red-400">{q.error instanceof Error ? q.error.message : String(q.error)}</p>}
      {rep && nodes.length === 0 ? (
        <EmptyState>{t('gpuops.noGpuNodes')}</EmptyState>
      ) : (
        <>
          {risks.length === 0 && rep && <EmptyState>{t('gpuops.noCapacityRisk')}</EmptyState>}
          {risks.map((r, i) => (
            <div key={`${r.kind}-${r.target || i}`} className="flex items-start gap-2 text-sm">
              <Badge tone={r.severity === 'critical' ? 'danger' : 'warn'}>{r.severity}</Badge>
              <span>{r.message}</span>
            </div>
          ))}
          <div className="flex flex-col gap-2">
            {nodes.map((n) => (
              <div key={n.node} className="flex flex-col gap-1 text-xs">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{n.node}</span>
                  <Badge tone={HEALTH_TONE[n.health]}>{n.health}</Badge>
                  <span className="opacity-70">
                    {n.requested}/{n.allocatable} · {t('gpuops.free')} {n.free}
                  </span>
                </div>
                <div className="h-1.5 overflow-hidden rounded bg-mist">
                  <div
                    className={n.health === 'critical' ? 'h-full bg-danger' : 'h-full bg-cyan'}
                    style={{ width: `${Math.min(100, (n.requested / n.allocatable) * 100)}%` }}
                  />
                </div>
              </div>
            ))}
          </div>
        </>
      )}
    </Card>
  )
}
