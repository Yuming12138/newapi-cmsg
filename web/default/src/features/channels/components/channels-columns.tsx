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
/* eslint-disable react-refresh/only-export-components */
import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { type ColumnDef } from '@tanstack/react-table'
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  ListOrdered,
  RotateCcw,
  Shuffle,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { getCurrencyLabel } from '@/lib/currency'
import {
  formatTimestampToDate,
  formatQuota as formatQuotaValue,
} from '@/lib/format'
import { getLobeIcon } from '@/lib/lobe-icon'
import { cn, truncateText } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Progress } from '@/components/ui/progress'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTableColumnHeader } from '@/components/data-table/column-header'
import { GroupBadge } from '@/components/group-badge'
import {
  StatusBadge,
  dotColorMap,
  textColorMap,
} from '@/components/status-badge'
import {
  consumeCliproxyCPAResetCredit,
  getCodexUsage,
  resetCliproxyCPAQuotaState,
} from '../api'
import { CHANNEL_STATUS_CONFIG, MODEL_FETCHABLE_TYPES } from '../constants'
import {
  formatBalance,
  formatRelativeTime,
  formatResponseTime,
  getBalanceVariant,
  getChannelTypeIcon,
  getChannelTypeLabel,
  getResponseTimeConfig,
  isMultiKeyChannel,
  parseModelsList,
  parseGroupsList,
  parseChannelSettings,
  handleUpdateChannelField,
  handleUpdateTagField,
  handleUpdateChannelBalance,
  isTagAggregateRow,
  channelsQueryKeys,
  type TagRow,
} from '../lib'
import { parseUpstreamUpdateMeta } from '../lib/upstream-update-utils'
import type { Channel } from '../types'
import { useChannels } from './channels-provider'
import { DataTableRowActions } from './data-table-row-actions'
import { DataTableTagRowActions } from './data-table-tag-row-actions'
import {
  CodexUsageDialog,
  type CodexUsageDialogData,
} from './dialogs/codex-usage-dialog'
import { NumericSpinnerInput } from './numeric-spinner-input'

function parseIonetMeta(otherInfo: string | null | undefined): null | {
  source?: string
  deployment_id?: string
} {
  if (!otherInfo) return null
  try {
    const parsed = JSON.parse(otherInfo)
    if (parsed && typeof parsed === 'object') {
      return parsed
    }
  } catch {
    return null
  }
  return null
}

type CliproxyCPAQuotaWindow = {
  shareRemainingPercent: number | null
  remainingPercent: number | null
  usedPercent: number | null
  resetAfterSeconds: number | null
  resetAt: number | null
}

type CliproxyCPAQuotaBucket = {
  key: string
  label: string
  bucketType: string | null
  canExhaust: boolean | null
  accountCount: number | null
  availableAccountCount: number | null
  balanceUnits: number | null
  usableBalanceUnits: number | null
  remainingSharePercent: number | null
  rawRemainingPercent: number | null
  fiveHour: CliproxyCPAQuotaWindow | null
  weekly: CliproxyCPAQuotaWindow | null
  nextResetAfterSeconds: number | null
  nextResetAt: number | null
  reserveFiveHourPercent: number | null
  reserveWeeklyPercent: number | null
}

type CliproxyCPAQuotaAccount = {
  authIndex: string
  label: string
  bucket: string | null
  state: string | null
  ok: boolean | null
  schedulable: boolean | null
  skipped: boolean | null
  runtimeUnavailable: boolean | null
  quotaExhaustedWindow: string | null
  unavailable: boolean | null
  disabled: boolean | null
  canExhaust: boolean | null
  reason: string | null
  retryable: boolean | null
  resetAt: number | null
  lastError: string | null
  error: string | null
  planType: string | null
  resetCreditsAvailable: number | null
  balanceUnits: number | null
  usableBalanceUnits: number | null
  fiveHour: CliproxyCPAQuotaWindow | null
  weekly: CliproxyCPAQuotaWindow | null
}

type CliproxyCPAQuotaMeta = {
  guardMode: string | null
  shareLimitPercent: number | null
  remainingSharePercent: number | null
  usableBalanceUnits: number | null
  totalBalanceUnits: number | null
  accountCount: number | null
  availableAccountCount: number | null
  updatedAt: number | null
  fiveHour: CliproxyCPAQuotaWindow | null
  weekly: CliproxyCPAQuotaWindow | null
  nextResetAfterSeconds: number | null
  nextResetAt: number | null
  buckets: CliproxyCPAQuotaBucket[]
  accounts: CliproxyCPAQuotaAccount[]
}

function asObject(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object'
    ? (value as Record<string, unknown>)
    : null
}

function numberValue(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : null
  }
  return null
}

function booleanValue(value: unknown): boolean | null {
  if (typeof value === 'boolean') return value
  if (typeof value === 'number') return value !== 0
  if (typeof value === 'string') {
    const normalized = value.trim().toLowerCase()
    if (['1', 'true', 'yes', 'y', 'on'].includes(normalized)) return true
    if (['0', 'false', 'no', 'n', 'off'].includes(normalized)) return false
  }
  return null
}

function timestampValue(value: unknown): number | null {
  const numeric = numberValue(value)
  if (numeric != null) {
    if (numeric <= 0) return null
    return numeric > 1_000_000_000_000
      ? Math.round(numeric / 1000)
      : Math.round(numeric)
  }
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Date.parse(value)
    return Number.isFinite(parsed) ? Math.round(parsed / 1000) : null
  }
  return null
}

function getCliproxyCPAResetAt(
  window: CliproxyCPAQuotaWindow | null | undefined,
  updatedAt: number | null | undefined
): number | null {
  if (!window) return null
  if (window.resetAt != null) return window.resetAt
  if (
    window.resetAfterSeconds != null &&
    updatedAt != null &&
    Number.isFinite(updatedAt)
  ) {
    return Math.round(updatedAt + window.resetAfterSeconds)
  }
  return null
}

function parseCliproxyCPAQuotaWindow(
  value: unknown,
  shareLimitPercent: number | null
): CliproxyCPAQuotaWindow | null {
  const item = asObject(value)
  if (!item) return null
  const usedPercent = numberValue(item.used_percent)
  const remainingPercent = numberValue(item.remaining_percent)
  const shareRemainingPercent =
    usedPercent == null || shareLimitPercent == null
      ? null
      : Math.max(0, shareLimitPercent - usedPercent)

  return {
    shareRemainingPercent,
    remainingPercent,
    usedPercent,
    resetAfterSeconds: numberValue(item.reset_after_seconds),
    resetAt: timestampValue(item.reset_at),
  }
}

function quotaSourceWindowsByName(
  value: unknown
): Record<string, Record<string, unknown>> {
  if (!Array.isArray(value)) return {}
  const result: Record<string, Record<string, unknown>> = {}
  value.forEach((raw) => {
    const item = asObject(raw)
    if (!item || typeof item.name !== 'string' || item.name.trim() === '') {
      return
    }
    result[item.name.trim()] = item
  })
  return result
}

function getCliproxyCPAResetAfter(
  fiveHour: CliproxyCPAQuotaWindow | null,
  weekly: CliproxyCPAQuotaWindow | null
): number | null {
  const candidates = [
    fiveHour?.resetAfterSeconds,
    weekly?.resetAfterSeconds,
  ].filter((value): value is number => value != null && value >= 0)
  return candidates.length > 0 ? Math.min(...candidates) : null
}

function getCliproxyCPANextResetAt(
  fiveHour: CliproxyCPAQuotaWindow | null,
  weekly: CliproxyCPAQuotaWindow | null,
  updatedAt: number | null
): number | null {
  const candidates = [
    getCliproxyCPAResetAt(fiveHour, updatedAt),
    getCliproxyCPAResetAt(weekly, updatedAt),
  ].filter((value): value is number => value != null && value > 0)
  return candidates.length > 0 ? Math.min(...candidates) : null
}

function cliproxyCPABucketLabel(
  key: string,
  value: Record<string, unknown>,
  canExhaust: boolean | null
): string {
  if (typeof value.label === 'string' && value.label.trim() !== '') {
    return value.label
  }
  if (key === 'personal' || canExhaust) return '个人池'
  if (key === 'protected') return '共享 Pro'
  return key
}

