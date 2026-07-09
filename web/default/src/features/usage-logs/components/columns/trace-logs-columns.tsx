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
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { type ColumnDef } from '@tanstack/react-table'
import {
  Activity,
  AlertTriangle,
  Check,
  Copy,
  Eye,
  Route,
  Timer,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import {
  formatLogQuota,
  formatTimestampToDate,
  formatTokens,
  formatUseTime,
} from '@/lib/format'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { DataTableColumnHeader } from '@/components/data-table'
import { StatusBadge, type StatusBadgeProps } from '@/components/status-badge'
import {
  getCliproxyCPADispatchAudits,
  type CliproxyCPADispatchAuditRecord,
} from '@/features/channels/api'
import type { UsageLog } from '../../data/schema'
import { formatModelName, parseLogOther } from '../../lib/format'
import {
  getLogTypeConfig,
  isDisplayableLogType,
  isTimingLogType,
} from '../../lib/utils'
import type { LogOtherData } from '../../types'
import { DetailsDialog } from '../dialogs/details-dialog'
import { ModelBadge } from '../model-badge'
import { useUsageLogsContext } from '../usage-logs-provider'

function msToText(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms) || ms < 0) return '-'
  return formatUseTime(ms / 1000)
}

function secToText(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds) || seconds < 0) return '-'
  return formatUseTime(seconds)
}

function traceStatusVariant(
  log: UsageLog,
  other: LogOtherData | null
): StatusBadgeProps['variant'] {
  if (log.type === 5) return 'red'
  const streamStatus = other?.stream_status?.status
  if (streamStatus === 'error') return 'red'
  if (streamStatus === 'ok') return 'green'
  if (log.type === 2) return 'blue'
  return 'neutral'
}

function traceStatusLabel(log: UsageLog, other: LogOtherData | null): string {
  if (log.type === 5) return 'error'
  const streamStatus = other?.stream_status?.status
  if (streamStatus) return streamStatus
  if (log.type === 2) return 'consume'
  return getLogTypeConfig(log.type).label
}

function compactNumber(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return '-'
  return value.toLocaleString()
}

function isCliproxyCPATraceLog(log: UsageLog): boolean {
  if (!log.request_id || !log.channel) return false
  const name = (log.channel_name || '').toLowerCase()
  return name.includes('cliproxy') || name.includes('cpa')
}

function cpaAuditAccountLabel(
  value:
    | { account?: string; auth_index?: string; provider?: string }
    | null
    | undefined,
  sensitiveVisible: boolean
): string {
  if (!value) return '-'
  if (!sensitiveVisible) return '••••'
  if (value.account && value.account.trim() !== '') return value.account
  if (value.auth_index && value.auth_index.trim() !== '') {
    return `account ${value.auth_index.slice(-6)}`
  }
  return value.provider || '-'
}

function cpaAuditStateLabel(
  state: string | null | undefined,
  reason: string | null | undefined
): string {
  const normalized = (state || reason || '').trim()
  if (normalized === 'available') return '可用'
  if (normalized === 'manual_disabled') return '手动禁用'
  if (normalized === 'quota_7d_exhausted') return '7d 用完'
  if (normalized === 'quota_5h_exhausted') return '5h 用完'
  if (normalized === 'protected_reserve') return '低水位保护'
  if (normalized === 'auth_invalid') return '登录失效'
  if (normalized === 'cooldown') return '冷却'
  if (normalized === 'unsupported_model') return '不支持模型'
  if (normalized === 'free_tier_blocked') return '免费号拦截'
  if (normalized === 'blocked') return '阻塞'
  if (normalized === 'skipped') return reason || '跳过'
  return normalized || '-'
}

function cpaAuditErrorLabel(
  error: CliproxyCPADispatchAuditRecord['error']
): string | null {
  if (!error) return null
  if (error.message && error.message.trim() !== '') return error.message
  if (error.code && error.code.trim() !== '') return error.code
  return error.http_status == null ? null : `HTTP ${error.http_status}`
}

function cpaAuditSelectedAttempt(audit: CliproxyCPADispatchAuditRecord) {
  const attempts = audit.attempts ?? []
  return (
    attempts.find((attempt) => attempt.success === true) ??
    attempts[attempts.length - 1]
  )
}

