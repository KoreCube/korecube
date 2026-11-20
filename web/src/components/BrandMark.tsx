import { Link } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { APP_VERSION, formatAppVersion } from '@/lib/version'

type Props = {
  className?: string
  /** Compact "KC" for narrow headers */
  compact?: boolean
  /** When set, wraps brand in a router Link */
  to?: string
  /** Show version chip next to the mark (default true) */
  showVersion?: boolean
  brandClassName?: string
}

export function BrandMark({
  className,
  compact = false,
  to,
  showVersion = true,
  brandClassName,
}: Props) {
  const mark = (
    <span className={cn('inline-flex items-center gap-1.5', className)}>
      <span className={cn('hud-brand', brandClassName)}>
        {compact ? (
          <>
            K<span className="accent">C</span>
          </>
        ) : (
          <>
            Kore<span className="accent">Cube</span>
          </>
        )}
      </span>
      {showVersion ? (
        <span className="translate-y-[-0.05em] font-mono text-[10px] font-medium tracking-[0.08em] text-text-dim sm:text-[11px]">
          {formatAppVersion(APP_VERSION)}
        </span>
      ) : null}
    </span>
  )

  if (to) {
    return (
      <Link to={to} className="inline-flex shrink-0 items-center no-underline hover:opacity-90">
        {mark}
      </Link>
    )
  }
  return mark
}
