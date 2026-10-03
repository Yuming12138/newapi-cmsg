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
import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { getDashboardProviderBalances } from '@/features/dashboard/api'
import type {
  KimiSubscriptionBalance,
  ProviderAccountBalance,
} from '@/features/dashboard/types'

type BalanceRowProps = {
  name: string
  caption: string
  amount: number | null | undefined
  currency: 'USD' | 'CNY'
  updatedAt?: number
  unavailableText: string
  partial?: boolean
  loading?: boolean
  tone: string
}

function BalanceRow(props: BalanceRowProps) {
  const { t, i18n } = useTranslation()
  const formattedAmount =
    props.amount != null && Number.isFinite(props.amount)
      ? new Intl.NumberFormat(i18n.language, {
          style: 'currency',
          currency: props.currency,
          minimumFractionDigits: 2,
          maximumFractionDigits: 2,
        }).format(props.amount)
      : '--'
  const updatedAt = props.updatedAt
    ? new Intl.DateTimeFormat(i18n.language, {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }).format(new Date(props.updatedAt * 1000))
    : null
  let syncLabel = props.unavailableText
  if (props.amount != null) {
    if (props.partial) {
      syncLabel = t('Partially synced')
    } else if (updatedAt) {
      syncLabel = t('Updated {{time}}', { time: updatedAt })
    }
  }

  return (
    <div className='bg-background/75 rounded-xl border px-3 py-2.5'>
      <div className='flex items-center justify-between gap-2 text-xs'>
        <span className={`font-semibold ${props.tone}`}>{props.name}</span>
        <span className='text-muted-foreground truncate'>{props.caption}</span>
      </div>
      <div className='mt-1 flex items-end justify-between gap-2'>
        <span className='font-mono text-xl font-semibold tabular-nums'>
          {props.loading ? '···' : formattedAmount}
        </span>
        <span
          className='text-muted-foreground shrink-0 text-[10px]'
          title={updatedAt ?? undefined}
        >
          {syncLabel}
        </span>
      </div>
    </div>
  )
}

function providerUnavailableText(
  provider: ProviderAccountBalance | undefined,
  noChannel: string,
  waiting: string
): string {
  return provider?.channel_count ? waiting : noChannel
}

function subscriptionWindowLabel(name: string): string {
  switch (name) {
    case '5h':
      return '5h'
    case 'week':
    case '7d':
      return '7d'
    case 'month_total':
      return '月度总额'
    case 'month_code':
      return '月度代码'
    default:
      return name
  }
}

