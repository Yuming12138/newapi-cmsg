/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import dayjs from 'dayjs'
import { useQuery } from '@tanstack/react-query'
import { BrainCircuit, ExternalLink } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { Skeleton } from '@/components/ui/skeleton'
import { getCodexRadarOverview } from '../../api'
import type { CodexRadarMetric } from '../../types'

const REFRESH_INTERVAL_MS = 10 * 60 * 1000

interface MetricGroup {
  family: string
  label: string
  efforts: string[]
  firstCardClassName?: string
}

const METRIC_GROUPS: MetricGroup[] = [
  {
    family: 'sol',
    label: 'Sol',
    efforts: ['ultra', 'max', 'xhigh', 'high', 'medium', 'low'],
  },
  {
    family: 'terra',
    label: 'Terra',
    efforts: ['ultra', 'max', 'xhigh', 'high', 'medium', 'low'],
  },
  {
    family: 'luna',
    label: 'Luna',
    efforts: ['max', 'xhigh', 'high', 'medium', 'low'],
    firstCardClassName: 'xl:col-start-2',
  },
  {
    family: 'gpt-5.5',
    label: 'GPT-5.5',
    efforts: ['xhigh', 'high'],
    firstCardClassName: 'xl:col-start-3',
  },
]

const FAMILY_STYLES: Record<
  string,
  { accent: string; border: string; surface: string; value: string }
> = {
  sol: {
    accent: 'bg-warning',
    border: 'border-warning/40',
    surface: 'bg-warning/5',
    value: 'text-warning',
  },
  terra: {
    accent: 'bg-blue-500',
    border: 'border-blue-500/40',
    surface: 'bg-blue-500/5',
    value: 'text-blue-600 dark:text-blue-400',
  },
  luna: {
    accent: 'bg-zinc-400',
    border: 'border-zinc-400/40',
    surface: 'bg-muted/20',
    value: 'text-foreground',
  },
  'gpt-5.5': {
    accent: 'bg-cyan-500',
    border: 'border-cyan-500/40',
    surface: 'bg-cyan-500/5',
    value: 'text-cyan-600 dark:text-cyan-400',
  },
}

function metricSlot(family: string, effort: string): string {
  return `${family}:${effort}`
}

function MetricCard(props: {
  family: string
  familyLabel: string
  effort: string
  metric?: CodexRadarMetric
  className?: string
}) {
  const { t } = useTranslation()
  const styles = FAMILY_STYLES[props.family] ?? FAMILY_STYLES.luna
  const displayName = `${props.familyLabel} ${props.effort}`
  const minutes = props.metric
    ? Math.max(1, Math.round(props.metric.average_task_seconds / 60))
    : null
  const ariaLabel = props.metric
    ? `${displayName}, ${t('dashboard.overview.codexRadar.iq')} ${props.metric.score.toFixed(1)}, ${t('dashboard.overview.codexRadar.cost')} $${props.metric.average_cost_usd.toFixed(1)}, ${t('dashboard.overview.codexRadar.duration')} ${minutes}`
    : `${displayName}, ${t('dashboard.overview.codexRadar.pending')}`

  return (
    <div
      className={cn(
        'bg-card relative grid min-h-28 min-w-0 grid-cols-[minmax(0,1fr)_4.25rem] overflow-hidden rounded-md border shadow-xs',
        styles.border,
        styles.surface,
        props.className
      )}
      aria-label={ariaLabel}
    >
      <span
        className={cn('absolute inset-x-0 top-0 h-1', styles.accent)}
        aria-hidden='true'
      />
      <div className='flex min-w-0 flex-col justify-between px-3 py-3'>
        <span
          className='truncate text-xs font-semibold sm:text-sm'
          title={displayName}
        >
          {displayName}
        </span>
        {props.metric ? (
          <span
            className={cn(
              'mt-3 text-3xl leading-none font-semibold tabular-nums',
              styles.value
            )}
            title={t('dashboard.overview.codexRadar.iq')}
          >
            {props.metric.score.toFixed(1)}
          </span>
        ) : (
          <span className='text-muted-foreground mt-3 text-[11px] leading-4'>
            {t('dashboard.overview.codexRadar.pending')}
          </span>
        )}
      </div>

      <div className='border-border/70 grid grid-rows-2 border-l'>
        <span
          className={cn(
            'flex items-center justify-center border-b text-sm font-semibold tabular-nums',
            props.metric ? styles.value : 'text-muted-foreground'
          )}
          title={t('dashboard.overview.codexRadar.cost')}
        >
          {props.metric ? `$${props.metric.average_cost_usd.toFixed(1)}` : '—'}
        </span>
        <span
          className={cn(
            'flex items-center justify-center text-sm font-semibold tabular-nums',
            props.metric ? styles.value : 'text-muted-foreground'
          )}
          title={t('dashboard.overview.codexRadar.duration')}
        >
          {minutes == null
            ? '—'
            : t('dashboard.overview.codexRadar.minutes', { count: minutes })}
        </span>
      </div>
    </div>
  )
}

