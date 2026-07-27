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
import { getCodexRadarOverview } from '../../api'
import type { CodexRadarMetric } from '../../types'
import { PanelWrapper } from '../ui/panel-wrapper'

const REFRESH_INTERVAL_MS = 15 * 60 * 1000

function scoreClassName(status: string): string {
  switch (status) {
    case 'green':
      return 'text-success'
    case 'yellow':
      return 'text-warning'
    case 'red':
      return 'text-destructive'
    default:
      return 'text-foreground'
  }
}

function familyAccentClassName(family: string): string {
  switch (family) {
    case 'sol':
      return 'bg-warning'
    case 'terra':
      return 'bg-blue-500'
    case 'luna':
      return 'bg-zinc-400'
    default:
      return 'bg-primary'
  }
}

function familySurfaceClassName(family: string): string {
  switch (family) {
    case 'sol':
      return 'bg-warning/5'
    case 'terra':
      return 'bg-blue-500/5'
    case 'luna':
      return 'bg-muted/25'
    default:
      return 'bg-card'
  }
}

function metricName(metric: CodexRadarMetric): string {
  const family = metric.family
    ? metric.family.charAt(0).toUpperCase() + metric.family.slice(1)
    : metric.model
  return `${family} ${metric.reasoning_effort}`
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
  const updatedAt = query.data?.updated_at
    ? dayjs(query.data.updated_at).format('M/D HH:mm')
    : ''

  return (
    <PanelWrapper
      title={
        <span className='flex items-center gap-2'>
          <BrainCircuit
            className='text-muted-foreground/60 size-4'
            aria-hidden='true'
          />
          {t('dashboard.overview.codexRadar.title')}
        </span>
      }
      description={t('dashboard.overview.codexRadar.description')}
      loading={query.isLoading}
      empty={query.isError || metrics.length === 0}
      emptyMessage={t('dashboard.overview.codexRadar.unavailable')}
      height='min-h-80'
      contentClassName='p-0'
    >
      <div className='flex min-h-80 flex-col'>
        <div className='bg-border grid min-h-0 flex-1 grid-cols-2 gap-px sm:grid-cols-3 lg:grid-cols-5'>
          {metrics.map((metric) => (
            <div
              key={metric.key}
              className={cn(
                'relative flex min-h-24 min-w-0 flex-col justify-between overflow-hidden px-3 py-3 sm:px-4',
                familySurfaceClassName(metric.family)
              )}
              aria-label={`${metricName(metric)}, ${t('dashboard.overview.codexRadar.iq')} ${metric.score.toFixed(1)}, ${t('dashboard.overview.codexRadar.cost')} $${metric.average_cost_usd.toFixed(1)}, ${t('dashboard.overview.codexRadar.duration')} ${Math.max(1, Math.round(metric.average_task_seconds / 60))}`}
            >
              <span
                className={cn(
                  'absolute inset-x-0 top-0 h-0.5',
                  familyAccentClassName(metric.family)
                )}
                aria-hidden='true'
              />
              <div className='flex min-w-0 items-start justify-between gap-2'>
                <span
                  className='truncate text-xs font-semibold sm:text-sm'
                  title={metric.label}
                >
                  {metricName(metric)}
                </span>
                <span
                  className='text-muted-foreground shrink-0 text-xs font-medium tabular-nums sm:text-sm'
                  title={t('dashboard.overview.codexRadar.cost')}
                >
                  ${metric.average_cost_usd.toFixed(1)}
                </span>
              </div>

              <div className='mt-3 flex items-end justify-between gap-2'>
                <span
                  className={cn(
                    'text-2xl leading-none font-semibold tabular-nums sm:text-3xl',
                    scoreClassName(metric.status)
                  )}
                  title={t('dashboard.overview.codexRadar.iq')}
                >
                  {metric.score.toFixed(1)}
                </span>
                <span
                  className='text-muted-foreground shrink-0 text-xs tabular-nums sm:text-sm'
                  title={t('dashboard.overview.codexRadar.duration')}
                >
                  {t('dashboard.overview.codexRadar.minutes', {
                    count: Math.max(
                      1,
                      Math.round(metric.average_task_seconds / 60)
                    ),
                  })}
                </span>
              </div>
            </div>
          ))}
        </div>

        <div className='bg-muted/20 text-muted-foreground flex flex-wrap items-center justify-between gap-2 border-t px-4 py-2 text-[11px] sm:px-5'>
          <span>
            {updatedAt &&
              t('dashboard.overview.codexRadar.updatedAt', {
                time: updatedAt,
              })}
            {query.data?.stale && (
              <span className='text-warning ml-1'>
                {t('dashboard.overview.codexRadar.cached')}
              </span>
            )}
          </span>
          <a
            href={query.data?.source_url ?? 'https://codexradar.com/'}
            target='_blank'
            rel='noreferrer'
            className='hover:text-foreground focus-visible:ring-ring inline-flex items-center gap-1 rounded-sm outline-none focus-visible:ring-2'
          >
            {query.data?.attribution ??
              t('dashboard.overview.codexRadar.attribution')}
            <ExternalLink className='size-3' aria-hidden='true' />
          </a>
        </div>
      </div>
    </PanelWrapper>
  )
}