function formatKimiResetCountdown(
  resetAt: number | null | undefined,
  nowMs: number
): string | null {
  if (resetAt == null || !Number.isFinite(resetAt) || resetAt <= 0) {
    return null
  }
  const remainingMinutes = Math.max(
    0,
    Math.ceil((resetAt * 1000 - nowMs) / 60_000)
  )
  if (remainingMinutes <= 0) return null
  const hours = Math.floor(remainingMinutes / 60)
  const minutes = remainingMinutes % 60
  if (hours > 0) return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`
  return `${remainingMinutes}m`
}

function KimiSubscriptionRow(props: {
  subscription: KimiSubscriptionBalance
  officialBalance: number | null | undefined
  loading: boolean
}) {
  const { t, i18n } = useTranslation()
  const [nowMs, setNowMs] = useState(() => Date.now())
  useEffect(() => {
    const timer = window.setInterval(() => setNowMs(Date.now()), 30_000)
    return () => window.clearInterval(timer)
  }, [])
  const windows =
    props.subscription.windows.length > 0
      ? props.subscription.windows
      : props.subscription.accounts.length === 1
        ? props.subscription.accounts[0].windows
        : []
  const remaining =
    props.subscription.remaining_percent ??
    (windows.length > 0
      ? Math.min(...windows.map((window) => window.remaining_percent))
      : null)
  const formattedRemaining =
    remaining != null && Number.isFinite(remaining)
      ? `${new Intl.NumberFormat(i18n.language, {
          minimumFractionDigits: 2,
          maximumFractionDigits: 2,
        }).format(remaining)}%`
      : '--'
  const updatedAt = props.subscription.updated_at
    ? new Intl.DateTimeFormat(i18n.language, {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }).format(new Date(props.subscription.updated_at * 1000))
    : null
  const syncLabel = props.subscription.partial
    ? t('Partially synced')
    : updatedAt
      ? t('Updated {{time}}', { time: updatedAt })
      : t('Waiting for balance sync')

  return (
    <div className='bg-background/75 rounded-xl border px-3 py-2.5'>
      <div className='flex items-center justify-between gap-2 text-xs'>
        <span className='font-semibold text-violet-700 dark:text-violet-300'>
          Kimi
        </span>
        <span className='text-muted-foreground truncate'>
          {t('CPA subscription quota')}
        </span>
      </div>
      <div className='mt-1 flex items-end justify-between gap-2'>
        <span className='font-mono text-xl font-semibold tabular-nums'>
          {props.loading ? '···' : formattedRemaining}
        </span>
        <span className='text-muted-foreground shrink-0 text-[10px]'>
          {syncLabel}
        </span>
      </div>
      <div className='text-muted-foreground mt-1 flex flex-wrap gap-x-2 gap-y-0.5 text-[10px]'>
        {windows.map((window) => {
          const resetCountdown =
            window.name === '5h'
              ? formatKimiResetCountdown(window.reset_at, nowMs)
              : null
          const formattedWindowRemaining = new Intl.NumberFormat(
            i18n.language,
            {
              minimumFractionDigits: 1,
              maximumFractionDigits: 1,
            }
          ).format(window.remaining_percent)
          return (
            <span key={window.name}>
              {subscriptionWindowLabel(window.name)} {formattedWindowRemaining}%
              {resetCountdown && (
                <>
                  {' · '}
                  {t('Resets in {{time}}', { time: resetCountdown })}
                </>
              )}
            </span>
          )
        })}
      </div>
      {props.officialBalance != null && (
        <div className='text-muted-foreground mt-1 text-[10px]'>
          {t('Official wallet')}: ¥
          {new Intl.NumberFormat(i18n.language, {
            minimumFractionDigits: 2,
            maximumFractionDigits: 2,
          }).format(props.officialBalance)}
        </div>
      )}
    </div>
  )
}

export function UpstreamBalancesPanel() {
  const { t } = useTranslation()
  const balancesQuery = useQuery({
    queryKey: ['dashboard', 'provider-balances'],
    queryFn: getDashboardProviderBalances,
    staleTime: 60 * 1000,
    refetchInterval: 5 * 60 * 1000,
  })
  const kimi = balancesQuery.data?.kimi
  const deepSeek = balancesQuery.data?.deepseek
  const asxs = balancesQuery.data?.asxs
  const noChannel = t('No active channel')
  const waiting = t('Waiting for balance sync')
  const unavailable = t('Balance temporarily unavailable')

  return (
    <div className='bg-warning/10 flex flex-col gap-3 border-t p-4 sm:p-5 xl:border-t-0 xl:border-l'>
      <div>
        <h3 className='text-sm font-semibold'>{t('Upstream balances')}</h3>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t('ASXS account subscriptions and official model account balances')}
        </p>
      </div>
      <div className='grid gap-2'>
        <BalanceRow
          name='ASXS'
          caption={t('Total daily subscription balance')}
          amount={asxs?.balance}
          currency='USD'
          updatedAt={asxs?.updated_at}
          unavailableText={unavailable}
          partial={asxs?.partial}
          loading={balancesQuery.isPending}
          tone='text-amber-700 dark:text-amber-300'
        />
        {kimi?.subscription ? (
          <KimiSubscriptionRow
            subscription={kimi.subscription}
            officialBalance={kimi.balance}
            loading={balancesQuery.isPending}
          />
        ) : (
          <BalanceRow
            name='Kimi'
            caption={t('Official account balance')}
            amount={kimi?.balance}
            currency='CNY'
            updatedAt={kimi?.updated_at}
            unavailableText={
              balancesQuery.isError
                ? unavailable
                : providerUnavailableText(kimi, noChannel, waiting)
            }
            partial={kimi?.partial}
            loading={balancesQuery.isPending}
            tone='text-violet-700 dark:text-violet-300'
          />
        )}
        <BalanceRow
          name='DeepSeek'
          caption={t('Official account balance')}
          amount={deepSeek?.balance}
          currency='CNY'
          updatedAt={deepSeek?.updated_at}
          unavailableText={
            balancesQuery.isError
              ? unavailable
              : providerUnavailableText(deepSeek, noChannel, waiting)
          }
          partial={deepSeek?.partial}
          loading={balancesQuery.isPending}
          tone='text-cyan-700 dark:text-cyan-300'
        />
      </div>
    </div>
  )
}
