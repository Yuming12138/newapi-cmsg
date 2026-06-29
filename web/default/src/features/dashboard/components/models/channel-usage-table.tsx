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
import { useEffect, useMemo, useState } from 'react'
import { Router } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  formatCompactNumber,
  formatNumber,
  formatPercent,
  formatQuota,
} from '@/lib/format'
import { computeTimeRange } from '@/lib/time'
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
import { buildQueryParams, getDefaultDays } from '@/features/dashboard/lib'
import type {
  ChannelQuotaDataItem,
  DashboardFilters,
} from '@/features/dashboard/types'

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

export function ChannelUsageTable(props: ChannelUsageTableProps) {
  const { t } = useTranslation()
  const [data, setData] = useState<ChannelQuotaDataItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const { filters } = props

  useEffect(() => {
    const abortController = new AbortController()
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)
    setError(false)

    const timeRange = computeTimeRange(
      getDefaultDays(filters?.time_granularity),
      filters?.start_timestamp,
      filters?.end_timestamp
    )

    getChannelQuotaDates(buildQueryParams(timeRange, filters))
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
  }, [filters])

  const rows = useMemo(() => aggregateChannelUsage(data), [data])
  const totalQuota = useMemo(
    () => rows.reduce((sum, row) => sum + row.quota, 0),
    [rows]
  )

  return (
    <div className='overflow-hidden rounded-lg border'>
      <div className='flex flex-col gap-1.5 border-b px-3 py-2 sm:px-5 sm:py-3 lg:flex-row lg:items-center lg:justify-between'>
        <div className='flex items-center gap-2'>
          <Router className='text-muted-foreground/60 size-4' />
          <div className='text-sm font-semibold'>{t('Channel Usage')}</div>
          <span className='text-muted-foreground text-xs'>
            {t('Total:')} {formatQuota(totalQuota)}
          </span>
        </div>
        <div className='text-muted-foreground text-xs'>
          {t('Per-channel consumption within the selected range')}
        </div>
      </div>

      {!loading && (error || rows.length === 0) ? (
        <div className='text-muted-foreground p-6 text-center text-sm'>
          {error
            ? t('Failed to load channel usage')
            : t('No channel usage data')}
        </div>
      ) : (
        <div className='max-h-[360px] overflow-auto'>
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
                : rows.map((row) => (
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
      )}
    </div>
  )
}
