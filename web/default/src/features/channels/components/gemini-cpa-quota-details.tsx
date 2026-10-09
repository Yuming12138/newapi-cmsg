import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatQuota, formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Progress } from '@/components/ui/progress'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { dotColorMap, textColorMap } from '@/components/status-badge'
import { handleUpdateChannelBalance } from '../lib/channel-actions'
import { getBalanceVariant } from '../lib/channel-utils'
import {
  parseGeminiCPAQuotaMeta,
  type GeminiCPAAccount,
  type GeminiCPAModelQuota,
  type GeminiCPAQuotaMeta,
} from '../lib/gemini-cpa-quota'
import type { Channel } from '../types'
import { useChannels } from './channels-provider'

const DETAILS_CLASS =
  'bg-background text-foreground border-border max-w-none flex-col items-stretch border shadow-lg [&>svg]:bg-background [&>svg]:fill-background'

function formatPercent(value: number | null): string {
  return value == null ? '-' : `${value.toFixed(2)}%`
}

function GeminiQuotaRow(props: { quota: GeminiCPAModelQuota }) {
  const { t } = useTranslation()
  const quota = props.quota
  const value = quota.remainingPercent
  let progressColor = '[&_[data-slot=progress-indicator]]:bg-emerald-500'
  if (value != null && value <= 5)
    progressColor = '[&_[data-slot=progress-indicator]]:bg-red-500'
  else if (value != null && value <= 20)
    progressColor = '[&_[data-slot=progress-indicator]]:bg-amber-500'

  return (
    <div className='space-y-1.5'>
      <div className='flex items-start justify-between gap-3 text-xs'>
        <span className='min-w-0 break-all'>{quota.model}</span>
        <span className='shrink-0 font-semibold tabular-nums'>
          {value == null
            ? t('geminiQuotaDetails.notProvided')
            : formatPercent(value)}
        </span>
      </div>
      {value != null && (
        <Progress
          value={value}
          aria-label={quota.model}
          className={cn('[&_[data-slot=progress-track]]:h-1.5', progressColor)}
        />
      )}
      {quota.resetAt != null && (
        <p className='text-muted-foreground text-xs tabular-nums'>
          {t('geminiQuotaDetails.resetsAt', {
            time: formatTimestampToDate(quota.resetAt),
          })}
        </p>
      )}
    </div>
  )
}

function GeminiAccountDetails(props: { account: GeminiCPAAccount }) {
  const { t } = useTranslation()
  const account = props.account
  let status = t('geminiQuotaDetails.quotaAvailable')
  if (account.error) status = t('geminiQuotaDetails.syncFailed')
  else if (account.runtimeUnavailable)
    status = t('geminiQuotaDetails.runtimeUnavailable')
  else if (
    !account.models.some(
      (quota) => quota.remainingPercent != null && quota.remainingPercent > 0
    )
  )
    status = t('geminiQuotaDetails.exhausted')

  return (
    <section className='border-border space-y-3 border-t pt-3'>
      <div className='flex items-start justify-between gap-3'>
        <div>
          <p className='text-xs font-semibold'>
            {t('geminiQuotaDetails.account', { id: account.id.slice(-6) })}
          </p>
          <p className='text-muted-foreground mt-1 text-xs'>
            {account.tier || '-'} · {status}
          </p>
        </div>
        <span className='text-xs font-semibold tabular-nums'>
          {formatPercent(account.remainingPercent)}
        </span>
      </div>
      {account.error && (
        <p className='text-destructive text-xs break-words'>{account.error}</p>
      )}
      {account.models.map((quota) => (
        <GeminiQuotaRow key={quota.model} quota={quota} />
      ))}
    </section>
  )
}