function CPATraceInline({
  log,
  other,
  sensitiveVisible,
}: {
  log: UsageLog
  other: LogOtherData | null
  sensitiveVisible: boolean
}) {
  const enabled = isCliproxyCPATraceLog(log)
  const { data, isFetching, error } = useQuery({
    queryKey: ['cliproxy-cpa-dispatch-audit', log.channel, log.request_id],
    queryFn: () =>
      getCliproxyCPADispatchAudits(log.channel, 1, log.request_id),
    enabled,
    staleTime: 5_000,
    refetchOnWindowFocus: false,
  })

  if (!enabled) return null
  const audit = data?.success ? data.data?.dispatches?.[0] : undefined
  const selected = audit ? cpaAuditSelectedAttempt(audit) : undefined
  const skipped = audit
    ? (audit.candidates ?? []).filter(
        (candidate) => candidate.schedulable === false
      )
    : []
  const errorLabel = audit ? cpaAuditErrorLabel(audit.error ?? selected?.error) : null

  if (isFetching && !audit) {
    return (
      <span className='text-muted-foreground/70 mt-1 text-[11px]'>
        CPA Trace 读取中...
      </span>
    )
  }

  if (error || data?.success === false) {
    return (
      <span className='text-destructive mt-1 text-[11px]'>
        CPA Trace 读取失败
      </span>
    )
  }

  if (!audit) {
    return (
      <span className='text-muted-foreground/70 mt-1 text-[11px]'>
        CPA Trace 暂无匹配
      </span>
    )
  }

  const attempts = audit.attempts ?? []
  const selectedLabel = cpaAuditAccountLabel(selected, sensitiveVisible)
  const skippedItems = skipped.slice(0, 4).map((candidate) => ({
    account: cpaAuditAccountLabel(candidate, sensitiveVisible),
    reason: cpaAuditStateLabel(candidate.state, candidate.reason),
  }))

  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger
          render={
            <div className='mt-1 w-full max-w-[220px] min-w-0 cursor-help overflow-hidden rounded border border-emerald-500/30 bg-emerald-500/5 px-1.5 py-1 text-[11px] leading-snug' />
          }
        >
          <div className='truncate text-emerald-700 dark:text-emerald-300'>
            CPA 选中 {selectedLabel}
          </div>
          <div className='truncate text-emerald-700 dark:text-emerald-300'>
            尝试 {attempts.length} · 跳过 {skipped.length} · 首包{' '}
            {msToText(audit.first_payload_ms)}
          </div>
          <div className='text-muted-foreground/80 truncate'>
            NewAPI FRT {msToText(other?.frt)} / CPA 总耗时{' '}
            {msToText(audit.duration_ms)}
          </div>
        </TooltipTrigger>
        <TooltipContent
          side='bottom'
          align='start'
          className='block w-[380px] max-w-[calc(100vw-2rem)] space-y-2 whitespace-normal p-3 text-left text-xs leading-relaxed'
        >
          <div className='flex items-center justify-between gap-3'>
            <span className='font-semibold'>CPA Trace</span>
            <span className='text-background/70 font-mono'>
              尝试 {attempts.length} / 跳过 {skipped.length}
            </span>
          </div>
          <div className='grid grid-cols-[72px_minmax(0,1fr)] gap-x-2 gap-y-1'>
            <span className='text-background/70'>request_id</span>
            <span className='break-all font-mono'>{audit.request_id || '-'}</span>
            <span className='text-background/70'>模型</span>
            <span className='break-words'>{audit.model || selected?.model || '-'}</span>
            <span className='text-background/70'>选中</span>
            <span className='break-words'>{selectedLabel}</span>
            <span className='text-background/70'>耗时</span>
            <span>
              首包 {msToText(audit.first_payload_ms)} · CPA{' '}
              {msToText(audit.duration_ms)} · NewAPI {msToText(other?.frt)}
            </span>
          </div>
          <div className='border-background/20 border-t pt-2'>
            <div className='text-background/70 mb-1'>跳过账号</div>
            {skippedItems.length === 0 ? (
              <div>无</div>
            ) : (
              <div className='space-y-1'>
                {skippedItems.map((item, index) => (
                  <div
                    key={`${item.account}-${item.reason}-${index}`}
                    className='grid grid-cols-[minmax(0,1fr)_auto] gap-2'
                  >
                    <span className='break-words'>{item.account}</span>
                    <span className='text-background/70 whitespace-nowrap'>
                      {item.reason}
                    </span>
                  </div>
                ))}
                {skipped.length > skippedItems.length && (
                  <div className='text-background/70'>
                    还有 {skipped.length - skippedItems.length} 个
                  </div>
                )}
              </div>
            )}
          </div>
          {errorLabel && (
            <div className='text-destructive break-words border-t pt-2'>
              {errorLabel}
            </div>
          )}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

function buildTraceContext(
  log: UsageLog,
  other: LogOtherData | null,
  isAdmin: boolean
): string {
  const adminInfo = other?.admin_info
  const stream = other?.stream_status
  const context = {
    context_type: isAdmin ? 'admin_request_trace' : 'request_trace',
    intended_flow: isAdmin
      ? 'user reports request id or time to admin; admin copies this trace for AI-assisted debugging'
      : 'user-facing request summary',
    redaction: 'prompt content, API keys, and full token values are omitted',
    request_id: log.request_id || undefined,
    log_id: log.id,
    time: formatTimestampToDate(log.created_at),
    log_type: getLogTypeConfig(log.type).label,
    user: isAdmin ? log.username || undefined : undefined,
    token_name: isAdmin ? log.token_name || undefined : undefined,
    group: log.group || other?.group || undefined,
    model: log.model_name || undefined,
    upstream_model: other?.upstream_model_name || undefined,
    request_path: other?.request_path || undefined,
    request_conversion: other?.request_conversion || undefined,
    channel: isAdmin
      ? {
          id: log.channel || undefined,
          name: log.channel_name || undefined,
          retry_chain: adminInfo?.use_channel || undefined,
          affinity: adminInfo?.channel_affinity
            ? {
                rule: adminInfo.channel_affinity.rule_name || undefined,
                selected_group:
                  adminInfo.channel_affinity.using_group ||
                  adminInfo.channel_affinity.selected_group ||
                  undefined,
                key_hint: adminInfo.channel_affinity.key_hint || undefined,
                key_fp: adminInfo.channel_affinity.key_fp || undefined,
              }
            : undefined,
        }
      : undefined,
    timing: {
      total_seconds: log.use_time || undefined,
      first_response_ms: other?.frt || undefined,
      stream_duration_ms: stream?.duration_ms || undefined,
      first_chunk_ms: stream?.first_chunk_ms || undefined,
      last_chunk_age_ms: stream?.last_chunk_age_ms || undefined,
    },
    stream: stream
      ? {
          status: stream.status || undefined,
          end_reason: stream.end_reason || undefined,
          error_count: stream.error_count || undefined,
          chunk_count: stream.chunk_count || undefined,
          write_count: stream.write_count || undefined,
          ping_count: stream.ping_count || undefined,
          received: stream.received || undefined,
          sent: stream.sent || undefined,
        }
      : undefined,
    usage: {
      input_tokens: log.prompt_tokens || undefined,
      output_tokens: log.completion_tokens || undefined,
      cache_read_tokens: other?.cache_tokens || undefined,
      cache_write_tokens:
        other?.cache_creation_tokens ||
        (other?.cache_creation_tokens_5m || 0) +
          (other?.cache_creation_tokens_1h || 0) ||
        undefined,
      quota: log.quota || undefined,
    },
  }
  return JSON.stringify(context, null, 2)
}

export function useTraceLogsColumns(isAdmin: boolean): ColumnDef<UsageLog>[] {
  const { t } = useTranslation()
  const columns: ColumnDef<UsageLog>[] = [
    {
      accessorKey: 'created_at',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Request')} />
      ),
      cell: ({ row }) => {
        const log = row.original
        const other = parseLogOther(log.other)
        const config = getLogTypeConfig(log.type)
        return (
          <div className='flex min-w-[180px] flex-col gap-1'>
            <span className='font-mono text-xs tabular-nums'>
              {formatTimestampToDate(log.created_at)}
            </span>
            <div className='flex flex-wrap items-center gap-1'>
              <StatusBadge
                label={t(config.label)}
                variant={config.color as StatusBadgeProps['variant']}
                size='sm'
                copyable={false}
              />
              <StatusBadge
                label={traceStatusLabel(log, other)}
                variant={traceStatusVariant(log, other)}
                size='sm'
                copyable={false}
              />
            </div>
            {log.request_id ? (
              <StatusBadge
                label={log.request_id.slice(0, 8)}
                copyText={log.request_id}
                icon={Activity}
                size='sm'
                className='font-mono'
              />
            ) : (
              <span className='text-muted-foreground/50 text-[11px]'>
                {t('No Request ID')}
              </span>
            )}
          </div>
        )
      },
      enableHiding: false,
      meta: { label: t('Request') },
    },
    {
      accessorKey: 'model_name',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Route')} />
      ),
      cell: ({ row }) => {
        const log = row.original
        if (!isDisplayableLogType(log.type)) return null
        const other = parseLogOther(log.other)
        const modelInfo = formatModelName(log)
        const conversion =
          other?.request_conversion && other.request_conversion.length > 0
            ? other.request_conversion.join(' -> ')
            : t('Native format')
        return (
          <div className='flex max-w-[260px] flex-col gap-1'>
            <ModelBadge
              modelName={modelInfo.name}
              actualModel={modelInfo.actualModel}
            />
            <span className='text-muted-foreground/70 truncate font-mono text-[11px]'>
              {other?.request_path || '-'}
            </span>
            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <span className='text-muted-foreground/70 inline-flex min-w-0 items-center gap-1 text-[11px]' />
                  }
                >
                  <Route className='size-3 shrink-0' aria-hidden='true' />
                  <span className='truncate'>{conversion}</span>
                </TooltipTrigger>
                <TooltipContent>
                  <span className='font-mono text-xs'>{conversion}</span>
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>
          </div>
        )
      },
      meta: { label: t('Route') },
    },
    {
      id: 'actor',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Actor')} />
      ),
      cell: function ActorCell({ row }) {
        const { sensitiveVisible } = useUsageLogsContext()
        const log = row.original
        if (!isDisplayableLogType(log.type)) return null
        const group = log.group || parseLogOther(log.other)?.group || ''
        return (
          <div className='flex max-w-[180px] flex-col gap-0.5 text-xs'>
            {isAdmin && (
              <span className='truncate'>
                {sensitiveVisible ? log.username || '-' : '••••'}
              </span>
            )}
            <span className='text-muted-foreground truncate font-mono text-[11px]'>
              {sensitiveVisible ? log.token_name || '-' : '••••'}
            </span>
            {group && (
              <span className='text-muted-foreground/70 truncate text-[11px]'>
                {sensitiveVisible ? group : '••••'}
              </span>
            )}
          </div>
        )
      },
      meta: { label: t('Actor'), mobileHidden: true },
    },
  ]

  if (isAdmin) {
    columns.push({
      id: 'channel',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Channel')} />
      ),
      cell: function ChannelCell({ row }) {
        const { sensitiveVisible } = useUsageLogsContext()
        const log = row.original
        if (!isDisplayableLogType(log.type)) return null
        const other = parseLogOther(log.other)
        const adminInfo = other?.admin_info
        const chain = adminInfo?.use_channel
        const affinity = adminInfo?.channel_affinity
        return (
          <div className='flex max-w-[220px] flex-col gap-0.5 text-xs'>
            <StatusBadge
              label={`#${log.channel || 0}`}
              autoColor={String(log.channel || 0)}
              copyText={String(log.channel || 0)}
              size='sm'
              className='font-mono'
            />
            {log.channel_name && (
              <span className='text-muted-foreground/70 truncate text-[11px]'>
                {sensitiveVisible ? log.channel_name : '••••'}
              </span>
            )}
            {chain && chain.length > 0 && (
              <span className='text-muted-foreground/70 truncate font-mono text-[11px]'>
                {chain.join(' -> ')}
              </span>
            )}
            {affinity && (
              <span className='truncate text-[11px] text-amber-600'>
                {t('Affinity')}: {affinity.rule_name || '-'}
              </span>
            )}
            <CPATraceInline
              log={log}
              other={other}
              sensitiveVisible={sensitiveVisible}
            />
          </div>
        )
      },
      meta: { label: t('Channel'), mobileHidden: true },
    })
  }

  columns.push(
    {
      id: 'stream',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Stream')} />
      ),
      cell: ({ row }) => {
        const log = row.original
        if (!isTimingLogType(log.type)) return null
        const other = parseLogOther(log.other)
        const stream = other?.stream_status
        const status = traceStatusLabel(log, other)
        const variant = traceStatusVariant(log, other)
        return (
          <div className='flex min-w-[170px] flex-col gap-1 text-xs'>
            <div className='flex flex-wrap items-center gap-1'>
              <StatusBadge
                label={status}
                variant={variant}
                size='sm'
                copyable={false}
              />
              {stream?.end_reason && (
                <span className='text-muted-foreground font-mono text-[11px]'>
                  {stream.end_reason}
                </span>
              )}
              {stream?.status === 'error' && (
                <AlertTriangle
                  className='size-3 text-red-500'
                  aria-hidden='true'
                />
              )}
            </div>
            <div className='text-muted-foreground/70 flex flex-wrap gap-x-2 gap-y-0.5 font-mono text-[11px]'>
              <span>chunk {compactNumber(stream?.chunk_count)}</span>
              <span>write {compactNumber(stream?.write_count)}</span>
              <span>ping {compactNumber(stream?.ping_count)}</span>
            </div>
            <div className='text-muted-foreground/70 flex flex-wrap gap-x-2 gap-y-0.5 font-mono text-[11px]'>
              <span>dur {msToText(stream?.duration_ms)}</span>
              <span>first {msToText(stream?.first_chunk_ms)}</span>
              {stream?.last_chunk_age_ms != null && (
                <span>tail {msToText(stream.last_chunk_age_ms)}</span>
              )}
            </div>
          </div>
        )
      },
      meta: { label: t('Stream') },
    },
    {
      id: 'timing',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Timing')} />
      ),
      cell: ({ row }) => {
        const log = row.original
        if (!isTimingLogType(log.type)) return null
        const other = parseLogOther(log.other)
        return (
          <div className='flex min-w-[120px] flex-col gap-0.5 text-xs'>
            <span className='inline-flex items-center gap-1 font-mono'>
              <Timer className='text-muted-foreground size-3' />
              {secToText(log.use_time)}
            </span>
            <span className='text-muted-foreground/70 font-mono text-[11px]'>
              FRT {msToText(other?.frt)}
            </span>
            {log.completion_tokens > 0 && log.use_time > 0 && (
              <span className='text-muted-foreground/70 font-mono text-[11px]'>
                {Math.round(log.completion_tokens / log.use_time)} t/s
              </span>
            )}
          </div>
        )
      },
      meta: { label: t('Timing'), mobileHidden: true },
    },
    {
      id: 'usage',
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Usage')} />
      ),
      cell: ({ row }) => {
        const log = row.original
        if (!isDisplayableLogType(log.type)) return null
        const other = parseLogOther(log.other)
        const cacheWriteSplit =
          (other?.cache_creation_tokens_5m || 0) +
          (other?.cache_creation_tokens_1h || 0)
        const cacheWrite = other?.cache_creation_tokens || cacheWriteSplit
        return (
          <div className='flex min-w-[145px] flex-col gap-0.5 text-xs'>
            <span className='font-mono'>
              {formatTokens(log.prompt_tokens)} /{' '}
              {formatTokens(log.completion_tokens)}
            </span>
            {(other?.cache_tokens || cacheWrite > 0) && (
              <span className='text-muted-foreground/70 font-mono text-[11px]'>
                cache {formatTokens(other?.cache_tokens || 0)} /{' '}
                {formatTokens(cacheWrite)}
              </span>
            )}
            <span className='text-muted-foreground/80 font-mono text-[11px]'>
              {formatLogQuota(log.quota || 0)}
            </span>
          </div>
        )
      },
      meta: { label: t('Usage'), mobileHidden: true },
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: function TraceActionsCell({ row }) {
        const [dialogOpen, setDialogOpen] = useState(false)
        const log = row.original
        const other = parseLogOther(log.other)
        const { copiedText, copyToClipboard } = useCopyToClipboard({
          notify: false,
        })
        const traceContext = buildTraceContext(log, other, isAdmin)
        const copied = copiedText === traceContext
        return (
          <>
            <div className='flex items-center gap-1'>
              <TooltipProvider>
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <Button
                        type='button'
                        variant='outline'
                        size='sm'
                        className='h-7 px-2 text-xs'
                        onClick={(event) => {
                          event.stopPropagation()
                          copyToClipboard(traceContext)
                        }}
                      />
                    }
                  >
                    {copied ? (
                      <Check className='size-3 text-green-600' />
                    ) : (
                      <Copy className='size-3' />
                    )}
                    <span>{t('Copy Admin Trace')}</span>
                  </TooltipTrigger>
                  <TooltipContent>
                    <span>
                      {t(
                        'Copy admin diagnostic context for AI-assisted debugging'
                      )}
                    </span>
                  </TooltipContent>
                </Tooltip>
              </TooltipProvider>
              <Button
                type='button'
                variant='ghost'
                size='icon'
                className='size-7'
                onClick={(event) => {
                  event.stopPropagation()
                  setDialogOpen(true)
                }}
                aria-label={t('View details')}
              >
                <Eye className='size-3.5' />
              </Button>
            </div>
            <DetailsDialog
              log={log}
              isAdmin={isAdmin}
              open={dialogOpen}
              onOpenChange={setDialogOpen}
            />
          </>
        )
      },
      meta: { label: t('Actions') },
      enableHiding: false,
    }
  )

  return columns
}
