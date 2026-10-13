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
import { TriangleAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import type { CodexRadarMetric } from '../../types'

interface CodexRadarMetricCardProps {
  family: string
  familyLabel: string
  effort: string
  metric?: CodexRadarMetric
}

export function CodexRadarMetricCard(props: CodexRadarMetricCardProps) {
  const { t } = useTranslation()
  const metric = props.metric
  const hasScore = metric?.score != null
  const scoreText =
    metric?.score == null
      ? t('dashboard.overview.codexRadar.pending')
      : metric.score.toFixed(1)
  const scorePercent =
    metric?.score != null && metric.score_max > 0
      ? Math.min(100, Math.max(0, (metric.score / metric.score_max) * 100))
      : null
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

  return (
    <article
      className='codex-radar-family codex-radar-card @container/radar-card flex min-h-44 min-w-0 flex-col rounded-lg border'
      data-family={props.family}
      aria-label={`${props.familyLabel} ${props.effort}`}
    >
      <header className='flex flex-wrap items-center justify-between gap-x-2 gap-y-1 px-3 pt-3'>
        <span className='codex-radar-effort rounded-sm px-1.5 py-0.5 text-xs font-semibold'>
          {props.effort}
        </span>
        {metric && (
          <span className='text-muted-foreground text-xs tabular-nums'>
            {t('dashboard.overview.codexRadar.gradedRuns', {
              count: metric.graded_runs_count,
            })}
          </span>
        )}
      </header>

      <div className='grid flex-1 grid-cols-[minmax(0,1fr)_auto] gap-2 px-3 pt-3 pb-2.5'>
        <div className='min-w-0 self-center'>
          <p className='text-muted-foreground mb-1.5 text-xs'>
            {t('dashboard.overview.codexRadar.iq')}
          </p>
          <p
            className={cn(
              hasScore
                ? 'codex-radar-accent codex-radar-score leading-none font-semibold tabular-nums'
                : 'text-muted-foreground text-xs leading-5'
            )}
          >
            {scoreText}
          </p>
        </div>
        <dl className='codex-radar-stat-divider space-y-2 border-l pl-2.5 text-right text-xs'>
          <div>
            <dt className='text-muted-foreground'>
              {t('dashboard.overview.codexRadar.cost')}
            </dt>
            <dd className='codex-radar-accent mt-0.5 text-sm font-semibold tabular-nums'>
              {costText}
            </dd>
          </div>
          <div>
            <dt className='text-muted-foreground'>
              {t('dashboard.overview.codexRadar.duration')}
            </dt>
            <dd className='mt-0.5 font-medium tabular-nums'>{durationText}</dd>
          </div>
        </dl>
      </div>

      <div className='px-3 pb-3'>
        <div
          className='codex-radar-score-track mb-2 h-1.5 overflow-hidden rounded-full'
          aria-hidden='true'
        >
          {scorePercent != null && (
            <div
              className='codex-radar-marker h-full rounded-full'
              data-radar-score-bar=''
              style={{ width: `${scorePercent}%` }}
            />
          )}
        </div>
        <div className='flex flex-wrap items-center justify-between gap-x-2 gap-y-1 text-xs'>
          {metric && (
            <span className='text-muted-foreground tabular-nums'>
              {t('dashboard.overview.codexRadar.coverage', {
                count: metric.coverage,
                total: metric.tasks,
              })}
            </span>
          )}
          {metric?.insufficient_data && (
            <span
              className='codex-radar-warning inline-flex items-center gap-1'
              title={t('dashboard.overview.codexRadar.insufficientDataDetail')}
            >
              <TriangleAlert className='size-3 shrink-0' aria-hidden='true' />
              {t('dashboard.overview.codexRadar.insufficientData')}
            </span>
          )}
        </div>
      </div>
    </article>
  )
}
