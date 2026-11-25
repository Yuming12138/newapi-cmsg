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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { getDashboardProviderBalances } from '@/features/dashboard/api'
import type { ProviderAccountBalance } from '@/features/dashboard/types'

type BalanceRowProps = {
  name: string
  caption: string
  amount: number | null | undefined
  currency: 'USD' | 'credits'
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
      ? props.currency === 'credits'
        ? new Intl.NumberFormat(i18n.language, {
            minimumFractionDigits: 0,
            maximumFractionDigits: 0,
          }).format(props.amount)
        : new Intl.NumberFormat(i18n.language, {
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

export function UpstreamBalancesPanel() {
  const { t } = useTranslation()
  const balancesQuery = useQuery({
    queryKey: ['dashboard', 'provider-balances'],
    queryFn: getDashboardProviderBalances,
    staleTime: 60 * 1000,
    refetchInterval: 5 * 60 * 1000,
  })
  const asxs = balancesQuery.data?.asxs
  const c2a = balancesQuery.data?.c2a
  const pool = balancesQuery.data?.daily_quota_pool
  const noChannel = t('No active channel')
  const waiting = t('Waiting for balance sync')
  const unavailable = t('Balance temporarily unavailable')

  return (
    <div className='bg-warning/10 flex flex-col gap-3 border-t p-4 sm:p-5 xl:border-t-0 xl:border-l'>
      <div>
        <h3 className='text-sm font-semibold'>{t('Upstream balances')}</h3>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t('ASXS shared quota and official model account balances')}
        </p>
      </div>
      <div className='grid gap-2'>
        {pool && (
          <BalanceRow
            name={t('Total available pool')}
            caption={
              pool.estimated
                ? t('Includes estimated subscription quota')
                : t('Shared upstream balance')
            }
            amount={pool.remaining_usd}
            currency='USD'
            updatedAt={pool.updated_at}
            unavailableText={unavailable}
            partial={pool.partial}
            loading={balancesQuery.isPending}
            tone='text-foreground'
          />
        )}
        <BalanceRow
          name='ASXS'
          caption={
            asxs?.source === 'asxs_usage'
              ? t("Today's shared quota")
              : t('Total daily subscription balance')
          }
          amount={asxs?.balance}
          currency='USD'
          updatedAt={asxs?.updated_at}
          unavailableText={unavailable}
          partial={asxs?.partial}
          loading={balancesQuery.isPending}
          tone='text-amber-700 dark:text-amber-300'
        />
        {c2a && (
          <BalanceRow
            name='C2A'
            caption={t('Upstream account credits total')}
            amount={c2a.balance}
            currency='credits'
            updatedAt={c2a.updated_at}
            unavailableText={
              balancesQuery.isError
                ? unavailable
                : providerUnavailableText(c2a, noChannel, waiting)
            }
            partial={c2a.partial}
            loading={balancesQuery.isPending}
            tone='text-blue-700 dark:text-blue-300'
          />
        )}
      </div>
    </div>
  )
}