export function CodexRadarPanel() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['dashboard', 'codex-radar', 'overview'],
    queryFn: getCodexRadarOverview,
    staleTime: REFRESH_INTERVAL_MS,
    refetchInterval: REFRESH_INTERVAL_MS,
    retry: 1,
  })
  const metrics = query.data?.metrics ?? []
  const metricsBySlot = new Map(
    metrics.map((metric) => [
      metricSlot(metric.family, metric.reasoning_effort),
      metric,
    ])
  )
  const updatedAt = query.data?.updated_at
    ? dayjs(query.data.updated_at).format('M/D HH:mm')
    : ''

  return (
    <section className='min-w-0' aria-labelledby='codex-radar-heading'>
      <div className='mb-3 flex flex-col justify-between gap-2 sm:flex-row sm:items-start'>
        <div className='min-w-0'>
          <div className='flex flex-wrap items-center gap-x-3 gap-y-1'>
            <h3
              id='codex-radar-heading'
              className='flex items-center gap-2 text-lg font-semibold'
            >
              <BrainCircuit
                className='text-muted-foreground size-5'
                aria-hidden='true'
              />
              {t('dashboard.overview.codexRadar.title')}
            </h3>
            {updatedAt && (
              <span className='text-muted-foreground text-xs tabular-nums'>
                {t('dashboard.overview.codexRadar.updatedAt', {
                  time: updatedAt,
                })}
                {query.data?.stale && (
                  <span className='text-warning ml-1'>
                    {t('dashboard.overview.codexRadar.cached')}
                  </span>
                )}
              </span>
            )}
          </div>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t('dashboard.overview.codexRadar.description')}
          </p>
        </div>

        <a
          href={query.data?.source_url ?? 'https://codexradar.com/'}
          target='_blank'
          rel='noreferrer'
          className='text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex shrink-0 items-center gap-1 rounded-sm text-xs outline-none focus-visible:ring-2'
        >
          {query.data?.attribution ??
            t('dashboard.overview.codexRadar.attribution')}
          <ExternalLink className='size-3' aria-hidden='true' />
        </a>
      </div>

      {query.isLoading ? (
        <div className='grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-6'>
          {Array.from({ length: 12 }, (_, index) => (
            <Skeleton key={index} className='h-28 rounded-md' />
          ))}
        </div>
      ) : query.isError || metrics.length === 0 ? (
        <div className='text-muted-foreground flex min-h-28 items-center justify-center rounded-md border text-sm'>
          {t('dashboard.overview.codexRadar.unavailable')}
        </div>
      ) : (
        <div className='space-y-2.5'>
          {METRIC_GROUPS.map((group) => (
            <div
              key={group.family}
              className='grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-6'
              aria-label={group.label}
            >
              {group.efforts.map((effort, index) => (
                <MetricCard
                  key={effort}
                  family={group.family}
                  familyLabel={group.label}
                  effort={effort}
                  metric={metricsBySlot.get(metricSlot(group.family, effort))}
                  className={index === 0 ? group.firstCardClassName : undefined}
                />
              ))}
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
