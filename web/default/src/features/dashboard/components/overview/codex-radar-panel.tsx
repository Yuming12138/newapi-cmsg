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
import { useMemo } from 'react'
import dayjs from 'dayjs'
import { useQuery } from '@tanstack/react-query'
import { BrainCircuit, ExternalLink } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { getCodexRadarOverview } from '../../api'
import type { CodexRadarMetric } from '../../types'
import { PanelWrapper } from '../ui/panel-wrapper'

const REFRESH_INTERVAL_MS = 15 * 60 * 1000
const DISPLAY_METRIC_KEYS = [
  'gpt_56_sol_max',
  'gpt_56_sol_medium',
  'gpt_56_terra_max',
  'gpt_56_terra_high',
  'gpt_56_luna_max',
  'gpt_56_luna_high',
]

function selectDisplayMetrics(metrics: CodexRadarMetric[]): CodexRadarMetric[] {
  const byKey = new Map(metrics.map((metric) => [metric.key, metric]))
  const selected = DISPLAY_METRIC_KEYS.map((key) => byKey.get(key)).filter(
    (metric): metric is CodexRadarMetric => metric != null
  )

  if (selected.length >= DISPLAY_METRIC_KEYS.length) {
    return selected
  }

  const selectedKeys = new Set(selected.map((metric) => metric.key))
  for (const metric of metrics) {
    if (selectedKeys.has(metric.key)) continue
    selected.push(metric)
    selectedKeys.add(metric.key)
    if (selected.length >= DISPLAY_METRIC_KEYS.length) break
  }
  return selected
}

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
  const metrics = useMemo(
    () => selectDisplayMetrics(query.data?.metrics ?? []),
    [query.data?.metrics]
  )
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
      height='h-80'
      contentClassName='p-0'
    >
      <div className='flex h-80 flex-col'>
        <div className='text-muted-foreground grid grid-cols-[minmax(6rem,1fr)_3.5rem_4rem_4.5rem] items-center border-b px-4 py-2 text-[11px] font-medium sm:px-5'>
          <span className='truncate'>
            {t('dashboard.overview.codexRadar.model')}
          </span>
          <span
            className='truncate text-right'
            title={t('dashboard.overview.codexRadar.iq')}
          >
            {t('dashboard.overview.codexRadar.iq')}
          </span>
          <span
            className='truncate text-right'
            title={t('dashboard.overview.codexRadar.cost')}
          >
            {t('dashboard.overview.codexRadar.cost')}
          </span>
          <span
            className='truncate text-right'
            title={t('dashboard.overview.codexRadar.duration')}
          >
            {t('dashboard.overview.codexRadar.duration')}
          </span>
        </div>

        <div className='min-h-0 flex-1'>
          {metrics.map((metric) => (
            <div
              key={metric.key}
              className='border-border/60 grid min-h-9 grid-cols-[minmax(6rem,1fr)_3.5rem_4rem_4.5rem] items-center border-b px-4 py-2 text-xs last:border-b-0 sm:px-5'
            >
              <span className='truncate font-medium' title={metric.label}>
                {metricName(metric)}
              </span>
              <span
                className={cn(
                  'text-right text-sm font-semibold tabular-nums',
                  scoreClassName(metric.status)
                )}
              >
                {metric.score.toFixed(1)}
              </span>
              <span className='text-muted-foreground text-right tabular-nums'>
                ${metric.average_cost_usd.toFixed(1)}
              </span>
              <span className='text-muted-foreground text-right tabular-nums'>
                {t('dashboard.overview.codexRadar.minutes', {
                  count: Math.max(
                    1,
                    Math.round(metric.average_task_seconds / 60)
                  ),
                })}
              </span>
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
