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
import { BarChart3, ChevronRight, ExternalLink } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn } from '@/lib/utils'
import {
  getRankedOurModels,
  INTELLIGENCE_LEADERBOARD,
  INTELLIGENCE_LEADERBOARD_SIZE,
  INTELLIGENCE_SNAPSHOT_DATE,
  INTELLIGENCE_SOURCE_NAME,
  INTELLIGENCE_SOURCE_URL,
} from '../lib/intelligence-ranking'
import type { PricingModel } from '../types'
import { RankBadge } from './model-details-apps'

export interface IntelligenceRankingProps {
  models: PricingModel[]
  onModelClick: (modelName: string) => void
  className?: string
}

export function IntelligenceRanking(props: IntelligenceRankingProps) {
  const { t } = useTranslation()

  const ranked = useMemo(
    () =>
      getRankedOurModels(
        props.models.map((m) => m.model_name || '').filter(Boolean)
      ),
    [props.models]
  )

  const modelMap = useMemo(() => {
    const map = new Map<string, PricingModel>()
    for (const model of props.models) {
      if (model.model_name) map.set(model.model_name, model)
    }
    return map
  }, [props.models])

  if (ranked.length === 0) return null

  const maxScore = INTELLIGENCE_LEADERBOARD[0]?.score || 1

  return (
    <section
      className={cn('mb-4 rounded-xl border p-3 sm:mb-5 sm:p-4', props.className)}
      aria-label={t('Intelligence Ranking')}
    >
      <div className='mb-3 flex flex-wrap items-center justify-between gap-x-3 gap-y-1.5'>
        <div className='flex min-w-0 items-center gap-2'>
          <BarChart3 className='text-muted-foreground size-4 shrink-0' />
          <h2 className='text-sm font-semibold sm:text-base'>
            {t('Intelligence Ranking')}
          </h2>
          <span className='text-muted-foreground/70 text-xs'>
            {t('{{count}} of our models ranked', { count: ranked.length })}
          </span>
        </div>
        <a
          href={INTELLIGENCE_SOURCE_URL}
          target='_blank'
          rel='noreferrer'
          className='text-muted-foreground hover:text-foreground inline-flex items-center gap-1 text-xs transition-colors'
        >
          {t('Data source')}: {INTELLIGENCE_SOURCE_NAME}
          <ExternalLink className='size-3' />
        </a>
      </div>

      <div className='grid grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4'>
        {ranked.map((entry) => {
          const model = modelMap.get(entry.ourModel)
          const icon = model?.vendor_icon
            ? getLobeIcon(model.vendor_icon, 14)
            : null
          return (
            <button
              key={entry.ourModel}
              type='button'
              onClick={() => props.onModelClick(entry.ourModel)}
              className='group hover:bg-muted/50 hover:border-foreground/20 flex min-w-0 items-center gap-2 rounded-lg border px-2.5 py-2 text-left transition-colors'
              title={t('View model details')}
            >
              <RankBadge rank={entry.rank} />
              <div className='min-w-0 flex-1'>
                <div className='flex items-center gap-1.5'>
                  {icon}
                  <span className='truncate text-xs font-medium'>
                    {model?.model_name || entry.ourModel}
                  </span>
                </div>
                <div className='text-muted-foreground/70 mt-0.5 truncate text-[11px]'>
                  {entry.name}
                </div>
                <div className='bg-muted/60 mt-1.5 h-1 overflow-hidden rounded-full'>
                  <div
                    className='bg-primary/70 h-full rounded-full'
                    style={{ width: `${(entry.score / maxScore) * 100}%` }}
                  />
                </div>
              </div>
              <div className='shrink-0 text-right'>
                <div className='font-mono text-sm font-semibold tabular-nums'>
                  {entry.score.toFixed(1)}
                </div>
                <div className='text-muted-foreground/60 text-[10px]'>
                  {t('Index')}
                </div>
              </div>
              <ChevronRight className='text-muted-foreground/40 group-hover:text-muted-foreground size-3.5 shrink-0 transition-colors' />
            </button>
          )
        })}
      </div>

      <p className='text-muted-foreground/50 mt-2.5 text-[11px]'>
        {t('Ranked by Artificial Analysis Intelligence Index')} ·{' '}
        {t('{{total}} models tracked', { total: INTELLIGENCE_LEADERBOARD_SIZE })}{' '}
        · {t('Snapshot')}: {INTELLIGENCE_SNAPSHOT_DATE} ·{' '}
        {t('Only models available on this site are shown')}
      </p>
    </section>
  )
}
