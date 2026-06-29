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
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { VChart } from '@visactor/react-vchart'
import { Loader2, Router, TrendingUp } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  formatCompactNumber,
  formatNumber,
  formatPercent,
  formatQuota,
  quotaUnitsToDollars,
} from '@/lib/format'
import {
  formatChartTime,
  getRollingDateRange,
  type TimeGranularity,
} from '@/lib/time'
import { VCHART_OPTION } from '@/lib/vchart'
import { useThemeCustomization } from '@/context/theme-customization-provider'
import { useTheme } from '@/context/theme-provider'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { getChannelQuotaDates } from '@/features/dashboard/api'
import {
  TIME_GRANULARITY_OPTIONS,
  TIME_RANGE_PRESETS,
} from '@/features/dashboard/constants'
import { getDefaultDays } from '@/features/dashboard/lib'
import type {
  ChannelQuotaDataItem,
  DashboardFilters,
} from '@/features/dashboard/types'

let themeManagerPromise: Promise<
  (typeof import('@visactor/vchart'))['ThemeManager']
> | null = null

interface ChannelUsageTableProps {
  filters?: DashboardFilters
}

interface ChannelUsageRow {
  channelId: number
  channelName: string
  group: string
  quota: number
  count: number
  tokenUsed: number
  share: number
}

const CHANNEL_TIME_RANGE_PRESETS = [
  ...TIME_RANGE_PRESETS,
  { label: '90 Days', days: 90 },
] as const

const TOP_CHANNEL_LIMIT_OPTIONS = [5, 10, 20, 50]

function aggregateChannelUsage(
  data: ChannelQuotaDataItem[]
): ChannelUsageRow[] {
  const rows = new Map<number, ChannelUsageRow>()

  data.forEach((item) => {
    const channelId = Number(item.channel_id) || 0
    if (channelId <= 0) return
    const existing = rows.get(channelId) ?? {
      channelId,
      channelName: item.channel_name || `#${channelId}`,
      group: item.group || '',
      quota: 0,
      count: 0,
      tokenUsed: 0,
      share: 0,
    }
    existing.channelName = item.channel_name || existing.channelName
    existing.group = item.group || existing.group
    existing.quota += Number(item.quota) || 0
    existing.count += Number(item.count) || 0
    existing.tokenUsed += Number(item.token_used) || 0
    rows.set(channelId, existing)
  })

  const totalQuota = Array.from(rows.values()).reduce(
    (sum, row) => sum + row.quota,
    0
  )

  return Array.from(rows.values())
    .map((row) => ({
      ...row,
      share: totalQuota > 0 ? (row.quota / totalQuota) * 100 : 0,
    }))
    .sort((a, b) => b.quota - a.quota || b.count - a.count)
}

function getChannelLabel(item: ChannelQuotaDataItem): string {
  const channelId = Number(item.channel_id) || 0
  return item.channel_name || `#${channelId}`
}

function buildChannelTrendSpec(
  data: ChannelQuotaDataItem[],
  rows: ChannelUsageRow[],
  timeGranularity: TimeGranularity,
  topChannelLimit: number,
  t: (key: string) => string
) {
  const topChannelIds = new Set(
    rows.slice(0, topChannelLimit).map((row) => row.channelId)
  )
  const otherLabel = t('Other')
  const buckets = new Map<string, Map<string, { quota: number }>>()

  data.forEach((item) => {
    const channelId = Number(item.channel_id) || 0
    if (channelId <= 0) return

    const time = formatChartTime(item.created_at, timeGranularity)
    const channel = topChannelIds.has(channelId)
      ? getChannelLabel(item)
      : otherLabel
    const byChannel = buckets.get(time) ?? new Map<string, { quota: number }>()
    const existing = byChannel.get(channel) ?? { quota: 0 }
    existing.quota += Number(item.quota) || 0
    byChannel.set(channel, existing)
    buckets.set(time, byChannel)
  })

  const values = Array.from(buckets.entries())
    .sort(([a], [b]) => a.localeCompare(b))
    .flatMap(([time, byChannel]) =>
      Array.from(byChannel.entries()).map(([channel, value]) => ({
        Time: time,
        Channel: channel,
        Usage: quotaUnitsToDollars(value.quota),
        rawQuota: value.quota,
      }))
    )

  return {
    type: 'bar',
    data: [{ id: 'channelTrend', values }],
    xField: 'Time',
    yField: 'Usage',
    seriesField: 'Channel',
    stack: true,
    legends: { visible: true, selectMode: 'single' },
    tooltip: {
      mark: {
        content: [
          {
            key: (datum: Record<string, unknown>) =>
              String(datum?.Channel ?? ''),
            value: (datum: Record<string, unknown>) =>
              formatQuota(Number(datum?.rawQuota) || 0),
          },
        ],
      },
      dimension: {
        content: [
          {
            key: (datum: Record<string, unknown>) =>
              String(datum?.Channel ?? ''),
            value: (datum: Record<string, unknown>) =>
              formatQuota(Number(datum?.rawQuota) || 0),
          },
        ],
      },
    },
    background: { fill: 'transparent' },
  }
}