function parseCliproxyCPAQuotaBucket(
  key: string,
  value: unknown,
  updatedAt: number | null
): CliproxyCPAQuotaBucket | null {
  const item = asObject(value)
  if (!item) return null
  const windows = asObject(item.windows)
  const canExhaust = booleanValue(item.can_exhaust)
  const fiveHour = parseCliproxyCPAQuotaWindow(windows?.['5h'], null)
  const weekly = parseCliproxyCPAQuotaWindow(windows?.['7d'], null)
  return {
    key,
    label: cliproxyCPABucketLabel(key, item, canExhaust),
    bucketType: typeof item.bucket === 'string' ? item.bucket : null,
    canExhaust,
    accountCount: numberValue(item.account_count),
    availableAccountCount: numberValue(item.available_account_count),
    balanceUnits: numberValue(item.balance_units),
    usableBalanceUnits: numberValue(item.usable_balance_units),
    remainingSharePercent: numberValue(item.remaining_share_percent),
    rawRemainingPercent: numberValue(item.raw_remaining_percent),
    fiveHour,
    weekly,
    nextResetAfterSeconds: getCliproxyCPAResetAfter(fiveHour, weekly),
    nextResetAt: getCliproxyCPANextResetAt(fiveHour, weekly, updatedAt),
    reserveFiveHourPercent: numberValue(item.min_remaining_percent_5h),
    reserveWeeklyPercent: numberValue(item.min_remaining_percent_7d),
  }
}

function cliproxyCPAAccountLabel(
  item: Record<string, unknown>,
  authIndex: string
): string {
  const raw =
    typeof item.account_label === 'string' && item.account_label.trim() !== ''
      ? item.account_label
      : typeof item.label === 'string' && item.label.trim() !== ''
        ? item.label
        : ''
  if (raw) return raw
  return authIndex ? `account ${authIndex.slice(-6)}` : 'account'
}

function cliproxyCPAAccountLastError(value: unknown): string | null {
  if (typeof value === 'string' && value.trim() !== '') return value
  const item = asObject(value)
  if (!item) return null
  const message = typeof item.message === 'string' ? item.message.trim() : ''
  const code = typeof item.code === 'string' ? item.code.trim() : ''
  const httpStatus = numberValue(item.http_status)
  if (message) return message
  if (code) return code
  return httpStatus == null ? null : `HTTP ${httpStatus}`
}

function parseCliproxyCPAQuotaAccount(
  value: unknown
): CliproxyCPAQuotaAccount | null {
  const item = asObject(value)
  if (!item) return null
  const authIndex =
    typeof item.auth_index === 'string' ? item.auth_index.trim() : ''
  if (!authIndex) return null
  const windows = asObject(item.windows)
  return {
    authIndex,
    label: cliproxyCPAAccountLabel(item, authIndex),
    bucket: typeof item.bucket === 'string' ? item.bucket : null,
    state: typeof item.state === 'string' ? item.state : null,
    ok: booleanValue(item.ok),
    schedulable: booleanValue(item.schedulable),
    skipped: booleanValue(item.skipped),
    runtimeUnavailable: booleanValue(item.runtime_unavailable),
    quotaExhaustedWindow:
      typeof item.quota_exhausted_window === 'string'
        ? item.quota_exhausted_window
        : null,
    unavailable: booleanValue(item.unavailable),
    disabled: booleanValue(item.disabled),
    canExhaust: booleanValue(item.can_exhaust),
    reason: typeof item.reason === 'string' ? item.reason : null,
    retryable: booleanValue(item.retryable),
    resetAt: timestampValue(item.reset_at),
    lastError: cliproxyCPAAccountLastError(item.last_error),
    error: typeof item.error === 'string' ? item.error : null,
    planType: typeof item.plan_type === 'string' ? item.plan_type : null,
    resetCreditsAvailable: numberValue(item.reset_credits_available),
    balanceUnits: numberValue(item.balance_units),
    usableBalanceUnits: numberValue(item.usable_balance_units),
    fiveHour: parseCliproxyCPAQuotaWindow(windows?.['5h'], null),
    weekly: parseCliproxyCPAQuotaWindow(windows?.['7d'], null),
  }
}

function parseCliproxyCPAQuotaAccounts(
  value: unknown
): CliproxyCPAQuotaAccount[] {
  if (!Array.isArray(value)) return []
  return value
    .map(parseCliproxyCPAQuotaAccount)
    .filter((item): item is CliproxyCPAQuotaAccount => item != null)
}

function parseCliproxyCPAQuotaBuckets(
  value: unknown,
  updatedAt: number | null
): CliproxyCPAQuotaBucket[] {
  const buckets = asObject(value)
  if (!buckets) return []
  return Object.entries(buckets)
    .map(([key, item]) => parseCliproxyCPAQuotaBucket(key, item, updatedAt))
    .filter((item): item is CliproxyCPAQuotaBucket => item != null)
}

function parseCliproxyCPAQuotaMeta(
  otherInfo: string | null | undefined
): CliproxyCPAQuotaMeta | null {
  if (!otherInfo) return null
  try {
    const parsed = asObject(JSON.parse(otherInfo))
    const quotaSource = asObject(parsed?.quota_source)
    const quotaSourceWindows = quotaSourceWindowsByName(quotaSource?.windows)
    const guard = asObject(parsed?.cliproxy_cpa_quota_guard)
    const health = asObject(guard?.health)
    const windows = asObject(health?.windows)
    if (!guard || !health) return null
    const updatedAt =
      timestampValue(quotaSource?.updated_at) ?? timestampValue(guard.updated_at)
    const buckets = parseCliproxyCPAQuotaBuckets(health.buckets, updatedAt)
    const accounts = parseCliproxyCPAQuotaAccounts(health.accounts)

    const shareLimitPercent = numberValue(health.share_limit_percent)
    const fiveHour = parseCliproxyCPAQuotaWindow(
      quotaSourceWindows['5h'] ?? windows?.['5h'],
      shareLimitPercent
    )
    const weekly = parseCliproxyCPAQuotaWindow(
      quotaSourceWindows['7d'] ?? windows?.['7d'],
      shareLimitPercent
    )
    if (!fiveHour && !weekly && buckets.length === 0) return null
    const topNextResetAfterSeconds = getCliproxyCPAResetAfter(fiveHour, weekly)
    const topNextResetAt = getCliproxyCPANextResetAt(
      fiveHour,
      weekly,
      updatedAt
    )
    const bucketNextResetAfterCandidates = buckets
      .map((bucket) => bucket.nextResetAfterSeconds)
      .filter((value): value is number => value != null && value >= 0)
    const bucketNextResetAtCandidates = buckets
      .map((bucket) => bucket.nextResetAt)
      .filter((value): value is number => value != null && value > 0)

    return {
      shareLimitPercent,
      remainingSharePercent: numberValue(health.remaining_share_percent),
      usableBalanceUnits:
        numberValue(quotaSource?.balance) ??
        numberValue(health.usable_balance_units),
      totalBalanceUnits: numberValue(health.total_balance_units),
      accountCount: numberValue(health.account_count),
      availableAccountCount: numberValue(health.available_account_count),
      updatedAt,
      fiveHour,
      weekly,
      nextResetAfterSeconds:
        topNextResetAfterSeconds ??
        (bucketNextResetAfterCandidates.length > 0
          ? Math.min(...bucketNextResetAfterCandidates)
          : null),
      nextResetAt:
        topNextResetAt ??
        (bucketNextResetAtCandidates.length > 0
          ? Math.min(...bucketNextResetAtCandidates)
          : null),
      guardMode:
        typeof health.guard_mode === 'string' ? health.guard_mode : null,
      buckets,
      accounts,
    }
  } catch {
    return null
  }
}

function formatPercent(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return '-'
  const rounded = Math.round(value * 10) / 10
  return `${Number.isInteger(rounded) ? rounded.toFixed(0) : rounded.toFixed(1)}%`
}

