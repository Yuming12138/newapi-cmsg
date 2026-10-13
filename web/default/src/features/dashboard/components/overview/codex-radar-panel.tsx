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
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { getCodexRadarOverview } from '../../api'
import type { CodexRadarMetric } from '../../types'

const REFRESH_INTERVAL_MS = 10 * 60 * 1000
const EFFORTS = ['ultra', 'max', 'xhigh', 'high', 'medium', 'low']
const METRIC_GROUPS = [
  { family: 'gpt-6-astra', label: 'GPT-6 Astra' },
  { family: 'gpt-6.1-sol', label: 'GPT-6.1 Sol' },
  { family: 'gpt-6-sol', label: 'GPT-6 Sol' },
  { family: 'gpt-5.6-sol', label: 'GPT-5.6 Sol' },
]

function metricSlot(family: string, effort: string): string {
  return `${family}:${effort}`
}

export function CodexRadarMetricCard(props: {
  familyLabel: string
  effort: string
  metric?: CodexRadarMetric
}) {
  const { t } = useTranslation()
  const metric = props.metric
  const scoreText =
    metric?.score == null
      ? t('dashboard.overview.codexRadar.pending')
      : metric.score.toFixed(1)
  const costText =
    metric?.average_cost_usd == null
      ? '—'
      : `$${metric.average_cost_usd.toFixed(2)}`
  const durationText =
    metric?.average_task_seconds == null
      ? '—'
      : t('dashboard.overview.codexRadar.minutes', {
          count: Math.round(metric.average_task_seconds / 6) / 10,
        })
  const coverageText = metric
    ? t('dashboard.overview.codexRadar.coverage', {
        count: metric.coverage,
        total: metric.tasks,
      })
    : t('dashboard.overview.codexRadar.pending')
  const displayName = `${props.familyLabel} ${props.effort}`

  return (
    <article
      className='bg-card min-w-0 rounded-md border px-3 py-3 shadow-xs'
      aria-label={displayName}
    >
      <div className='flex flex-wrap items-center justify-between gap-x-2 gap-y-1'>
        <span className='text-sm font-semibold'>{props.effort}</span>
        {metric && (
          <span className='text-muted-foreground text-xs tabular-nums'>
            {t('dashboard.overview.codexRadar.gradedRuns', {
              count: metric.graded_runs_count,
            })}
          </span>
        )}
      </div>
      <div
        className={cn(
          'mt-3 tabular-nums',
          metric?.score == null
            ? 'text-muted-foreground text-xs'
            : 'text-3xl leading-none font-semibold'
        )}
        title={t('dashboard.overview.codexRadar.iq')}
      >
        {scoreText}
      </div>
      <p className='text-muted-foreground mt-2 text-xs tabular-nums'>
        {coverageText}
      </p>
      {metric?.insufficient_data && (
        <p
          className='mt-1 text-xs'
          title={t('dashboard.overview.codexRadar.insufficientDataDetail')}
        >
          {t('dashboard.overview.codexRadar.insufficientData')}
        </p>
      )}
      <dl className='mt-3 grid grid-cols-2 gap-2 border-t pt-2 text-xs'>
        <div className='min-w-0'>
          <dt className='text-muted-foreground'>
            {t('dashboard.overview.codexRadar.cost')}
          </dt>
          <dd className='mt-1 font-medium tabular-nums'>{costText}</dd>
        </div>
        <div className='min-w-0 text-right'>
          <dt className='text-muted-foreground'>
            {t('dashboard.overview.codexRadar.duration')}
          </dt>
          <dd className='mt-1 font-medium tabular-nums'>{durationText}</dd>
        </div>
      </dl>
    </article>
  )
}

export function CodexRadarPanel() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['dashboard', 'codex-radar', 'overview', 'radar-bench-v2'],
    queryFn: getCodexRadarOverview,
    staleTime: REFRESH_INTERVAL_MS,
    refetchInterval: REFRESH_INTERVAL_MS,
    // Backend deduplication, stale fallback and failure backoff already protect
    // the upstream. Avoid duplicating the global error toast on auto-retries.
    retry: false,
  })
  const metrics = query.data?.metrics ?? []
  const metricsBySlot = new Map(
    metrics.map((metric) => [
      metricSlot(metric.family, metric.reasoning_effort),
      metric,
    ])
  )
  const fetchedAt = query.data?.fetched_at
    ? dayjs(query.data.fetched_at).format('M/D HH:mm')
    : ''

  let content
  if (query.isLoading) {
    content = (
      <div className='space-y-3' aria-busy='true'>
        {METRIC_GROUPS.map((group) => (
          <div key={group.family}>
            <Skeleton className='mb-2 h-5 w-28 rounded-sm' />
            <div className='grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-6'>
              {EFFORTS.map((effort) => (
                <Skeleton key={effort} className='h-44 rounded-md' />
              ))}
            </div>
          </div>
        ))}
      </div>
    )
  } else if (metrics.length === 0) {
    content = (
      <div className='text-muted-foreground flex min-h-28 flex-col items-center justify-center gap-3 rounded-md border text-sm'>
        <span>{t('dashboard.overview.codexRadar.unavailable')}</span>
        <Button
          variant='outline'
          size='sm'
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
        >
          {t('dashboard.overview.codexRadar.retry')}
        </Button>
      </div>
    )
  } else {
    content = (
      <div className='space-y-3'>
        {METRIC_GROUPS.map((group) => (
          <section key={group.family} aria-label={group.label}>
            <h4 className='mb-2 text-sm font-semibold'>{group.label}</h4>
            <div className='grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-6'>
              {EFFORTS.map((effort) => (
                <CodexRadarMetricCard
                  key={effort}
                  familyLabel={group.label}
                  effort={effort}
                  metric={metricsBySlot.get(metricSlot(group.family, effort))}
                />
              ))}
            </div>
          </section>
        ))}
      </div>
    )
  }

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
            {fetchedAt && (
              <span className='text-muted-foreground text-xs tabular-nums'>
                {t('dashboard.overview.codexRadar.syncedAt', {
                  time: fetchedAt,
                })}
                {query.data?.stale && (
                  <span className='ml-1'>
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
          {t('dashboard.overview.codexRadar.attribution')}
          <ExternalLink className='size-3' aria-hidden='true' />
        </a>
      </div>

      {content}
    </section>
  )
}
