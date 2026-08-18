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
import { useEffect, useMemo } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ArrowRight, CreditCard } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useAuthStore } from '@/stores/auth-store'
import { getSelf } from '@/lib/api'
import { getCurrencyLabel, isCurrencyDisplayEnabled } from '@/lib/currency'
import { formatNumber, formatQuota } from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
import { useStatus } from '@/hooks/use-status'
import { Button } from '@/components/ui/button'
import { StaggerContainer, StaggerItem } from '@/components/page-transition'
import { getUserQuotaDates } from '@/features/dashboard/api'
import { useSummaryCardsConfig } from '@/features/dashboard/hooks/use-dashboard-config'
import type { QuotaDataItem } from '@/features/dashboard/types'
import { StatCard } from '../ui/stat-card'

const SUMMARY_SPARKLINE_BUCKETS = 12

type SummarySparklineKey = 'balance' | 'usage' | 'requests'

function displayPoolGroupName(group?: string): string {
  const trimmed = group?.trim()
  return trimmed || 'default'
}

function getBucketIndex(
  timestamp: number,
  start: number,
  end: number,
  bucketCount: number
): number {
  if (end <= start) return 0
  const ratio = (timestamp - start) / (end - start)
  return Math.min(bucketCount - 1, Math.max(0, Math.floor(ratio * bucketCount)))
}

function buildSummarySparklines(
  data: QuotaDataItem[],
  currentBalance: number,
  start: number,
  end: number
): Record<SummarySparklineKey, number[]> {
  const usage = Array.from({ length: SUMMARY_SPARKLINE_BUCKETS }, () => 0)
  const requests = Array.from({ length: SUMMARY_SPARKLINE_BUCKETS }, () => 0)

  for (const item of data) {
    const timestamp = Number(item.created_at) || start
    const index = getBucketIndex(
      timestamp,
      start,
      end,
      SUMMARY_SPARKLINE_BUCKETS
    )
    usage[index] += Number(item.quota) || 0
    requests[index] += Number(item.count) || 0
  }

  let balance = currentBalance
  const balanceTrend = Array.from(
    { length: SUMMARY_SPARKLINE_BUCKETS },
    () => 0
  )

  for (let index = SUMMARY_SPARKLINE_BUCKETS - 1; index >= 0; index--) {
    balanceTrend[index] = Math.max(0, balance)
    balance += usage[index]
  }

  return {
    balance: balanceTrend,
    usage,
    requests,
  }
}