function formatShortDuration(seconds: number | null | undefined): string {
  if (seconds == null || !Number.isFinite(seconds)) return '-'
  const totalSeconds = Math.max(0, Math.round(seconds))
  const days = Math.floor(totalSeconds / 86400)
  const hours = Math.floor((totalSeconds % 86400) / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m`
  return `${totalSeconds}s`
}

function formatCompactTimestamp(timestamp: number | null | undefined): string {
  if (timestamp == null || !Number.isFinite(timestamp) || timestamp <= 0) {
    return '-'
  }
  const date = new Date(timestamp * 1000)
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  return `${month}-${day} ${hours}:${minutes}`
}

function formatCliproxyCPAUnits(value: number | null | undefined): string {
  return value == null || !Number.isFinite(value) ? '-' : formatBalance(value)
}

function formatResetCreditsAvailable(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return '-'
  return String(Math.max(0, Math.trunc(value)))
}

function isCliproxyCPAAccountAvailable(
  account: CliproxyCPAQuotaAccount
): boolean {
  if (account.state) return account.state === 'available'
  if (account.schedulable != null) return account.schedulable
  return (
    account.ok === true &&
    account.skipped !== true &&
    account.unavailable !== true &&
    account.disabled !== true
  )
}

function getCliproxyCPAAccountIssue(account: CliproxyCPAQuotaAccount): string {
  if (account.state === 'manual_disabled') return '手动禁用'
  if (account.state === 'quota_7d_exhausted') return '7d 周额度已用完'
  if (account.state === 'quota_5h_exhausted') return '5h 额度已用完'
  if (account.state === 'protected_reserve') return '低水位保护'
  if (account.state === 'auth_invalid') return '需要重新登录'
  if (account.state === 'cooldown') return '冷却中'
  if (account.disabled === true) return '已禁用'
  if (
    account.quotaExhaustedWindow === '7d' ||
    account.reason === 'quota_7d_exhausted'
  ) {
    return '7d 周额度已用完'
  }
  if (
    account.quotaExhaustedWindow === '5h' ||
    account.reason === 'quota_5h_exhausted'
  ) {
    return '5h 额度已用完'
  }
  if (account.reason === 'protected_reserve_reached') return '低水位保护'
  if (account.unavailable === true || account.runtimeUnavailable === true) {
    return 'CPA 暂停调度'
  }
  if (account.skipped === true) {
    if (account.reason === 'auth_unavailable') return 'CPA 暂停调度'
    return '已跳过'
  }
  if (account.ok === false) return '不可用'
  if (account.state === 'unknown') return '状态未知'
  return '状态异常'
}

function getCliproxyCPAAccountIssueDetail(
  account: CliproxyCPAQuotaAccount,
  updatedAt: number | null
): string | null {
  const quotaWindow =
    account.quotaExhaustedWindow === '7d' ||
    account.reason === 'quota_7d_exhausted'
      ? account.weekly
      : account.quotaExhaustedWindow === '5h' ||
          account.reason === 'quota_5h_exhausted'
        ? account.fiveHour
        : null
  if (quotaWindow) {
    const resetAt = getCliproxyCPAResetAt(quotaWindow, updatedAt)
    if (resetAt != null) return `重置 ${formatCompactTimestamp(resetAt)}`
    if (quotaWindow.resetAfterSeconds != null) {
      return `重置 ${formatShortDuration(quotaWindow.resetAfterSeconds)}`
    }
  }
  if (account.resetAt != null) return `恢复 ${formatCompactTimestamp(account.resetAt)}`
  if (account.reason === 'protected_reserve_reached') return '保留共享 Pro 余量'
  if (account.state === 'auth_invalid') return account.lastError || 'OAuth 失效'
  if (account.state === 'protected_reserve') return '保留共享 Pro 余量'
  if (account.state === 'cooldown') return account.lastError || account.reason
  if (
    account.reason === 'auth_unavailable' &&
    account.ok === true &&
    (account.runtimeUnavailable === true || account.unavailable === true)
  ) {
    return '额度可读，等待 CPA 恢复调度'
  }
  return account.reason || account.lastError || account.error || null
}

function getCliproxyCPAAccountPlanLabel(
  account: CliproxyCPAQuotaAccount
): string | null {
  if (account.planType && account.planType.trim() !== '')
    return account.planType
  if (account.canExhaust === true) return '个人池'
  if (account.canExhaust === false) return '共享 Pro'
  return null
}

function clampPercent(value: number | null | undefined): number {
  if (value == null || !Number.isFinite(value)) return 0
  return Math.min(100, Math.max(0, value))
}

function getCliproxyCPAProgressColor(value: number | null | undefined): string {
  const percent = clampPercent(value)
  if (percent <= 10) return '[&_[data-slot=progress-indicator]]:bg-rose-500'
  if (percent <= 30) return '[&_[data-slot=progress-indicator]]:bg-amber-500'
  return '[&_[data-slot=progress-indicator]]:bg-emerald-500'
}

function getCliproxyCPABucketAccountCountLabel(
  bucket: CliproxyCPAQuotaBucket
): string | null {
  if (bucket.accountCount == null) return null
  if (bucket.availableAccountCount == null) return `总 ${bucket.accountCount}`
  return `可用 ${bucket.availableAccountCount} / 总 ${bucket.accountCount}`
}

function formatCliproxyCPABucketSummary(
  bucket: CliproxyCPAQuotaBucket
): string {
  const count = getCliproxyCPABucketAccountCountLabel(bucket)
  const prefix = count ? `${bucket.label} ${count}` : bucket.label
  return `${prefix} ${formatCliproxyCPAUnits(bucket.usableBalanceUnits)}`
}

function formatCliproxyCPASummary(meta: CliproxyCPAQuotaMeta): string {
  if (meta.buckets.length > 0) {
    const personal = meta.buckets.find(
      (bucket) => bucket.key === 'personal' || bucket.canExhaust
    )
    const protectedBucket = meta.buckets.find(
      (bucket) => bucket.key === 'protected' || bucket.canExhaust === false
    )
    const otherBuckets = meta.buckets.filter(
      (bucket) => bucket !== personal && bucket !== protectedBucket
    )
    const parts = [personal, protectedBucket, ...otherBuckets]
      .filter((bucket): bucket is CliproxyCPAQuotaBucket => bucket != null)
      .map(formatCliproxyCPABucketSummary)
    if (meta.nextResetAt != null) {
      parts.push(`next ${formatCompactTimestamp(meta.nextResetAt)}`)
    } else if (meta.nextResetAfterSeconds != null) {
      parts.push(`reset ${formatShortDuration(meta.nextResetAfterSeconds)}`)
    }
    return parts.join(' · ')
  }
  const fiveHourPercent =
    meta.guardMode === 'low_watermark'
      ? meta.fiveHour?.remainingPercent
      : meta.fiveHour?.shareRemainingPercent
  const weeklyPercent =
    meta.guardMode === 'low_watermark'
      ? meta.weekly?.remainingPercent
      : meta.weekly?.shareRemainingPercent
  const parts = [
    `5h ${formatPercent(fiveHourPercent)}`,
    `7d ${formatPercent(weeklyPercent)}`,
  ]
  if (meta.nextResetAt != null) {
    parts.push(`next ${formatCompactTimestamp(meta.nextResetAt)}`)
  } else if (meta.nextResetAfterSeconds != null) {
    parts.push(`reset ${formatShortDuration(meta.nextResetAfterSeconds)}`)
  }
  return parts.join(' · ')
}

/**
 * Render limited items with "and X more" indicator
 */
const SENSITIVE_MASK = '••••'
const CPA_TOOLTIP_CONTENT_CLASS =
  'max-w-none border border-border bg-background p-3 text-foreground shadow-xl'

function renderLimitedItems(
  items: React.ReactNode[],
  maxDisplay: number = 2
): React.ReactNode {
  if (items.length === 0)
    return <span className='text-muted-foreground text-xs'>-</span>

  const displayed = items.slice(0, maxDisplay)
  const remaining = items.length - maxDisplay

  return (
    <div className='flex max-w-full items-center gap-1 overflow-hidden'>
      {displayed}
      {remaining > 0 && (
        <StatusBadge
          label={`+${remaining}`}
          variant='neutral'
          size='sm'
          copyable={false}
          className='flex-shrink-0'
        />
      )}
    </div>
  )
}

/**
 * Upstream update tags (+N / -N) shown on channel name for model-fetchable channels
 */
function UpstreamUpdateTags({ channel }: { channel: Channel }) {
  const { upstream, setCurrentRow } = useChannels()
  if (!MODEL_FETCHABLE_TYPES.has(channel.type)) return null

  const meta = parseUpstreamUpdateMeta(channel.settings)
  if (!meta.enabled) return null

  const addCount = meta.pendingAddModels.length
  const removeCount = meta.pendingRemoveModels.length
  if (addCount === 0 && removeCount === 0) return null

  return (
    <div className='flex items-center gap-0.5'>
      {addCount > 0 && (
        <StatusBadge
          label={`+${addCount}`}
          variant='success'
          size='sm'
          copyable={false}
          className='cursor-pointer'
          onClick={(e: React.MouseEvent) => {
            e.stopPropagation()
            setCurrentRow(channel)
            upstream.openModal(
              channel,
              meta.pendingAddModels,
              meta.pendingRemoveModels,
              'add'
            )
          }}
        />
      )}
      {removeCount > 0 && (
        <StatusBadge
          label={`-${removeCount}`}
          variant='danger'
          size='sm'
          copyable={false}
          className='cursor-pointer'
          onClick={(e: React.MouseEvent) => {
            e.stopPropagation()
            setCurrentRow(channel)
            upstream.openModal(
              channel,
              meta.pendingAddModels,
              meta.pendingRemoveModels,
              'remove'
            )
          }}
        />
      )}
    </div>
  )
}

function CliproxyCPAQuotaProgress({
  label,
  window,
  updatedAt,
}: {
  label: string
  window: CliproxyCPAQuotaWindow | null
  updatedAt: number | null
}) {
  const percent = window?.remainingPercent
  const resetAt = getCliproxyCPAResetAt(window, updatedAt)

  return (
    <div className='space-y-1'>
      <div className='flex items-center justify-between gap-2 text-[11px] leading-none'>
        <span className='text-foreground/75 font-medium'>{label}</span>
        <span className='flex shrink-0 items-center gap-1 tabular-nums'>
          <span className='text-foreground font-semibold'>
            {formatPercent(percent)}
          </span>
          <span className='text-foreground/70'>
            {resetAt != null ? formatCompactTimestamp(resetAt) : '-'}
          </span>
        </span>
      </div>
      <Progress
        value={clampPercent(percent)}
        className={cn(
          '[&_[data-slot=progress-track]]:bg-foreground/20 h-1.5',
          getCliproxyCPAProgressColor(percent)
        )}
      />
    </div>
  )
}

function CliproxyCPAAccountWindowSummary({
  account,
  updatedAt,
}: {
  account: CliproxyCPAQuotaAccount
  updatedAt: number | null
}) {
  const fiveHourResetAt = getCliproxyCPAResetAt(account.fiveHour, updatedAt)
  const weeklyResetAt = getCliproxyCPAResetAt(account.weekly, updatedAt)

  return (
    <div className='text-foreground/70 flex flex-wrap gap-x-2 gap-y-0.5 text-[10px] tabular-nums'>
      <span>5h {formatPercent(account.fiveHour?.remainingPercent)}</span>
      {fiveHourResetAt != null && (
        <span>reset {formatCompactTimestamp(fiveHourResetAt)}</span>
      )}
      <span>7d {formatPercent(account.weekly?.remainingPercent)}</span>
      {weeklyResetAt != null && (
        <span>reset {formatCompactTimestamp(weeklyResetAt)}</span>
      )}
    </div>
  )
}

function CliproxyCPAResetCreditButton({
  channelId,
  account,
}: {
  channelId: number
  account: CliproxyCPAQuotaAccount
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [isResetting, setIsResetting] = useState(false)
  const resetCredits = account.resetCreditsAvailable ?? 0
  const disabled = resetCredits <= 0 || isResetting || account.ok === false

  const handleConfirm = async () => {
    setIsResetting(true)
    try {
      const res = await consumeCliproxyCPAResetCredit(
        channelId,
        account.authIndex
      )
      if (!res.success) {
        throw new Error(res.message || t('Reset request failed'))
      }
      toast.success(
        t('Reset request sent. Quota display will refresh after CPA polling.')
      )
      queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
      setConfirmOpen(false)
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Reset request failed')
      )
    } finally {
      setIsResetting(false)
    }
  }

  return (
    <>
      <Button
        type='button'
        variant='outline'
        size='xs'
        disabled={disabled}
        className='h-6 px-1.5 text-[11px]'
        onClick={(event: React.MouseEvent<HTMLButtonElement>) => {
          event.preventDefault()
          event.stopPropagation()
          setConfirmOpen(true)
        }}
      >
        <RotateCcw className='size-3' />
        {isResetting ? t('Resetting') : t('主动重置')}
      </Button>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Consume reset credit?')}
        desc={
          <div className='space-y-1 text-sm'>
            <p>
              {t(
                'This will consume one upstream reset credit for this CPA account.'
              )}
            </p>
            <p className='text-muted-foreground'>
              {t(
                'The upstream API does not guarantee this only resets the 5h window; the actual affected quota window is controlled by OpenAI.'
              )}
            </p>
            <p className='text-muted-foreground'>
              {account.label} · {t('Remaining reset credits')}{' '}
              {formatResetCreditsAvailable(account.resetCreditsAvailable)}
            </p>
          </div>
        }
        confirmText={isResetting ? t('Resetting') : t('Confirm reset')}
        isLoading={isResetting}
        handleConfirm={handleConfirm}
      />
    </>
  )
}

function CliproxyCPALocalResetButton({
  channelId,
  account,
}: {
  channelId: number
  account: CliproxyCPAQuotaAccount
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [isResetting, setIsResetting] = useState(false)
  const disabled = isResetting || account.disabled === true

  const handleClick = async (event: React.MouseEvent<HTMLButtonElement>) => {
    event.preventDefault()
    event.stopPropagation()
    setIsResetting(true)
    try {
      const res = await resetCliproxyCPAQuotaState(channelId, account.authIndex)
      if (!res.success) {
        throw new Error(res.message || t('Reset request failed'))
      }
      toast.success(t('Local CPA cooldown and quota state cleared.'))
      queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : t('Reset request failed')
      )
    } finally {
      setIsResetting(false)
    }
  }

  return (
    <Button
      type='button'
      variant='outline'
      size='xs'
      disabled={disabled}
      className='h-6 px-1.5 text-[11px]'
      onClick={handleClick}
    >
      <RotateCcw className='size-3' />
      {isResetting ? t('Resetting') : t('清本地状态')}
    </Button>
  )
}

function CliproxyCPAAccountRow({
  account,
  channelId,
  updatedAt,
  unavailable = false,
}: {
  account: CliproxyCPAQuotaAccount
  channelId: number
  updatedAt: number | null
  unavailable?: boolean
}) {
  const issue = unavailable ? getCliproxyCPAAccountIssue(account) : null
  const issueDetail = unavailable
    ? getCliproxyCPAAccountIssueDetail(account, updatedAt)
    : null
  const planLabel = getCliproxyCPAAccountPlanLabel(account)
  const canReset =
    account.disabled !== true && (account.resetCreditsAvailable ?? 0) > 0

  return (
    <div className='bg-muted/30 border-border/80 space-y-1 rounded border p-1.5'>
      <div className='flex items-start justify-between gap-2'>
        <div className='min-w-0'>
          <p className='text-foreground truncate text-[11px] font-medium'>
            {account.label}
          </p>
          {unavailable ? (
            <p className='text-destructive text-[10px]'>
              {issue}
              {issueDetail ? ` · ${issueDetail}` : ''}
              {planLabel ? ` · ${planLabel}` : ''}
            </p>
          ) : (
            <p className='text-foreground/70 text-[10px]'>
              reset credits{' '}
              <span className='tabular-nums'>
                {formatResetCreditsAvailable(account.resetCreditsAvailable)}
              </span>
            </p>
          )}
        </div>
        <div className='flex shrink-0 flex-wrap justify-end gap-1'>
          {unavailable && (
            <CliproxyCPALocalResetButton
              channelId={channelId}
              account={account}
            />
          )}
          {canReset && (
            <CliproxyCPAResetCreditButton
              channelId={channelId}
              account={account}
            />
          )}
        </div>
      </div>
      <CliproxyCPAAccountWindowSummary
        account={account}
        updatedAt={updatedAt}
      />
    </div>
  )
}

function filterCliproxyCPAAccountsForBucket(
  accounts: CliproxyCPAQuotaAccount[],
  bucket: CliproxyCPAQuotaBucket
): CliproxyCPAQuotaAccount[] {
  return accounts.filter((account) => {
    if (!isCliproxyCPAAccountAvailable(account)) return false
    if (account.bucket && account.bucket === bucket.key) return true
    return account.bucket == null && account.canExhaust === bucket.canExhaust
  })
}

function CliproxyCPABucketDetails({
  bucket,
  accounts,
  channelId,
  updatedAt,
}: {
  bucket: CliproxyCPAQuotaBucket
  accounts: CliproxyCPAQuotaAccount[]
  channelId: number
  updatedAt: number | null
}) {
  const count = getCliproxyCPABucketAccountCountLabel(bucket)
  const rawBalanceVisible =
    bucket.balanceUnits != null &&
    bucket.usableBalanceUnits != null &&
    Math.abs(bucket.balanceUnits - bucket.usableBalanceUnits) > 0.000001

  return (
    <div className='bg-background text-foreground border-border min-w-[280px] space-y-2 rounded-md border p-2 shadow-sm'>
      <div className='flex items-start justify-between gap-3'>
        <div className='min-w-0'>
          <p className='truncate text-xs font-semibold'>
            {bucket.label}
            {count ? ` · ${count}` : ''}
          </p>
          {bucket.canExhaust === false && (
            <p className='text-foreground/70 text-[11px]'>
              reserve 5h {formatPercent(bucket.reserveFiveHourPercent)} / 7d{' '}
              {formatPercent(bucket.reserveWeeklyPercent)}
            </p>
          )}
        </div>
        <div className='shrink-0 text-right tabular-nums'>
          <p className='text-xs font-semibold'>
            {formatCliproxyCPAUnits(bucket.usableBalanceUnits)}
          </p>
          <p className='text-foreground/70 text-[11px]'>usable</p>
        </div>
      </div>
      {rawBalanceVisible && (
        <div className='text-foreground/70 flex justify-between gap-2 text-[11px]'>
          <span>raw remaining</span>
          <span className='tabular-nums'>
            {formatCliproxyCPAUnits(bucket.balanceUnits)}
          </span>
        </div>
      )}
      <CliproxyCPAQuotaProgress
        label='5h remaining'
        window={bucket.fiveHour}
        updatedAt={updatedAt}
      />
      <CliproxyCPAQuotaProgress
        label='7d remaining'
        window={bucket.weekly}
        updatedAt={updatedAt}
      />
      {accounts.length > 0 && (
        <div className='border-border/70 space-y-1 border-t pt-2'>
          <p className='text-foreground/70 text-[11px]'>可用账号</p>
          {accounts.map((account) => (
            <CliproxyCPAAccountRow
              key={account.authIndex}
              account={account}
              channelId={channelId}
              updatedAt={updatedAt}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function CliproxyCPAQuotaDetails({
  meta,
  channelId,
}: {
  meta: CliproxyCPAQuotaMeta
  channelId: number
}) {
  const unavailableAccounts = meta.accounts.filter(
    (account) => !isCliproxyCPAAccountAvailable(account)
  )

  return (
    <div className='text-foreground w-[360px] max-w-[calc(100vw-2rem)] space-y-2'>
      <div className='grid grid-cols-3 gap-2'>
        <div className='bg-background border-border rounded-md border p-2 shadow-sm'>
          <p className='text-foreground/70 text-[11px]'>CPA usable</p>
          <p className='text-sm font-semibold tabular-nums'>
            {formatCliproxyCPAUnits(meta.usableBalanceUnits)}
          </p>
        </div>
        <div className='bg-background border-border rounded-md border p-2 shadow-sm'>
          <p className='text-foreground/70 text-[11px]'>Total</p>
          <p className='text-sm font-semibold tabular-nums'>
            {formatCliproxyCPAUnits(meta.totalBalanceUnits)}
          </p>
        </div>
        <div className='bg-background border-border rounded-md border p-2 shadow-sm'>
          <p className='text-foreground/70 text-[11px]'>可用/总账号</p>
          <p className='text-sm font-semibold tabular-nums'>
            {meta.accountCount == null
              ? '-'
              : meta.availableAccountCount != null
                ? `${meta.availableAccountCount}/${meta.accountCount}`
                : meta.accountCount}
          </p>
        </div>
      </div>
      {meta.buckets.length > 0 ? (
        <div className='space-y-2'>
          {meta.buckets.map((bucket) => (
            <CliproxyCPABucketDetails
              key={bucket.key}
              bucket={bucket}
              accounts={filterCliproxyCPAAccountsForBucket(
                meta.accounts,
                bucket
              )}
              channelId={channelId}
              updatedAt={meta.updatedAt}
            />
          ))}
        </div>
      ) : (
        <div className='bg-background border-border space-y-2 rounded-md border p-2 shadow-sm'>
          {meta.guardMode !== 'low_watermark' && (
            <div className='text-foreground/70 flex justify-between gap-2 text-[11px]'>
              <span>CPA share</span>
              <span className='tabular-nums'>
                {formatPercent(meta.remainingSharePercent)} /{' '}
                {formatPercent(meta.shareLimitPercent)}
              </span>
            </div>
          )}
          <CliproxyCPAQuotaProgress
            label='5h remaining'
            window={meta.fiveHour}
            updatedAt={meta.updatedAt}
          />
          <CliproxyCPAQuotaProgress
            label='7d remaining'
            window={meta.weekly}
            updatedAt={meta.updatedAt}
          />
          {meta.accounts.length > 0 && (
            <div className='border-border/70 space-y-1 border-t pt-2'>
              <p className='text-foreground/70 text-[11px]'>Accounts</p>
              {meta.accounts.map((account) => (
                <CliproxyCPAAccountRow
                  key={account.authIndex}
                  account={account}
                  channelId={channelId}
                  updatedAt={meta.updatedAt}
                />
              ))}
            </div>
          )}
        </div>
      )}
      {unavailableAccounts.length > 0 && (
        <div className='bg-background border-border space-y-2 rounded-md border p-2 shadow-sm'>
          <div className='flex items-center justify-between gap-2'>
            <p className='text-xs font-semibold'>
              不可用账号 · {unavailableAccounts.length}
            </p>
            <p className='text-foreground/70 text-[11px]'>
              额度耗尽或 CPA 暂停调度
            </p>
          </div>
          <div className='space-y-1'>
            {unavailableAccounts.map((account) => (
              <CliproxyCPAAccountRow
                key={account.authIndex}
                account={account}
                channelId={channelId}
                updatedAt={meta.updatedAt}
                unavailable
              />
            ))}
          </div>
        </div>
      )}
      <div className='text-foreground/70 flex justify-between gap-2 text-[11px]'>
        <span>Next reset</span>
        <span className='tabular-nums'>
          {meta.nextResetAt != null
            ? formatCompactTimestamp(meta.nextResetAt)
            : formatShortDuration(meta.nextResetAfterSeconds)}
        </span>
      </div>
      {meta.updatedAt && (
        <div className='text-foreground/70 flex justify-between gap-2 text-[11px]'>
          <span>Updated</span>
          <span className='tabular-nums'>
            {formatCompactTimestamp(meta.updatedAt)}
          </span>
        </div>
      )}
    </div>
  )
}

/**
 * Priority cell component with inline editing
 */
function PriorityCell({ channel }: { channel: Channel }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isTagRow = isTagAggregateRow(channel)
  const priority = channel.priority
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [pendingValue, setPendingValue] = useState<number | null>(null)

  // Tag row - editable with confirmation for all tag channels
  if (isTagRow) {
    const tag = channel.tag || ''
    const channelCount = channel.children?.length || 0

    return (
      <>
        <NumericSpinnerInput
          value={priority ?? 0}
          onChange={(value) => {
            setPendingValue(value)
            setConfirmOpen(true)
          }}
          min={-999}
        />
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={t('Confirm Batch Update')}
          desc={`This will update the priority to ${pendingValue} for all ${channelCount} channel(s) with tag "${tag}". Continue?`}
          confirmText='Update'
          handleConfirm={() => {
            if (pendingValue !== null) {
              handleUpdateTagField(tag, 'priority', pendingValue, queryClient)
            }
            setConfirmOpen(false)
          }}
        />
      </>
    )
  }

  // Regular channel row - editable
  return (
    <NumericSpinnerInput
      value={priority ?? 0}
      onChange={(value) => {
        handleUpdateChannelField(channel.id, 'priority', value, queryClient)
      }}
      min={-999}
    />
  )
}

/**
 * Weight cell component with inline editing
 */
function WeightCell({ channel }: { channel: Channel }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isTagRow = isTagAggregateRow(channel)
  const weight = channel.weight
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [pendingValue, setPendingValue] = useState<number | null>(null)

  // Tag row - editable with confirmation for all tag channels
  if (isTagRow) {
    const tag = channel.tag || ''
    const channelCount = channel.children?.length || 0

    return (
      <>
        <NumericSpinnerInput
          value={weight ?? 0}
          onChange={(value) => {
            setPendingValue(value)
            setConfirmOpen(true)
          }}
          min={0}
        />
        <ConfirmDialog
          open={confirmOpen}
          onOpenChange={setConfirmOpen}
          title={t('Confirm Batch Update')}
          desc={`This will update the weight to ${pendingValue} for all ${channelCount} channel(s) with tag "${tag}". Continue?`}
          confirmText='Update'
          handleConfirm={() => {
            if (pendingValue !== null) {
              handleUpdateTagField(tag, 'weight', pendingValue, queryClient)
            }
            setConfirmOpen(false)
          }}
        />
      </>
    )
  }

  // Regular channel row - editable
  return (
    <NumericSpinnerInput
      value={weight ?? 0}
      onChange={(value) => {
        handleUpdateChannelField(channel.id, 'weight', value, queryClient)
      }}
      min={0}
    />
  )
}

/**
 * Balance cell component with click to update
 */
function BalanceCell({ channel }: { channel: Channel }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { sensitiveVisible } = useChannels()
  const isTagRow = isTagAggregateRow(channel)
  const balance = channel.balance || 0
  const usedQuota = channel.used_quota || 0
  const [isUpdating, setIsUpdating] = useState(false)
  const [codexUsageOpen, setCodexUsageOpen] = useState(false)
  const [codexUsageResponse, setCodexUsageResponse] =
    useState<CodexUsageDialogData | null>(null)
  const currencyLabel = getCurrencyLabel()
  const tokenSuffix = currencyLabel === 'Tokens' ? ' Tokens' : ''
  const withSuffix = (value: string) =>
    tokenSuffix && value !== '-' ? `${value}${tokenSuffix}` : value

  const usedDisplay = withSuffix(formatQuotaValue(usedQuota))
  const remainingDisplay = withSuffix(formatBalance(balance))
  const maskedUsedLabel = `${t('Used:')} ${SENSITIVE_MASK}`
  const maskedRemainingLabel = `${t('Remaining:')} ${SENSITIVE_MASK}`
  const cliproxyCPAQuota = parseCliproxyCPAQuotaMeta(channel.other_info)

  // Tag row: only show cumulative used quota
  if (isTagRow) {
    return (
      <StatusBadge
        label={sensitiveVisible ? `Used: ${usedDisplay}` : maskedUsedLabel}
        variant='neutral'
        size='sm'
        copyable={false}
      />
    )
  }

  // Regular channel row: show used and remaining with click to update
  const variant = getBalanceVariant(balance)

  const handleClickUpdate = async () => {
    if (isUpdating) return

    setIsUpdating(true)
    if (channel.type === 57) {
      try {
        const res = await getCodexUsage(channel.id)
        if (!res.success) {
          throw new Error(res.message || t('Failed to fetch usage'))
        }
        setCodexUsageResponse(res)
        setCodexUsageOpen(true)
      } catch (error) {
        toast.error(
          error instanceof Error ? error.message : t('Failed to fetch usage')
        )
      } finally {
        setIsUpdating(false)
      }
      return
    }

    await handleUpdateChannelBalance(channel.id, queryClient)
    setIsUpdating(false)
  }

  return (
    <TooltipProvider>
      <div
        className={cn(
          'flex text-xs font-medium',
          cliproxyCPAQuota
            ? 'flex-col items-start gap-0.5'
            : 'items-center gap-1.5'
        )}
      >
        <div className='flex items-center gap-1.5'>
          <span
            className={cn(
              'size-1.5 shrink-0 rounded-full',
              dotColorMap[isUpdating ? 'neutral' : variant]
            )}
            aria-hidden='true'
          />
          <Tooltip>
            <TooltipTrigger
              render={<span className='text-muted-foreground cursor-help' />}
            >
              {sensitiveVisible ? usedDisplay : SENSITIVE_MASK}
            </TooltipTrigger>
            <TooltipContent>
              <p>
                {sensitiveVisible
                  ? `${t('Used:')} ${usedDisplay}`
                  : maskedUsedLabel}
              </p>
            </TooltipContent>
          </Tooltip>
          <span className='text-muted-foreground/30'>·</span>
          <Tooltip>
            <TooltipTrigger
              render={
                <span
                  className={cn(
                    'cursor-pointer transition-opacity hover:opacity-70',
                    channel.type === 57
                      ? 'text-primary'
                      : textColorMap[isUpdating ? 'neutral' : variant]
                  )}
                  onClick={handleClickUpdate}
                />
              }
            >
              {sensitiveVisible
                ? isUpdating
                  ? 'Updating...'
                  : channel.type === 57
                    ? t('Account Info')
                    : remainingDisplay
                : SENSITIVE_MASK}
            </TooltipTrigger>
            <TooltipContent
              className={
                cliproxyCPAQuota ? CPA_TOOLTIP_CONTENT_CLASS : undefined
              }
            >
              <p>
                {sensitiveVisible
                  ? channel.type === 57
                    ? t('Click to view Codex usage')
                    : cliproxyCPAQuota
                      ? `CPA usable: ${remainingDisplay}`
                      : `${t('Remaining:')} ${remainingDisplay}`
                  : maskedRemainingLabel}
              </p>
              {cliproxyCPAQuota && (
                <CliproxyCPAQuotaDetails
                  meta={cliproxyCPAQuota}
                  channelId={channel.id}
                />
              )}
              {channel.type !== 57 && <p>{t('Click to update balance')}</p>}
            </TooltipContent>
          </Tooltip>
        </div>
        {cliproxyCPAQuota && (
          <Tooltip>
            <TooltipTrigger
              render={
                <span className='text-muted-foreground max-w-[230px] cursor-help truncate text-[11px] leading-none font-normal' />
              }
            >
              {formatCliproxyCPASummary(cliproxyCPAQuota)}
            </TooltipTrigger>
            <TooltipContent className={CPA_TOOLTIP_CONTENT_CLASS}>
              <CliproxyCPAQuotaDetails
                meta={cliproxyCPAQuota}
                channelId={channel.id}
              />
            </TooltipContent>
          </Tooltip>
        )}
      </div>

      <CodexUsageDialog
        open={codexUsageOpen}
        onOpenChange={setCodexUsageOpen}
        channelName={channel.name}
        channelId={channel.id}
        channelDisplayName={sensitiveVisible ? undefined : SENSITIVE_MASK}
        channelDisplayId={sensitiveVisible ? undefined : SENSITIVE_MASK}
        response={codexUsageResponse}
        onRefresh={async () => {
          if (isUpdating) return
          setIsUpdating(true)
          try {
            const res = await getCodexUsage(channel.id)
            if (!res.success) {
              throw new Error(res.message || t('Failed to fetch usage'))
            }
            setCodexUsageResponse(res)
          } catch (error) {
            toast.error(
              error instanceof Error
                ? error.message
                : t('Failed to fetch usage')
            )
          } finally {
            setIsUpdating(false)
          }
        }}
        isRefreshing={isUpdating}
      />
    </TooltipProvider>
  )
}

/**
 * Generate channels columns configuration
 */
export function useChannelsColumns(): ColumnDef<Channel>[] {
  const { t } = useTranslation()
  const { sensitiveVisible } = useChannels()
  return [
    // Checkbox column
    {
      id: 'select',
      header: ({ table }) => (
        <Checkbox
          checked={table.getIsAllPageRowsSelected()}
          indeterminate={table.getIsSomePageRowsSelected()}
          onCheckedChange={(value) => table.toggleAllPageRowsSelected(!!value)}
          aria-label='Select all'
        />
      ),
      cell: ({ row }) => {
        const isTagRow = isTagAggregateRow(row.original)

        // Don't show checkbox for tag rows
        if (isTagRow) {
          return null
        }

        return (
          <Checkbox
            checked={row.getIsSelected()}
            onCheckedChange={(value) => row.toggleSelected(!!value)}
            aria-label='Select row'
          />
        )
      },
      enableSorting: false,
      enableHiding: false,
      size: 40,
    },

    // ID column
    {
      accessorKey: 'id',
      meta: { label: t('ID'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title='ID' />
      ),
      cell: ({ row }) => {
        const id = row.getValue('id') as number
        const displayId = sensitiveVisible ? String(id) : SENSITIVE_MASK
        return (
          <StatusBadge
            label={displayId}
            variant='neutral'
            copyText={displayId}
            size='sm'
            className='font-mono'
          />
        )
      },
      size: 80,
    },

    // Name column
    {
      accessorKey: 'name',
      meta: { label: t('Name'), mobileTitle: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Name')} />
      ),
      cell: ({ row }) => {
        const isTagRow = isTagAggregateRow(row.original)
        const name = row.getValue('name') as string
        const channel = row.original
        const isMultiKey = isMultiKeyChannel(channel)

        // Tag row with expand/collapse
        if (isTagRow) {
          const tag = (row.original as TagRow).tag || name
          const childrenCount = (row.original as TagRow).children?.length || 0

          return (
            <div className='flex items-center gap-2'>
              <Button
                variant='ghost'
                size='sm'
                className='h-6 w-6 p-0'
                onClick={row.getToggleExpandedHandler()}
              >
                {row.getIsExpanded() ? (
                  <ChevronDown className='h-4 w-4' />
                ) : (
                  <ChevronRight className='h-4 w-4' />
                )}
              </Button>
              <div className='flex items-center gap-1.5'>
                <span className='font-semibold'>Tag：{tag}</span>
                <StatusBadge
                  label={`${childrenCount} channels`}
                  variant='blue'
                  size='sm'
                  copyable={false}
                />
              </div>
            </div>
          )
        }

        // Regular channel row
        const settings = parseChannelSettings(channel.setting)
        const isPassThrough = settings.pass_through_body_enabled === true

        return (
          <div className='flex items-center gap-2'>
            <div className='flex flex-col gap-1'>
              <div className='flex items-center gap-1.5'>
                <span className='font-medium'>{truncateText(name, 30)}</span>
                {isPassThrough && (
                  <TooltipProvider delay={100}>
                    <Tooltip>
                      <TooltipTrigger
                        render={
                          <AlertTriangle className='h-3.5 w-3.5 flex-shrink-0 text-amber-500' />
                        }
                      ></TooltipTrigger>
                      <TooltipContent side='top'>
                        {t(
                          'Request body pass-through is enabled. The request body will be sent directly to the upstream without any conversion.'
                        )}
                      </TooltipContent>
                    </Tooltip>
                  </TooltipProvider>
                )}
                {isMultiKey && (
                  <StatusBadge
                    label={`${channel.channel_info.multi_key_size} keys`}
                    variant='purple'
                    size='sm'
                    copyable={false}
                  />
                )}
                <UpstreamUpdateTags channel={channel} />
              </div>
              {channel.remark && (
                <TooltipProvider delay={200}>
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <span className='text-muted-foreground text-xs' />
                      }
                    >
                      {truncateText(channel.remark, 40)}
                    </TooltipTrigger>
                    <TooltipContent side='bottom' className='max-w-xs'>
                      {channel.remark}
                    </TooltipContent>
                  </Tooltip>
                </TooltipProvider>
              )}
            </div>
          </div>
        )
      },
      minSize: 200,
    },

    // Type column
    {
      accessorKey: 'type',
      meta: { label: t('Type') },
      header: t('Type'),
      cell: ({ row }) => {
        const isTagRow = isTagAggregateRow(row.original)

        if (isTagRow) {
          return (
            <StatusBadge
              label={t('Tag Aggregate')}
              variant='blue'
              size='sm'
              copyable={false}
            />
          )
        }

        const type = row.getValue('type') as number
        const typeNameKey = getChannelTypeLabel(type)
        const typeName = t(typeNameKey)
        const iconName = getChannelTypeIcon(type)
        const icon = getLobeIcon(`${iconName}.Color`, 20)
        const channel = row.original as Channel
        const isMultiKey = isMultiKeyChannel(channel)
        const multiKeyMode = channel.channel_info?.multi_key_mode ?? 'random'
        const MultiKeyModeIcon =
          multiKeyMode === 'random' ? Shuffle : ListOrdered
        const multiKeyTooltip =
          multiKeyMode === 'random'
            ? t('Multi-key: Random rotation')
            : t('Multi-key: Polling rotation')

        const ionetMeta = parseIonetMeta(channel.other_info)
        const isIonet = ionetMeta?.source === 'ionet'
        const deploymentId =
          typeof ionetMeta?.deployment_id === 'string'
            ? ionetMeta?.deployment_id
            : undefined

        return (
          <div className='flex items-center gap-2'>
            <div className='flex items-center gap-1.5'>
              {isMultiKey && (
                <TooltipProvider delay={100}>
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <span className='border-border bg-muted text-primary inline-flex h-6 w-6 items-center justify-center rounded-md border' />
                      }
                    >
                      <MultiKeyModeIcon className='h-3.5 w-3.5' />
                    </TooltipTrigger>
                    <TooltipContent side='top'>
                      {multiKeyTooltip}
                    </TooltipContent>
                  </Tooltip>
                </TooltipProvider>
              )}
              {icon}
            </div>
            <StatusBadge
              label={typeName}
              autoColor={typeName}
              size='sm'
              copyable={false}
            />
            {isIonet && (
              <TooltipProvider delay={100}>
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <span
                        className='flex cursor-pointer items-center gap-1.5 text-xs font-medium'
                        onClick={(e) => {
                          e.stopPropagation()
                          if (!deploymentId) return
                          const targetUrl = `/console/deployment?deployment_id=${deploymentId}`
                          window.open(targetUrl, '_blank', 'noopener')
                        }}
                      />
                    }
                  >
                    <span className='text-muted-foreground/30'>·</span>
                    <span className={cn(textColorMap.purple)}>IO.NET</span>
                  </TooltipTrigger>
                  <TooltipContent side='top'>
                    <div className='max-w-xs space-y-1'>
                      <div className='text-xs'>
                        {t('From IO.NET deployment')}
                      </div>
                      {deploymentId && (
                        <div className='text-muted-foreground font-mono text-xs'>
                          {t('Deployment ID')}: {deploymentId}
                        </div>
                      )}
                      <div className='text-muted-foreground text-xs'>
                        {t('Click to open deployment')}
                      </div>
                    </div>
                  </TooltipContent>
                </Tooltip>
              </TooltipProvider>
            )}
          </div>
        )
      },
      filterFn: (row, id, value) => {
        if (!value || value.length === 0 || value.includes('all')) return true
        return value.includes(String(row.getValue(id)))
      },
      size: 140,
      enableSorting: false,
    },

    // Status column
    {
      accessorKey: 'status',
      meta: { label: t('Status'), mobileBadge: true },
      header: t('Status'),
      cell: ({ row }) => {
        const isTagRow = isTagAggregateRow(row.original)
        const status = row.getValue('status') as number
        const channel = row.original as Channel

        // Tag row: show aggregated status
        if (isTagRow) {
          const childrenCount = (row.original as TagRow).children?.length || 0
          const hasEnabled = status === 1

          if (hasEnabled) {
            return (
              <StatusBadge
                label={`Active (${childrenCount})`}
                variant='success'
                showDot
                size='sm'
                copyable={false}
              />
            )
          } else {
            return (
              <StatusBadge
                label={`Inactive (${childrenCount})`}
                variant='neutral'
                size='sm'
                copyable={false}
              />
            )
          }
        }

        // Regular channel row
        const config =
          CHANNEL_STATUS_CONFIG[status as keyof typeof CHANNEL_STATUS_CONFIG] ||
          CHANNEL_STATUS_CONFIG[0]

        const isMultiKey = isMultiKeyChannel(channel)
        const keySize = channel.channel_info?.multi_key_size ?? 0
        const disabledCount = channel.channel_info?.multi_key_status_list
          ? Object.keys(channel.channel_info.multi_key_status_list).length
          : 0
        const enabledCount = Math.max(0, keySize - disabledCount)
        const label =
          isMultiKey && keySize > 0
            ? `${t(config.label)} (${enabledCount}/${keySize})`
            : t(config.label)

        // Auto-disabled: show reason and time tooltip
        if (status === 3) {
          let statusReason = ''
          let statusTime = ''
          try {
            const otherInfo = channel.other_info
              ? JSON.parse(channel.other_info)
              : null
            if (otherInfo) {
              statusReason = otherInfo.status_reason || ''
              statusTime = otherInfo.status_time
                ? formatTimestampToDate(otherInfo.status_time)
                : ''
            }
          } catch {
            /* empty */
          }

          if (statusReason || statusTime) {
            return (
              <TooltipProvider delay={100}>
                <Tooltip>
                  <TooltipTrigger render={<span />}>
                    <StatusBadge
                      label={label}
                      variant={config.variant}
                      showDot={config.showDot}
                      size='sm'
                      copyable={false}
                    />
                  </TooltipTrigger>
                  <TooltipContent side='top' className='max-w-xs'>
                    <div className='space-y-1 text-xs'>
                      {statusReason && (
                        <div>
                          {t('Reason:')} {statusReason}
                        </div>
                      )}
                      {statusTime && (
                        <div>
                          {t('Time:')} {statusTime}
                        </div>
                      )}
                    </div>
                  </TooltipContent>
                </Tooltip>
              </TooltipProvider>
            )
          }
        }

        return (
          <StatusBadge
            label={label}
            variant={config.variant}
            showDot={config.showDot}
            size='sm'
            copyable={false}
          />
        )
      },
      filterFn: (row, id, value) => {
        if (!value || value.length === 0 || value.includes('all')) return true
        const status = row.getValue(id) as number
        if (value.includes('enabled')) return status === 1
        if (value.includes('disabled')) return status !== 1
        return false
      },
      size: 120,
      enableSorting: false,
    },

    // Models column
    {
      accessorKey: 'models',
      meta: { label: t('Models'), mobileHidden: true },
      header: t('Models'),
      cell: ({ row }) => {
        const models = row.getValue('models') as string
        const modelArray = parseModelsList(models)

        if (modelArray.length === 0) {
          return <span className='text-muted-foreground text-xs'>-</span>
        }

        const modelBadges = modelArray.map((model, idx) => (
          <StatusBadge
            key={idx}
            label={model}
            autoColor={model}
            size='sm'
            className='font-mono'
          />
        ))

        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger render={<div />}>
                {renderLimitedItems(modelBadges, 2)}
              </TooltipTrigger>
              {modelArray.length > 2 && (
                <TooltipContent
                  side='top'
                  className='border-border bg-popover max-h-48 max-w-[320px] overflow-y-auto p-2'
                >
                  <div className='flex flex-wrap gap-1'>{modelBadges}</div>
                </TooltipContent>
              )}
            </Tooltip>
          </TooltipProvider>
        )
      },
      size: 200,
      enableSorting: false,
    },

    // Group column
    {
      accessorKey: 'group',
      meta: { label: t('Groups'), mobileHidden: true },
      header: t('Groups'),
      cell: ({ row }) => {
        const group = row.getValue('group') as string
        const groupArray = parseGroupsList(group)

        const groupBadges = groupArray.map((g) => (
          <GroupBadge
            key={g}
            group={g}
            label={sensitiveVisible ? undefined : SENSITIVE_MASK}
            size='sm'
          />
        ))

        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger render={<div />}>
                {renderLimitedItems(groupBadges, 2)}
              </TooltipTrigger>
              {groupArray.length > 2 && (
                <TooltipContent
                  side='top'
                  className='border-border bg-popover max-h-48 max-w-[320px] overflow-y-auto p-2'
                >
                  <div className='flex flex-wrap gap-1'>{groupBadges}</div>
                </TooltipContent>
              )}
            </Tooltip>
          </TooltipProvider>
        )
      },
      filterFn: (row, id, value) => {
        if (!value || value.length === 0 || value.includes('all')) return true
        const group = row.getValue(id) as string
        const groupArray = parseGroupsList(group)
        return groupArray.some((g) => value.includes(g))
      },
      size: 150,
      enableSorting: false,
    },

    // Tag column
    {
      accessorKey: 'tag',
      meta: { label: t('Tag'), mobileHidden: true },
      header: t('Tag'),
      cell: ({ row }) => {
        const tag = row.getValue('tag') as string | null
        if (!tag)
          return <span className='text-muted-foreground text-xs'>-</span>

        return <StatusBadge label={tag} autoColor={tag} size='sm' />
      },
      size: 120,
      enableSorting: false,
    },

    // Priority column
    {
      accessorKey: 'priority',
      meta: { label: t('Priority'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Priority')} />
      ),
      cell: ({ row }) => <PriorityCell channel={row.original} />,
      size: 100,
    },

    // Weight column
    {
      accessorKey: 'weight',
      meta: { label: t('Weight'), mobileHidden: true },
      header: t('Weight'),
      cell: ({ row }) => <WeightCell channel={row.original} />,
      size: 90,
      enableSorting: false,
    },

    // Balance column (Used/Remaining)
    {
      accessorKey: 'balance',
      meta: { label: t('Used / Remaining') },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Used / Remaining')} />
      ),
      cell: ({ row }) => <BalanceCell channel={row.original} />,
      size: 180,
    },

    // Response Time column
    {
      accessorKey: 'response_time',
      meta: { label: t('Response'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Response')} />
      ),
      cell: ({ row }) => {
        const responseTime = row.getValue('response_time') as number
        const config = getResponseTimeConfig(responseTime)

        return (
          <StatusBadge
            label={formatResponseTime(responseTime, t)}
            variant={config.variant}
            size='sm'
            copyable={false}
          />
        )
      },
      size: 110,
    },

    // Test Time column
    {
      accessorKey: 'test_time',
      meta: { label: t('Last Tested'), mobileHidden: true },
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title={t('Last Tested')} />
      ),
      cell: ({ row }) => {
        const testTime = row.getValue('test_time') as number

        // For invalid timestamps, show "Never" badge
        if (!testTime || testTime === 0) {
          return <span className='text-muted-foreground text-xs'>-</span>
        }

        const timeText = formatRelativeTime(testTime)
        const fullDate = formatTimestampToDate(testTime)

        // For valid timestamps, show tooltip with full date
        return (
          <TooltipProvider>
            <Tooltip>
              <TooltipTrigger
                render={
                  <span className='text-muted-foreground cursor-pointer font-mono text-sm' />
                }
              >
                {timeText}
              </TooltipTrigger>
              <TooltipContent side='top'>
                <p className='font-mono text-sm'>{fullDate}</p>
              </TooltipContent>
            </Tooltip>
          </TooltipProvider>
        )
      },
      size: 120,
      enableSorting: false,
    },

    // Actions column
    {
      id: 'actions',
      cell: ({ row }) => {
        // Check if this is a tag row (has children)
        const isTagRow = isTagAggregateRow(row.original)

        if (isTagRow) {
          return (
            <DataTableTagRowActions
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
              row={row as any}
            />
          )
        }

        return <DataTableRowActions row={row} />
      },
      size: 132,
      enableSorting: false,
      enableHiding: false,
    },
  ]
}