function getSafeGranularity(
  days: number,
  granularity: TimeGranularity
): TimeGranularity {
  if (days > 29) return 'day'
  if (days > 14 && granularity === 'hour') return 'day'
  return granularity
}

export function ChannelUsageTable(props: ChannelUsageTableProps) {
  const { t } = useTranslation()
  const { resolvedTheme } = useTheme()
  const { customization } = useThemeCustomization()
  const themeManagerRef = useRef<
    (typeof import('@visactor/vchart'))['ThemeManager'] | null
  >(null)
  const [data, setData] = useState<ChannelQuotaDataItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const [themeReady, setThemeReady] = useState(false)
  const [timeGranularity, setTimeGranularity] = useState<TimeGranularity>(
    () => props.filters?.time_granularity ?? 'hour'
  )
  const [selectedRange, setSelectedRange] = useState<number>(() =>
    getDefaultDays(props.filters?.time_granularity)
  )
  const [topChannelLimit, setTopChannelLimit] = useState(10)
  const { filters } = props

  useEffect(() => {
    const updateTheme = async () => {
      setThemeReady(false)
      if (!themeManagerPromise) {
        themeManagerPromise = import('@visactor/vchart').then(
          (m) => m.ThemeManager
        )
      }
      const ThemeManager = await themeManagerPromise
      themeManagerRef.current = ThemeManager
      ThemeManager.setCurrentTheme(resolvedTheme === 'dark' ? 'dark' : 'light')
      setThemeReady(true)
    }
    updateTheme()
  }, [resolvedTheme])

  const handleRangeChange = useCallback(
    (days: number) => {
      setSelectedRange(days)
      setTimeGranularity((current) => getSafeGranularity(days, current))
    },
    [setSelectedRange]
  )

  const handleGranularityChange = useCallback(
    (granularity: TimeGranularity) => {
      setTimeGranularity(getSafeGranularity(selectedRange, granularity))
    },
    [selectedRange]
  )

  useEffect(() => {
    const abortController = new AbortController()
    setLoading(true)
    setError(false)

    const { start, end } = getRollingDateRange(selectedRange)

    getChannelQuotaDates({
      start_timestamp: Math.floor(start.getTime() / 1000),
      end_timestamp: Math.floor(end.getTime() / 1000),
      default_time: timeGranularity,
      ...(filters?.username && { username: filters.username }),
    })
      .then((res) => {
        if (abortController.signal.aborted) return
        setData(res?.data || [])
      })
      .catch(() => {
        if (abortController.signal.aborted) return
        setData([])
        setError(true)
      })
      .finally(() => {
        if (!abortController.signal.aborted) {
          setLoading(false)
        }
      })

    return () => {
      abortController.abort()
    }
  }, [filters?.username, selectedRange, timeGranularity])

  const rows = useMemo(() => aggregateChannelUsage(data), [data])
  const visibleRows = useMemo(
    () => rows.slice(0, topChannelLimit),
    [rows, topChannelLimit]
  )
  const totalQuota = useMemo(
    () => rows.reduce((sum, row) => sum + row.quota, 0),
    [rows]
  )
  const trendSpec = useMemo(
    () =>
      buildChannelTrendSpec(data, rows, timeGranularity, topChannelLimit, t),
    [data, rows, timeGranularity, topChannelLimit, t]
  )

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex flex-col gap-2 border-b px-3 py-2 sm:px-5 sm:py-3'>
        <div className='flex items-center gap-2'>
          <Router className='text-muted-foreground/60 size-4' />
          <div className='text-sm font-semibold'>{t('Channel Usage')}</div>
          <span className='text-muted-foreground text-xs'>
            {t('Total:')} {formatQuota(totalQuota)}
          </span>
          {loading && (
            <Loader2 className='text-muted-foreground size-3.5 animate-spin' />
          )}
        </div>

        <div className='flex items-center gap-1.5 overflow-x-auto pb-1'>
          <div className='flex shrink-0 items-center gap-1.5 rounded-lg border p-0.5'>
            {CHANNEL_TIME_RANGE_PRESETS.map((preset) => (
              <button
                key={preset.days}
                type='button'
                onClick={() => handleRangeChange(preset.days)}
                className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                  selectedRange === preset.days
                    ? 'bg-primary text-primary-foreground shadow-sm'
                    : 'text-muted-foreground hover:bg-muted hover:text-foreground'
                }`}
              >
                {t(preset.label)}
              </button>
            ))}
          </div>

          <div className='flex shrink-0 items-center gap-1.5 rounded-lg border p-0.5'>
            {TIME_GRANULARITY_OPTIONS.map((opt) => {
              const disabled =
                (selectedRange > 14 && opt.value === 'hour') ||
                (selectedRange > 29 && opt.value === 'week')
              return (
                <button
                  key={opt.value}
                  type='button'
                  disabled={disabled}
                  onClick={() =>
                    handleGranularityChange(opt.value as TimeGranularity)
                  }
                  className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-45 ${
                    timeGranularity === opt.value
                      ? 'bg-primary text-primary-foreground shadow-sm'
                      : 'text-muted-foreground hover:bg-muted hover:text-foreground'
                  }`}
                >
                  {t(opt.label)}
                </button>
              )
            })}
          </div>

          <div className='flex shrink-0 items-center gap-1.5 rounded-lg border p-0.5'>
            <span className='text-muted-foreground px-2 text-xs font-medium'>
              {t('Top Channels')}
            </span>
            {TOP_CHANNEL_LIMIT_OPTIONS.map((limit) => (
              <button
                key={limit}
                type='button'
                onClick={() => setTopChannelLimit(limit)}
                className={`rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                  topChannelLimit === limit
                    ? 'bg-primary text-primary-foreground shadow-sm'
                    : 'text-muted-foreground hover:bg-muted hover:text-foreground'
                }`}
              >
                {t('Top {{count}}', { count: limit })}
              </button>
            ))}
          </div>
        </div>
      </div>

      {!loading && (error || rows.length === 0) ? (
        <div className='text-muted-foreground p-6 text-center text-sm'>
          {error
            ? t('Failed to load channel usage')
            : t('No channel usage data')}
        </div>
      ) : (
        <div className='grid gap-3 p-3 sm:p-4'>
          <div className='overflow-hidden rounded-lg border'>
            <div className='flex w-full items-center gap-2 border-b px-3 py-2 sm:px-5 sm:py-3'>
              <TrendingUp className='text-muted-foreground/60 size-4' />
              <div className='text-sm font-semibold'>
                {t('Channel Consumption Trend')}
              </div>
              <span className='text-muted-foreground text-xs'>
                {t(
                  'Use the controls to inspect per-channel consumption across longer windows'
                )}
              </span>
            </div>
            <div className='h-[300px] p-1.5 sm:h-96 sm:p-2'>
              {loading || !themeReady ? (
                <Skeleton className='h-full w-full' />
              ) : (
                <VChart
                  key={`channel-trend-${timeGranularity}-${topChannelLimit}-${resolvedTheme}-${customization.preset}`}
                  spec={{
                    ...trendSpec,
                    theme: resolvedTheme === 'dark' ? 'dark' : 'light',
                    background: 'transparent',
                  }}
                  option={VCHART_OPTION}
                />
              )}
            </div>
          </div>

          <div className='max-h-[360px] overflow-auto rounded-lg border'>
            <Table className='text-sm'>
              <TableHeader className='bg-background sticky top-0 z-10'>
                <TableRow className='hover:bg-transparent'>
                  <TableHead>{t('Channel')}</TableHead>
                  <TableHead>{t('Group')}</TableHead>
                  <TableHead className='text-right'>{t('Quota')}</TableHead>
                  <TableHead className='text-right'>{t('Calls')}</TableHead>
                  <TableHead className='text-right'>{t('Tokens')}</TableHead>
                  <TableHead className='text-right'>{t('Share')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {loading
                  ? Array.from({ length: 5 }).map((_, index) => (
                      <TableRow key={index}>
                        <TableCell>
                          <Skeleton className='h-4 w-44' />
                        </TableCell>
                        <TableCell>
                          <Skeleton className='h-4 w-24' />
                        </TableCell>
                        <TableCell className='text-right'>
                          <Skeleton className='ml-auto h-4 w-20' />
                        </TableCell>
                        <TableCell className='text-right'>
                          <Skeleton className='ml-auto h-4 w-14' />
                        </TableCell>
                        <TableCell className='text-right'>
                          <Skeleton className='ml-auto h-4 w-16' />
                        </TableCell>
                        <TableCell className='text-right'>
                          <Skeleton className='ml-auto h-4 w-14' />
                        </TableCell>
                      </TableRow>
                    ))
                  : visibleRows.map((row) => (
                      <TableRow key={row.channelId}>
                        <TableCell className='max-w-[260px] truncate font-mono'>
                          <span className='text-muted-foreground mr-2'>
                            #{row.channelId}
                          </span>
                          {row.channelName}
                        </TableCell>
                        <TableCell className='text-muted-foreground max-w-[180px] truncate'>
                          {row.group || '-'}
                        </TableCell>
                        <TableCell className='text-right font-mono font-semibold tabular-nums'>
                          {formatQuota(row.quota)}
                        </TableCell>
                        <TableCell className='text-right font-mono tabular-nums'>
                          {formatNumber(row.count)}
                        </TableCell>
                        <TableCell className='text-right font-mono tabular-nums'>
                          {formatCompactNumber(row.tokenUsed)}
                        </TableCell>
                        <TableCell className='text-right font-mono tabular-nums'>
                          {formatPercent(row.share)}
                        </TableCell>
                      </TableRow>
                    ))}
              </TableBody>
            </Table>
          </div>
        </div>
      )}
    </div>
  )
}