export function SummaryCards() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const setUser = useAuthStore((state) => state.auth.setUser)
  const { status, loading } = useStatus()

  const summaryTimeRange = useMemo(() => computeTimeRange(1), [])

  const selfQuery = useQuery({
    queryKey: ['dashboard', 'overview', 'self-quota'],
    queryFn: getSelf,
    staleTime: 30 * 1000,
    refetchInterval: 60 * 1000,
  })

  useEffect(() => {
    if (selfQuery.data?.success && selfQuery.data?.data) {
      setUser(selfQuery.data.data)
    }
  }, [selfQuery.data, setUser])

  const liveUser =
    selfQuery.data?.success && selfQuery.data?.data ? selfQuery.data.data : user

  const displayedRemainQuota = Number(liveUser?.quota ?? 0)
  const dailyPoolQuota = Number(status?.daily_quota_pool?.remaining_quota ?? 0)
  const dailyPoolQuotaPerUSD = Number(
    status?.daily_quota_pool?.quota_per_usd ?? 0
  )
  const hasDailyPoolQuota =
    status?.daily_quota_pool != null && Number.isFinite(dailyPoolQuota)
  const quotaPoolBreakdown = useMemo(() => {
    const breakdown = status?.daily_quota_pool?.group_breakdown ?? []
    const toQuota = (usd: unknown) => {
      const value = Number(usd)
      return Number.isFinite(value) && dailyPoolQuotaPerUSD > 0
        ? Math.max(0, Math.round(value * dailyPoolQuotaPerUSD))
        : 0
    }

    return breakdown.flatMap((item) => {
      const group = displayPoolGroupName(item.group)
      const reserveQuota = Number.isFinite(
        Number(item.reserve_bucket_remaining_quota)
      )
        ? Number(item.reserve_bucket_remaining_quota)
        : toQuota(item.reserve_bucket_remaining_usd)
      const reserveConfigured =
        item.reserve_configured !== false &&
        (Boolean(item.reserve_configured) ||
          Number(item.reserve_total_usd ?? 0) > 0 ||
          Number(item.reserve_bucket_remaining_usd ?? 0) > 0)
      const totalRemainingQuota = Number(item.remaining_quota ?? 0)
      const normalQuota = Number.isFinite(Number(item.normal_remaining_quota))
        ? Number(item.normal_remaining_quota)
        : Number.isFinite(Number(item.normal_remaining_usd))
          ? toQuota(item.normal_remaining_usd)
          : item.reserve_active === true && reserveConfigured
            ? Math.max(0, totalRemainingQuota - reserveQuota)
            : totalRemainingQuota
      const showNormalQuota = normalQuota > 0 || !reserveConfigured

      return [
        ...(showNormalQuota
          ? [
              {
                key: `${group}-normal`,
                group: `${group} · ${t('Standard daily budget')}`,
                quota: Math.max(0, normalQuota),
                estimated: Boolean(item.estimated),
                kind: 'normal' as const,
              },
            ]
          : []),
        ...(reserveConfigured
          ? [
              {
                key: `${group}-luna-reserve`,
                group: `${group} · ${t('Luna reserve')}`,
                quota: Math.max(0, reserveQuota),
                estimated: Boolean(item.estimated),
                kind: 'luna' as const,
              },
            ]
          : []),
      ]
    })
  }, [dailyPoolQuotaPerUSD, status?.daily_quota_pool?.group_breakdown, t])

  const usageTrendQuery = useQuery({
    queryKey: [
      'dashboard',
      'overview',
      'summary-sparklines',
      summaryTimeRange.start_timestamp,
      summaryTimeRange.end_timestamp,
    ],
    queryFn: async () =>
      getUserQuotaDates({
        start_timestamp: summaryTimeRange.start_timestamp,
        end_timestamp: summaryTimeRange.end_timestamp,
        default_time: 'hour',
      }),
    staleTime: 60 * 1000,
  })

  const summaryValues = useMemo(() => {
    const usedQuota = Number(liveUser?.used_quota ?? 0)
    const requestCount = Number(liveUser?.request_count ?? 0)

    return {
      remainDisplay: formatQuota(displayedRemainQuota),
      dailyPoolDisplay: hasDailyPoolQuota
        ? `${status?.daily_quota_pool?.estimated ? '≈ ' : ''}${formatQuota(dailyPoolQuota)}`
        : '--',
      usedDisplay: formatQuota(usedQuota),
      requestCountDisplay: formatNumber(requestCount),
    }
  }, [
    dailyPoolQuota,
    displayedRemainQuota,
    hasDailyPoolQuota,
    liveUser,
    status?.daily_quota_pool?.estimated,
  ])

  const currencyEnabledFromStore = isCurrencyDisplayEnabled()
  const statusCurrencyFlag =
    typeof status?.display_in_currency === 'boolean'
      ? Boolean(status.display_in_currency)
      : undefined
  const currencyEnabled =
    statusCurrencyFlag !== undefined
      ? statusCurrencyFlag
      : currencyEnabledFromStore
  const currencyLabel = currencyEnabled ? getCurrencyLabel() : 'Tokens'

  const sparklineData = useMemo(
    () =>
      buildSummarySparklines(
        usageTrendQuery.data?.data ?? [],
        displayedRemainQuota,
        summaryTimeRange.start_timestamp,
        summaryTimeRange.end_timestamp
      ),
    [
      summaryTimeRange.end_timestamp,
      summaryTimeRange.start_timestamp,
      usageTrendQuery.data?.data,
      displayedRemainQuota,
    ]
  )

  const items = useSummaryCardsConfig({
    ...summaryValues,
    currencyEnabled,
    currencyLabel,
  }).map((config, index) => {
    const tones = ['rose', 'teal', 'gray'] as const

    return {
      title: config.title,
      value: config.value,
      desc: config.description,
      icon: config.icon,
      tone: tones[index] ?? 'gray',
      sparkline:
        config.key === 'balance'
          ? sparklineData.balance
          : config.key === 'usage'
            ? sparklineData.usage
            : sparklineData.requests,
    }
  })

  return (
    <div className='bg-card overflow-hidden rounded-2xl border shadow-xs'>
      <div className='grid xl:grid-cols-[minmax(0,1fr)_19rem]'>
        <div className='flex flex-col gap-3 p-4 sm:p-5'>
          <div className='flex flex-wrap items-start justify-between gap-3'>
            <div className='flex flex-col gap-1'>
              <h3 className='text-base font-semibold'>
                {t('Usage at a glance')}
              </h3>
              <p className='text-muted-foreground text-sm'>
                {t(
                  "Monitor today's personal quota, total pool, usage, and requests"
                )}
              </p>
            </div>
          </div>
          <StaggerContainer className='grid gap-3 md:grid-cols-3'>
            {items.map((it) => (
              <StaggerItem
                key={it.title}
                className='bg-background/60 rounded-xl border p-3'
              >
                <StatCard
                  title={it.title}
                  value={it.value}
                  description={it.desc}
                  icon={it.icon}
                  tone={it.tone}
                  sparkline={it.sparkline}
                  loading={loading}
                />
              </StaggerItem>
            ))}
          </StaggerContainer>
        </div>

        <div className='bg-warning/10 flex flex-col justify-between gap-5 border-t p-4 sm:p-5 xl:border-t-0 xl:border-l'>
          <div className='flex flex-col gap-2'>
            <div className='text-muted-foreground text-sm'>
              {t("Today's total pool remaining")}
            </div>
            <div className='flex items-center gap-2'>
              <span className='font-mono text-2xl font-semibold tracking-tight'>
                {summaryValues.dailyPoolDisplay}
              </span>
              <CreditCard
                className='text-muted-foreground size-4'
                aria-hidden='true'
              />
            </div>
            <p className='text-muted-foreground text-sm leading-relaxed'>
              {currencyEnabled
                ? `${t('Displayed in')} ${currencyLabel}`
                : t('Balance is shown in quota units')}
            </p>
            {quotaPoolBreakdown.length > 0 && (
              <div className='mt-1 flex flex-col gap-1.5'>
                <div className='text-muted-foreground/70 text-xs font-medium tracking-wide uppercase'>
                  {t('Daily quota pools')}
                </div>
                {quotaPoolBreakdown.map((item) => (
                  <div
                    key={item.key}
                    className={`flex items-center justify-between gap-2 rounded-md border px-2.5 py-1.5 text-xs ${
                      item.kind === 'luna'
                        ? 'border-violet-500/30 bg-violet-500/10 text-violet-700 dark:text-violet-300'
                        : 'bg-background/70 border-border/60'
                    }`}
                  >
                    <span className='truncate font-medium'>{item.group}</span>
                    <span className='shrink-0 font-mono tabular-nums'>
                      {item.estimated ? '≈ ' : ''}
                      {formatQuota(item.quota)}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
          <Button className='justify-between' render={<Link to='/wallet' />}>
            <span>{t('Recharge')}</span>
            <ArrowRight data-icon='inline-end' />
          </Button>
        </div>
      </div>
    </div>
  )
}
