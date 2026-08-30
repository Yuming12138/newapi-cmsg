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
import { useQueryClient } from '@tanstack/react-query'
import type { Row } from '@tanstack/react-table'
import {
  MoreHorizontal,
  Boxes,
  Pencil,
  TestTube,
  Gauge,
  DollarSign,
  Download,
  Copy,
  Power,
  PowerOff,
  Key,
  Trash2,
  RefreshCw,
  Loader2,
  ShieldCheck,
  ShieldOff,
  CalendarClock,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestampToDate } from '@/lib/format'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DateTimePicker } from '@/components/datetime-picker'
import {
  cancelChannelQuotaProtectionForceUnlock,
  forceUnlockChannelQuotaProtection,
} from '../api'
import { MODEL_FETCHABLE_TYPES } from '../constants'
import {
  channelsQueryKeys,
  handleDeleteChannel,
  handleTestChannel,
  handleToggleChannelStatus,
  isChannelEnabled,
  isMultiKeyChannel,
} from '../lib'
import { parseUpstreamUpdateMeta } from '../lib/upstream-update-utils'
import type { Channel } from '../types'
import { useChannels } from './channels-provider'

interface DataTableRowActionsProps {
  row: Row<Channel>
}

type ChannelQuotaProtectionState = {
  active: boolean
  until: number | null
  resetAt: number | null
}

// Keep these bounds aligned with the backend guard. The backend remains the
// source of truth; the client-side checks only provide immediate feedback.
const FORCE_UNLOCK_MIN_WINDOW_SECONDS = 60
const FORCE_UNLOCK_MAX_WINDOW_SECONDS = 8 * 24 * 60 * 60

function recordValue(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null
}

function timestampValue(value: unknown): number | null {
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : null
}

function defaultQuotaProtectionUntil(
  protection: ChannelQuotaProtectionState
): Date {
  const now = Math.floor(Date.now() / 1000)
  const minUntil = now + FORCE_UNLOCK_MIN_WINDOW_SECONDS
  const maxUntil = now + FORCE_UNLOCK_MAX_WINDOW_SECONDS
  const preferredUntil =
    protection.until != null && protection.until > now
      ? protection.until
      : (protection.resetAt ?? now + 60 * 60)
  const clampedUntil = Math.min(maxUntil, Math.max(minUntil, preferredUntil))
  return new Date(clampedUntil * 1000)
}

function channelQuotaProtectionState(
  channel: Channel
): ChannelQuotaProtectionState | null {
  if (channel.id !== 12 || !channel.other_info) return null
  try {
    const otherInfo = recordValue(JSON.parse(channel.other_info))
    const guard = recordValue(otherInfo?.cliproxy_cpa_quota_guard)
    if (guard?.managed !== true) return null
    const health = recordValue(guard.health)
    const dynamicBudget = recordValue(health?.dynamic_daily_budget)
    if (dynamicBudget?.applied !== true) return null
    const manualOverride =
      recordValue(health?.manual_force_unlock) ??
      recordValue(guard.manual_force_unlock)
    const until = timestampValue(manualOverride?.until)
    const windows = recordValue(health?.windows)
    const weeklyWindow = recordValue(windows?.['7d'])
    const now = Math.floor(Date.now() / 1000)
    const resetCandidates = [
      timestampValue(dynamicBudget.next_daily_budget_reset_at),
      timestampValue(dynamicBudget.weekly_reset_at),
      timestampValue(dynamicBudget.effective_reset_at),
      timestampValue(weeklyWindow?.reset_at),
    ].filter((value): value is number => value != null && value > now)
    const active =
      manualOverride?.active === true && until != null && until > now
    return {
      active,
      until,
      resetAt: resetCandidates.length > 0 ? Math.min(...resetCandidates) : null,
    }
  } catch {
    return null
  }
}