function GeminiCPAQuotaDetails(props: { meta: GeminiCPAQuotaMeta | null }) {
  const { t } = useTranslation()
  const meta = props.meta
  if (!meta)
    return <p className='text-xs'>{t('geminiQuotaDetails.notCollected')}</p>
  return (
    <div
      tabIndex={0}
      role='region'
      aria-label={t('geminiQuotaDetails.title')}
      className='max-h-[calc(100dvh-6rem)] w-[400px] max-w-[calc(100vw-3rem)] space-y-3 overflow-y-auto p-1 text-left focus-visible:ring-2 focus-visible:outline-none'
    >
      <div className='flex items-start justify-between gap-4'>
        <div>
          <p className='text-sm font-semibold'>
            {t('geminiQuotaDetails.title')}
          </p>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t('geminiQuotaDetails.accounts', {
              available: meta.availableAccountCount ?? '-',
              total: meta.accountCount ?? '-',
            })}
            {' · '}
            {t('geminiQuotaDetails.credentials', {
              count: meta.credentialCount ?? '-',
            })}
          </p>
        </div>
        <div className='shrink-0 text-right'>
          <p className='text-sm font-semibold tabular-nums'>
            {formatPercent(meta.remainingPercent)}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t('geminiQuotaDetails.geminiAverage')}
          </p>
        </div>
      </div>
      <p className='text-muted-foreground text-xs leading-relaxed'>
        {t('geminiQuotaDetails.percentNote')}
      </p>
      {(meta.partial || meta.error) && (
        <div className='flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 p-2 text-xs text-amber-700 dark:text-amber-300'>
          <AlertTriangle className='size-4 shrink-0' aria-hidden='true' />
          <span className='break-words'>
            {meta.error || t('geminiQuotaDetails.partial')}
          </span>
        </div>
      )}
      {meta.accounts.map((account) => (
        <GeminiAccountDetails key={account.id} account={account} />
      ))}
      {meta.updatedAt != null && meta.updatedAt > 0 && (
        <p className='text-muted-foreground border-border border-t pt-2 text-xs tabular-nums'>
          {t('geminiQuotaDetails.updatedAt', {
            time: formatTimestampToDate(meta.updatedAt),
          })}
        </p>
      )}
    </div>
  )
}

export function GeminiCPABalanceCell(props: { channel: Channel }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { sensitiveVisible } = useChannels()
  const [updating, setUpdating] = useState(false)
  const meta = parseGeminiCPAQuotaMeta(props.channel.other_info)
  const remaining = meta?.remainingPercent ?? null
  const variant = remaining == null ? 'neutral' : getBalanceVariant(remaining)
  const displayed = sensitiveVisible ? formatPercent(remaining) : '******'
  const update = async () => {
    if (updating) return
    setUpdating(true)
    try {
      await handleUpdateChannelBalance(props.channel.id, queryClient)
    } finally {
      setUpdating(false)
    }
  }
  const summary = t('geminiQuotaDetails.accounts', {
    available: meta?.availableAccountCount ?? '-',
    total: meta?.accountCount ?? '-',
  })

  return (
    <TooltipProvider>
      <div className='flex flex-col items-start gap-1 text-xs'>
        <div className='flex items-center gap-1.5'>
          <span
            className={cn(
              'size-1.5 shrink-0 rounded-full',
              dotColorMap[updating ? 'neutral' : variant]
            )}
            aria-hidden='true'
          />
          <span className='text-muted-foreground'>
            {sensitiveVisible
              ? formatQuota(props.channel.used_quota || 0)
              : '******'}
          </span>
          <span className='text-muted-foreground/30'>·</span>
          <Tooltip>
            <TooltipTrigger
              render={
                <button
                  type='button'
                  onClick={update}
                  disabled={updating}
                  className={cn(
                    'min-h-6 cursor-pointer rounded-sm font-medium transition-opacity hover:opacity-70 focus-visible:ring-2 focus-visible:outline-none disabled:cursor-wait',
                    textColorMap[variant]
                  )}
                  aria-label={t('Click to update balance')}
                />
              }
            >
              {updating ? t('geminiQuotaDetails.updating') : displayed}
            </TooltipTrigger>
            <TooltipContent className={DETAILS_CLASS}>
              {sensitiveVisible && <GeminiCPAQuotaDetails meta={meta} />}
              <p className='text-muted-foreground text-xs'>
                {t('Click to update balance')}
              </p>
            </TooltipContent>
          </Tooltip>
        </div>
        {sensitiveVisible && (
          <Tooltip>
            <TooltipTrigger
              render={
                <button
                  type='button'
                  className='text-muted-foreground min-h-6 cursor-help rounded-sm text-left text-xs focus-visible:ring-2 focus-visible:outline-none'
                />
              }
            >
              Gemini · {summary}
              {(meta?.partial || meta?.error) && (
                <span className='text-amber-700 dark:text-amber-300'>
                  {' '}
                  · {t('geminiQuotaDetails.partialLabel')}
                </span>
              )}
            </TooltipTrigger>
            <TooltipContent className={DETAILS_CLASS}>
              <GeminiCPAQuotaDetails meta={meta} />
            </TooltipContent>
          </Tooltip>
        )}
      </div>
    </TooltipProvider>
  )
}
