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
import { BarChart3, ExternalLink } from 'lucide-react'
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

export interface IntelligenceRankingProps {
  models: PricingModel[]
  onModelClick: (modelName: string) => void
  className?: string
}

/** Height of the tallest bar, in px. Bars scale from 0 so heights stay honest. */
const CHART_HEIGHT = 156
/** Room reserved for the diagonal model name, in px. */
const LABEL_HEIGHT = 104

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

      <div className='flex items-end gap-1 overflow-x-auto pb-0.5 sm:gap-2'>
        {ranked.map((entry) => {
          const model = modelMap.get(entry.ourModel)
          const icon = model?.vendor_icon
            ? getLobeIcon(model.vendor_icon, 12)
            : null
          // Keep a sliver visible for very low scores; scale starts at 0.
          const heightPct = Math.max((entry.score / maxScore) * 100, 2)
          return (
            <button
              key={entry.ourModel}
              type='button'
              onClick={() => props.onModelClick(entry.ourModel)}
              className='group flex min-w-[52px] flex-1 basis-0 flex-col items-center'
              title={`${entry.ourModel} · #${entry.rank} · ${t('Index')} ${entry.score.toFixed(1)}`}
            >
              <span className='text-[11px] font-semibold tabular-nums'>
                {entry.score.toFixed(1)}
              </span>
              <span className='text-muted-foreground/50 text-[10px] tabular-nums'>
                #{entry.rank}
              </span>
              <div
                className='mt-1 flex w-full items-end justify-center'
                style={{ height: CHART_HEIGHT }}
              >
                <div
                  className='bg-primary/80 group-hover:bg-primary w-8 rounded-t-[4px] transition-colors sm:w-10'
                  style={{ height: `${heightPct}%` }}
                />
              </div>
              <div
                className='border-border/60 mt-1 h-px w-full border-t'
                aria-hidden='true'
              />
              <div className='mt-1 h-3'>{icon}</div>
              <div
                className='relative w-full'
                style={{ height: LABEL_HEIGHT }}
                title={entry.ourModel}
              >
                <span className='text-muted-foreground group-hover:text-foreground absolute top-1 left-1/2 origin-top-left rotate-[55deg] text-[11px] leading-none tracking-tight whitespace-nowrap transition-colors'>
                  {entry.ourModel}
                </span>
              </div>
            </button>
          )
        })}
      </div>

      <p className='text-muted-foreground/50 mt-2 text-[11px]'>
        {t('Ranked by Artificial Analysis Intelligence Index')} ·{' '}
        {t('{{total}} models tracked', { total: INTELLIGENCE_LEADERBOARD_SIZE })}{' '}
        · {t('Snapshot')}: {INTELLIGENCE_SNAPSHOT_DATE} ·{' '}
        {t('Only models available on this site are shown')}
      </p>
    </section>
  )
}
