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
import { useQuery } from '@tanstack/react-query'
import { Activity, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { formatTimestampToDate } from '@/lib/format'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { StatusBadge, type StatusBadgeProps } from '@/components/status-badge'
import { getChannelSchedulerRuntime } from '../../api'
import type { ChannelSchedulerRuntimeItem } from '../../types'

type RoutingRuntimeDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

function formatMs(value: number, hasValue: boolean) {
  if (!hasValue || !Number.isFinite(value) || value <= 0) return '-'
  return `${Math.round(value)} ms`
}

function formatPercent(value: number, hasValue = true) {
  if (!hasValue || !Number.isFinite(value)) return '-'
  return `${(value * 100).toFixed(1)}%`
}

function formatTime(value: number) {
  return formatTimestampToDate(value)
}

function runtimeStatus(
  item: ChannelSchedulerRuntimeItem,
  t: (key: string) => string
): { label: string; variant: StatusBadgeProps['variant'] } {
  if (item.temporary_unschedulable_now) {
    return { label: t('Temporarily Blocked'), variant: 'warning' }
  }
  if (item.in_flight > 0) {
    return { label: t('In Flight'), variant: 'info' }
  }
  if (item.attempt_count === 0) {
    return { label: t('Idle'), variant: 'neutral' }
  }
  if (item.failure_rate >= 0.5 || item.error_ewma >= 0.5) {
    return { label: t('Degraded'), variant: 'danger' }
  }
  if (item.failure_count > 0 || item.error_ewma > 0) {
    return { label: t('Watch'), variant: 'warning' }
  }
  return { label: t('Healthy'), variant: 'success' }
}

function compareRuntimeItems(
  a: ChannelSchedulerRuntimeItem,
  b: ChannelSchedulerRuntimeItem
) {
  if (a.temporary_unschedulable_now !== b.temporary_unschedulable_now) {
    return a.temporary_unschedulable_now ? -1 : 1
  }
  if (a.in_flight !== b.in_flight) return b.in_flight - a.in_flight
  if (a.failure_rate !== b.failure_rate) return b.failure_rate - a.failure_rate
  if (a.error_ewma !== b.error_ewma) return b.error_ewma - a.error_ewma
  return b.attempt_count - a.attempt_count
}

export function RoutingRuntimeDialog({
  open,
  onOpenChange,
}: RoutingRuntimeDialogProps) {
  const { t } = useTranslation()
  const { data, isFetching, refetch } = useQuery({
    queryKey: ['channel-scheduler-runtime'],
    queryFn: () => getChannelSchedulerRuntime({ include_idle: false }),
    enabled: open,
  })

  const items = useMemo(() => {
    return [...(data?.data?.items ?? [])].sort(compareRuntimeItems)
  }, [data?.data?.items])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='flex max-h-[calc(100dvh-2rem)] flex-col max-sm:h-dvh max-sm:w-screen max-sm:max-w-none max-sm:rounded-none sm:max-w-6xl'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <Activity className='h-5 w-5' />
            {t('Routing Monitor')}
          </DialogTitle>
          <DialogDescription>
            {t('Channels with recent attempts or temporary routing blocks')}
          </DialogDescription>
        </DialogHeader>

        <div className='flex items-center justify-between gap-3'>
          <div className='text-muted-foreground text-sm'>
            {t('Total')}: {items.length}
          </div>
          <Button
            variant='outline'
            size='sm'
            onClick={() => refetch()}
            disabled={isFetching}
          >
            <RefreshCw
              className={isFetching ? 'h-4 w-4 animate-spin' : 'h-4 w-4'}
            />
            {t('Refresh')}
          </Button>
        </div>

        <ScrollArea className='min-h-0 flex-1 rounded-md border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Channel')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
                <TableHead className='text-right'>{t('In Flight')}</TableHead>
                <TableHead className='text-right'>{t('Latency')}</TableHead>
                <TableHead className='text-right'>{t('Error EWMA')}</TableHead>
                <TableHead className='text-right'>
                  {t('Failure Rate')}
                </TableHead>
                <TableHead className='text-right'>{t('Attempts')}</TableHead>
                <TableHead>{t('Last Failure')}</TableHead>
                <TableHead>{t('Block Reason')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={9}
                    className='text-muted-foreground h-32 text-center'
                  >
                    {isFetching ? t('Loading...') : t('No runtime samples')}
                  </TableCell>
                </TableRow>
              ) : (
                items.map((item) => {
                  const status = runtimeStatus(item, t)
                  const block = item.temporary_unschedulable
                  return (
                    <TableRow key={item.channel_id}>
                      <TableCell>
                        <div className='flex min-w-48 flex-col'>
                          <span className='font-medium'>
                            #{item.channel_id} {item.channel_name}
                          </span>
                          <span className='text-muted-foreground text-xs'>
                            {item.group}
                            {item.tag ? ` / ${item.tag}` : ''}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <StatusBadge variant={status.variant} copyable={false}>
                          {status.label}
                        </StatusBadge>
                      </TableCell>
                      <TableCell className='text-right font-mono'>
                        {item.in_flight}
                      </TableCell>
                      <TableCell className='text-right font-mono'>
                        {formatMs(item.latency_ewma_ms, item.has_latency_ewma)}
                      </TableCell>
                      <TableCell className='text-right font-mono'>
                        {formatPercent(item.error_ewma, item.has_error_ewma)}
                      </TableCell>
                      <TableCell className='text-right font-mono'>
                        {formatPercent(item.failure_rate)}
                      </TableCell>
                      <TableCell className='text-right font-mono'>
                        {item.success_count}/{item.failure_count}
                      </TableCell>
                      <TableCell className='font-mono text-xs'>
                        {formatTime(item.last_failure_unix)}
                      </TableCell>
                      <TableCell className='max-w-64'>
                        {block ? (
                          <div className='flex flex-col'>
                            <span className='truncate'>
                              {block.reason || '-'}
                            </span>
                            <span className='text-muted-foreground text-xs'>
                              {block.status_code || '-'} /{' '}
                              {block.error_code || '-'} /{' '}
                              {formatTime(block.until_unix)}
                            </span>
                          </div>
                        ) : (
                          <span className='text-muted-foreground'>-</span>
                        )}
                      </TableCell>
                    </TableRow>
                  )
                })
              )}
            </TableBody>
          </Table>
        </ScrollArea>
      </DialogContent>
    </Dialog>
  )
}
