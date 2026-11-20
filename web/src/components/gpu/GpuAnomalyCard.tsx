import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Badge, Card, EmptyState } from '@/components/ui'
import { useCluster } from '@/store/cluster'
import { getGPUAnomalies, type GPUAnomaly } from '@/api/gpuOps'

const FEATURE_UNIT: Record<string, string> = { util: '%', fb_used: ' MiB', power: ' W', temp: ' °C' }

export function GpuAnomalyCard() {
  const { t } = useTranslation()
  const { clusterId } = useCluster()
  const q = useQuery({
    queryKey: ['gpu-anomalies', clusterId],
    queryFn: getGPUAnomalies,
    enabled: Boolean(clusterId),
    refetchInterval: 60_000,
  })
  const rep = q.data
  const items = rep?.persistent || []

  return (
    <Card className="flex min-w-0 flex-col gap-3 p-5">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-sm font-medium">{t('gpuops.anomalies')}</h2>
        {rep && !rep.unavailable && (
          <span className="text-xs opacity-70">
            {t('gpuops.anomalyMeta', {
              entities: rep.entities,
              trained: rep.trained,
              threshold: rep.threshold.toFixed(2),
              persist: rep.persist,
              transient: rep.transient,
            })}
          </span>
        )}
      </div>
      {q.isError && <p className="text-sm text-red-400">{q.error instanceof Error ? q.error.message : String(q.error)}</p>}
      {rep?.unavailable ? (
        <EmptyState>
          {t('gpuops.anomalyUnavailable')}: {rep.unavailable}
        </EmptyState>
      ) : items.length === 0 ? (
        <EmptyState>{t('gpuops.noAnomalies')}</EmptyState>
      ) : (
        items.map((a) => <AnomalyRow key={a.entity} a={a} minutes={t('gpuops.minutes', { n: a.streak })} />)
      )}
    </Card>
  )
}

function AnomalyRow({ a, minutes }: { a: GPUAnomaly; minutes: string }) {
  const features = Object.keys(a.latest).sort()
  return (
    <div className="flex flex-col gap-1 border-t border-line py-2 text-sm first:border-0">
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone="accent">{a.kind}</Badge>
        <span className="font-medium">{a.entity.replace(/^\w+:/, '')}</span>
        <Badge tone="warn">{minutes}</Badge>
        <span className="text-xs opacity-70">score {a.score.toFixed(2)}</span>
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs opacity-80">
        {features.map((f) => (
          <span key={f}>
            {f} {Math.round(a.latest[f])}
            {FEATURE_UNIT[f] || ''} <span className="opacity-60">/ {Math.round(a.baseline[f])}</span>
          </span>
        ))}
      </div>
    </div>
  )
}
