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
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { codexRadarQueryOptions } from '../../lib/codex-radar-query'
import { CodexRadarMetricCard } from './codex-radar-metric-card'

const METRIC_GRID_CLASS =
  'grid grid-cols-1 gap-2 @sm/radar:grid-cols-2 @3xl/radar:grid-cols-3 @6xl/radar:grid-cols-6'
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

export function CodexRadarPanel() {
  const { t } = useTranslation()
  const query = useQuery(codexRadarQueryOptions)
  const metrics = query.data?.metrics ?? []
  const showingCachedData =
    query.data?.stale || query.isError || query.failureCount > 0
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
  if (query.isLoading && query.failureCount === 0) {
    content = (
      <div className='space-y-4' aria-busy='true'>
        {METRIC_GROUPS.map((group) => (
          <div key={group.family}>
            <Skeleton className='mb-2 h-5 w-28 rounded-sm' />
            <div className={METRIC_GRID_CLASS}>
              {EFFORTS.map((effort) => (
                <Skeleton key={effort} className='h-44 rounded-lg' />
              ))}
            </div>
          </div>
        ))}
      </div>
    )
  } else if (metrics.length === 0) {
    content = (
      <div
        className='text-muted-foreground flex min-h-28 flex-col items-center justify-center gap-3 rounded-md border p-4 text-center text-sm'
        aria-busy={query.isFetching}
      >
        <div role='status' className='space-y-1'>
          <p>{t('dashboard.overview.codexRadar.unavailable')}</p>
          {query.isFetching && (
            <p className='text-xs'>
              {t('dashboard.overview.codexRadar.recovering')}
            </p>
          )}
        </div>
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
      <div className='space-y-4'>
        {METRIC_GROUPS.map((group) => (
          <section
            key={group.family}
            className='codex-radar-family'
            data-family={group.family}
            aria-label={group.label}
          >
            <div className='mb-2 flex items-center gap-2.5'>
              <span
                className='codex-radar-marker size-2 shrink-0 rounded-full'
                aria-hidden='true'
              />
              <h4 className='codex-radar-accent text-sm font-semibold'>
                {group.label}
              </h4>
              <div
                className='codex-radar-divider h-px flex-1'
                aria-hidden='true'
              />
            </div>
            <div className={METRIC_GRID_CLASS}>
              {EFFORTS.map((effort) => (
                <CodexRadarMetricCard
                  key={effort}
                  family={group.family}
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
    <section
      className='@container/radar min-w-0'
      aria-labelledby='codex-radar-heading'
    >
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
                {showingCachedData && (
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
          {metrics.length > 0 && showingCachedData && (
            <p role='status' className='text-muted-foreground mt-1 text-xs'>
              {t('dashboard.overview.codexRadar.cachedDetail')}
            </p>
          )}
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