export function DataTableRowActions({ row }: DataTableRowActionsProps) {
  const { t } = useTranslation()
  const channel = row.original
  const { setOpen, setCurrentRow, upstream } = useChannels()
  const queryClient = useQueryClient()
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false)
  const [quotaProtectionConfirmOpen, setQuotaProtectionConfirmOpen] =
    useState(false)
  const [quotaProtectionUntil, setQuotaProtectionUntil] = useState<
    Date | undefined
  >()
  const [quotaProtectionTimeError, setQuotaProtectionTimeError] = useState<
    string | null
  >(null)
  const [isTesting, setIsTesting] = useState(false)
  const [isTogglingStatus, setIsTogglingStatus] = useState(false)
  const [isUpdatingQuotaProtection, setIsUpdatingQuotaProtection] =
    useState(false)

  const isEnabled = isChannelEnabled(channel)
  const isMultiKey = isMultiKeyChannel(channel)
  const quotaProtection = channelQuotaProtectionState(channel)

  const openQuotaProtectionDialog = () => {
    if (!quotaProtection) return
    setQuotaProtectionTimeError(null)
    setQuotaProtectionUntil(
      quotaProtection.active
        ? quotaProtection.until != null
          ? new Date(quotaProtection.until * 1000)
          : undefined
        : defaultQuotaProtectionUntil(quotaProtection)
    )
    setQuotaProtectionConfirmOpen(true)
  }

  const handleEdit = () => {
    setCurrentRow(channel)
    setOpen('update-channel')
  }

  const handleTest = () => {
    setCurrentRow(channel)
    setOpen('test-channel')
  }

  const handleDirectTest = async (e: React.MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation()
    setIsTesting(true)
    try {
      await handleTestChannel(channel.id, { channelName: channel.name }, () => {
        queryClient.invalidateQueries({ queryKey: channelsQueryKeys.lists() })
      })
    } finally {
      setIsTesting(false)
    }
  }

  const handleQueryBalance = () => {
    setCurrentRow(channel)
    setOpen('balance-query')
  }

  const handleFetchModels = () => {
    setCurrentRow(channel)
    setOpen('fetch-models')
  }

  const handleManageOllamaModels = () => {
    setCurrentRow(channel)
    setOpen('ollama-models')
  }

  const handleCopy = () => {
    setCurrentRow(channel)
    setOpen('copy-channel')
  }

  const handleManageKeys = () => {
    setCurrentRow(channel)
    setOpen('multi-key-manage')
  }

  const handleToggleStatus = async (
    e?: React.MouseEvent<HTMLButtonElement>
  ) => {
    e?.stopPropagation()
    setIsTogglingStatus(true)
    try {
      await handleToggleChannelStatus(channel.id, channel.status, queryClient)
    } finally {
      setIsTogglingStatus(false)
    }
  }

  const handleQuotaProtectionConfirm = async () => {
    if (!quotaProtection) return

    let requestedUntil: number | undefined
    if (!quotaProtection.active) {
      const now = Math.floor(Date.now() / 1000)
      const selectedMilliseconds = quotaProtectionUntil?.getTime() ?? NaN
      if (!Number.isFinite(selectedMilliseconds)) {
        setQuotaProtectionTimeError(t('Select a date and time'))
        return
      }
      requestedUntil = Math.floor(selectedMilliseconds / 1000)
      const secondsFromNow = requestedUntil - now
      if (secondsFromNow <= 0) {
        setQuotaProtectionTimeError(t('The unlock time must be in the future'))
        return
      }
      if (secondsFromNow < FORCE_UNLOCK_MIN_WINDOW_SECONDS) {
        setQuotaProtectionTimeError(
          t('The unlock time must be at least 1 minute from now')
        )
        return
      }
      if (secondsFromNow > FORCE_UNLOCK_MAX_WINDOW_SECONDS) {
        setQuotaProtectionTimeError(
          t('The unlock time cannot be more than 8 days from now')
        )
        return
      }
    }

    setIsUpdatingQuotaProtection(true)
    try {
      const response = quotaProtection.active
        ? await cancelChannelQuotaProtectionForceUnlock(channel.id)
        : await forceUnlockChannelQuotaProtection(channel.id, requestedUntil)
      if (!response.success) {
        toast.error(response.message || t('Failed to update quota protection'))
        return
      }
      toast.success(
        quotaProtection.active
          ? t('Channel 12 quota protection restored')
          : t('Channel 12 quota protection force-unlocked')
      )
      setQuotaProtectionTimeError(null)
      setQuotaProtectionConfirmOpen(false)
      await queryClient.invalidateQueries({
        queryKey: channelsQueryKeys.lists(),
      })
    } catch {
      toast.error(t('Failed to update quota protection'))
    } finally {
      setIsUpdatingQuotaProtection(false)
    }
  }

  let statusIcon = <Power className='size-4' />
  if (isTogglingStatus) {
    statusIcon = <Loader2 className='size-4 animate-spin' />
  } else if (isEnabled) {
    statusIcon = <PowerOff className='size-4' />
  }

  let quotaProtectionConfirmText = t('Force unlock until selected time')
  if (isUpdatingQuotaProtection) {
    quotaProtectionConfirmText = t('Updating')
  } else if (quotaProtection?.active) {
    quotaProtectionConfirmText = t('Restore protection')
  }

  return (
    <div className='flex items-center justify-end gap-1'>
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={(e) => {
                e.stopPropagation()
                handleEdit()
              }}
              aria-label={t('Edit')}
            />
          }
        >
          <Pencil className='size-4' />
        </TooltipTrigger>
        <TooltipContent>{t('Edit')}</TooltipContent>
      </Tooltip>

      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={handleDirectTest}
              disabled={isTesting}
              aria-label={t('Test Connection')}
            />
          }
        >
          {isTesting ? (
            <Loader2 className='size-4 animate-spin' />
          ) : (
            <Gauge className='size-4' />
          )}
        </TooltipTrigger>
        <TooltipContent>{t('Test Connection')}</TooltipContent>
      </Tooltip>

      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              variant='ghost'
              size='icon-sm'
              onClick={handleToggleStatus}
              disabled={isTogglingStatus}
              aria-label={isEnabled ? t('Disable') : t('Enable')}
              className={
                isEnabled
                  ? 'text-destructive hover:text-destructive'
                  : 'text-emerald-600 hover:text-emerald-600 dark:text-emerald-400 dark:hover:text-emerald-400'
              }
            />
          }
        >
          {statusIcon}
        </TooltipTrigger>
        <TooltipContent>
          {isEnabled ? t('Disable') : t('Enable')}
        </TooltipContent>
      </Tooltip>

      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant='ghost'
              className='data-popup-open:bg-muted flex h-8 w-8 p-0'
            />
          }
        >
          <MoreHorizontal className='h-4 w-4' />
          <span className='sr-only'>{t('Open menu')}</span>
        </DropdownMenuTrigger>
        <DropdownMenuContent align='end' className='w-48'>
          {/* Test Connection */}
          <DropdownMenuItem onClick={handleTest}>
            {t('Test Connection')}
            <DropdownMenuShortcut>
              <TestTube size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          {/* Query Balance */}
          <DropdownMenuItem onClick={handleQueryBalance}>
            {t('Query Balance')}
            <DropdownMenuShortcut>
              <DollarSign size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          {quotaProtection && (
            <DropdownMenuItem
              onSelect={(event) => {
                event.preventDefault()
                openQuotaProtectionDialog()
              }}
              className={
                quotaProtection.active
                  ? undefined
                  : 'text-amber-700 focus:text-amber-700 dark:text-amber-400 dark:focus:text-amber-400'
              }
            >
              {quotaProtection.active
                ? t('Restore channel 12 quota protection')
                : t('Force unlock channel 12 quota protection')}
              <DropdownMenuShortcut>
                {quotaProtection.active ? (
                  <ShieldCheck size={16} />
                ) : (
                  <ShieldOff size={16} />
                )}
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          {/* Fetch Models */}
          <DropdownMenuItem onClick={handleFetchModels}>
            {t('Fetch Models')}
            <DropdownMenuShortcut>
              <Download size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          {/* Detect Upstream Updates (only for fetchable channel types) */}
          {MODEL_FETCHABLE_TYPES.has(channel.type) && (
            <DropdownMenuItem
              onClick={() => {
                const meta = parseUpstreamUpdateMeta(channel.settings)
                if (
                  meta.pendingAddModels.length > 0 ||
                  meta.pendingRemoveModels.length > 0
                ) {
                  upstream.openModal(
                    channel,
                    meta.pendingAddModels,
                    meta.pendingRemoveModels,
                    meta.pendingAddModels.length > 0 ? 'add' : 'remove'
                  )
                } else {
                  upstream.detectChannelUpdates(channel)
                }
              }}
            >
              {t('Upstream Updates')}
              <DropdownMenuShortcut>
                <RefreshCw size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          {/* Ollama Models (only for Ollama channels) */}
          {channel.type === 4 && (
            <DropdownMenuItem onClick={handleManageOllamaModels}>
              {t('Manage Ollama Models')}
              <DropdownMenuShortcut>
                <Boxes size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          <DropdownMenuSeparator />

          {/* Copy Channel */}
          <DropdownMenuItem onClick={handleCopy}>
            {t('Copy Channel')}
            <DropdownMenuShortcut>
              <Copy size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>

          {/* Manage Keys (only for multi-key channels) */}
          {isMultiKey && (
            <DropdownMenuItem onClick={handleManageKeys}>
              {t('Manage Keys')}
              <DropdownMenuShortcut>
                <Key size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}

          <DropdownMenuSeparator />

          {/* Delete */}
          <DropdownMenuItem
            onSelect={(e) => {
              e.preventDefault()
              setDeleteConfirmOpen(true)
            }}
            className='text-destructive focus:text-destructive'
          >
            {t('Delete')}
            <DropdownMenuShortcut>
              <Trash2 size={16} />
            </DropdownMenuShortcut>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <ConfirmDialog
        open={quotaProtectionConfirmOpen}
        onOpenChange={setQuotaProtectionConfirmOpen}
        title={
          quotaProtection?.active
            ? t('Restore channel 12 quota protection?')
            : t('Force unlock channel 12 quota protection?')
        }
        desc={
          quotaProtection?.active ? (
            t(
              'The automatic daily budget and protected reserve checks will resume on the next guard run (within about one minute).'
            )
          ) : (
            <div className='space-y-2 text-sm'>
              <p>
                {t(
                  'This temporarily bypasses only the New API daily budget and protected reserve for channel 12. It does not consume a reset credit or bypass actual CPA quota exhaustion or account unavailability.'
                )}
              </p>
              <p className='text-muted-foreground'>
                {t(
                  'Choose the date and time when the temporary bypass should end. The current CPA quota cycle can still end it earlier.'
                )}
              </p>
            </div>
          )
        }
        confirmText={quotaProtectionConfirmText}
        destructive={!quotaProtection?.active}
        isLoading={isUpdatingQuotaProtection}
        handleConfirm={handleQuotaProtectionConfirm}
      >
        {!quotaProtection?.active && (
          <div className='space-y-2'>
            <div className='text-muted-foreground flex items-center gap-1.5 text-xs font-medium'>
              <CalendarClock className='size-3.5' />
              <span>{t('Unlock until')}</span>
            </div>
            <DateTimePicker
              value={quotaProtectionUntil}
              onChange={(date) => {
                setQuotaProtectionUntil(date)
                setQuotaProtectionTimeError(null)
              }}
              className='w-full'
              placeholder={t('Select unlock date')}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'You can choose from 1 minute up to 8 days from now. The time uses your browser local time.'
              )}
            </p>
            {quotaProtectionTimeError && (
              <p className='text-destructive text-xs' role='alert'>
                {quotaProtectionTimeError}
              </p>
            )}
            {quotaProtectionUntil &&
              Number.isFinite(quotaProtectionUntil.getTime()) && (
                <p className='text-muted-foreground text-xs'>
                  {t('Selected end time: {{time}}', {
                    time: formatTimestampToDate(
                      Math.floor(quotaProtectionUntil.getTime() / 1000)
                    ),
                  })}
                </p>
              )}
          </div>
        )}
      </ConfirmDialog>

      <ConfirmDialog
        open={deleteConfirmOpen}
        onOpenChange={setDeleteConfirmOpen}
        title={t('Delete Channel')}
        desc={t(
          'Are you sure you want to delete channel "{{name}}"? This action cannot be undone.',
          { name: channel.name }
        )}
        confirmText={t('Delete')}
        destructive
        handleConfirm={() => {
          handleDeleteChannel(channel.id, queryClient)
          setDeleteConfirmOpen(false)
        }}
      />
    </div>
  )
}
