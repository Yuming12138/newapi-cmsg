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
import { CheckCircle2, HandCoins, Hourglass, ShieldAlert } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatTimestamp, formatQuota } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { requestQuotaIncrease } from '../api'
import type { UserProfile, QuotaRequestStatus } from '../types'

interface QuotaRequestCardProps {
  profile: UserProfile | null
  loading: boolean
  onProfileUpdate: () => void
}

function getRequestBadgeVariant(status?: QuotaRequestStatus) {
  switch (status) {
    case 'approved':
      return 'secondary'
    case 'rejected':
      return 'destructive'
    default:
      return 'outline'
  }
}

function getRequestStatusLabel(status: QuotaRequestStatus) {
  switch (status) {
    case 'approved':
      return 'Approved'
    case 'rejected':
      return 'Rejected'
    default:
      return 'Pending'
  }
}

export function QuotaRequestCard({
  profile,
  loading,
  onProfileUpdate,
}: QuotaRequestCardProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  const requestStatus = profile?.quota_request

  if (!profile || !requestStatus) return null

  const localizeReason = (reason?: string) => {
    if (!reason) return t('cmsg.profile.quotaRequest.unavailable')

    if (reason.startsWith('current quota is not below')) {
      return t('cmsg.profile.quotaRequest.balanceBelow', {
        threshold: formatQuota(requestStatus.threshold_quota),
      })
    }

    const reasonKeys: Record<string, string> = {
      'quota request is not available': 'cmsg.profile.quotaRequest.unavailable',
      'quota guard is not enabled': 'cmsg.profile.quotaRequest.guardDisabled',
      'daily request limit reached':
        'cmsg.profile.quotaRequest.dailyLimitReached',
      'invalid user': 'cmsg.profile.quotaRequest.invalidUser',
      'quota guard setting is not available':
        'cmsg.profile.quotaRequest.guardSettingUnavailable',
    }

    return t(reasonKeys[reason] ?? 'cmsg.profile.quotaRequest.unavailable')
  }

  const latest = requestStatus?.latest_request
  const isBelowThreshold = profile.quota < requestStatus.threshold_quota
  const canRequest = !!requestStatus?.can_request && isBelowThreshold
  const unavailableReason = !isBelowThreshold
    ? t('cmsg.profile.quotaRequest.balanceBelow', {
        threshold: formatQuota(requestStatus.threshold_quota),
      })
    : localizeReason(requestStatus.reason)
  const requestDisabled = loading || submitting || !canRequest

  const handleSubmit = async () => {
    setSubmitting(true)
    try {
      const result = await requestQuotaIncrease()
      if (result.success) {
        toast.success(t('cmsg.profile.quotaRequest.submitted'))
        setOpen(false)
        await onProfileUpdate()
      } else {
        toast.error(
          result.message
            ? localizeReason(result.message)
            : t('cmsg.profile.quotaRequest.failed')
        )
      }
    } catch {
      toast.error(t('cmsg.profile.quotaRequest.failed'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <>
      <Card>
        <CardHeader>
          <div className='flex items-start justify-between gap-3'>
            <div className='min-w-0 space-y-1'>
              <CardTitle className='flex items-center gap-2'>
                <HandCoins className='size-4 shrink-0' />
                {t('cmsg.profile.quotaRequest.title')}
              </CardTitle>
              <CardDescription>
                {t('cmsg.profile.quotaRequest.description', {
                  amount: formatQuota(requestStatus?.request_amount_quota ?? 0),
                  threshold: formatQuota(requestStatus?.threshold_quota ?? 0),
                })}
              </CardDescription>
            </div>
            {latest && (
              <Badge variant={getRequestBadgeVariant(latest.status)}>
                {t(getRequestStatusLabel(latest.status))}
              </Badge>
            )}
          </div>
        </CardHeader>
        <CardContent className='space-y-3'>
          <div className='grid gap-2 text-sm sm:grid-cols-2'>
            <div className='rounded-lg border px-3 py-2'>
              <div className='text-muted-foreground text-xs'>
                {t('cmsg.profile.quotaRequest.currentBalance')}
              </div>
              <div className='font-mono text-base font-semibold'>
                {formatQuota(profile?.quota ?? 0)}
              </div>
            </div>
            <div className='rounded-lg border px-3 py-2'>
              <div className='text-muted-foreground text-xs'>
                {t('cmsg.profile.quotaRequest.todayRequests')}
              </div>
              <div className='font-mono text-base font-semibold'>
                {requestStatus?.request_count ?? 0}/
                {requestStatus?.max_requests_per_day ?? 0}
              </div>
            </div>
          </div>

          {!canRequest && (
            <div className='bg-muted/50 text-muted-foreground flex items-start gap-2 rounded-lg border px-3 py-2 text-sm'>
              <ShieldAlert className='mt-0.5 size-4 shrink-0' />
              <span>{unavailableReason}</span>
            </div>
          )}

          {latest && (
            <div className='bg-muted/30 rounded-lg border px-3 py-2 text-sm'>
              <div className='text-muted-foreground mb-1 flex items-center gap-2 text-xs'>
                <Hourglass className='size-3.5 shrink-0' />
                {t('cmsg.profile.quotaRequest.latestRequest')}
              </div>
              <div className='flex flex-wrap items-center gap-2'>
                <span>
                  {formatQuota(
                    Math.round(
                      latest.amount_usd *
                        (requestStatus?.quota_per_usd ?? 500000)
                    )
                  )}
                </span>
                <span className='text-muted-foreground'>
                  #{latest.request_count ?? 0}
                </span>
                <span className='text-muted-foreground'>
                  {formatTimestamp(latest.created_at)}
                </span>
              </div>
              {latest.auto_approved && (
                <div className='text-muted-foreground mt-1 flex items-center gap-2 text-xs'>
                  <CheckCircle2 className='size-3.5 shrink-0' />
                  {t('cmsg.profile.quotaRequest.autoApproved')}
                </div>
              )}
            </div>
          )}
        </CardContent>
        <CardFooter className='flex flex-col items-stretch gap-2 sm:flex-row sm:items-center sm:justify-between'>
          <div className='text-muted-foreground text-xs'>
            {t('cmsg.profile.quotaRequest.remainingRequests', {
              count: requestStatus?.remaining_requests ?? 0,
            })}
          </div>
          <div className='flex flex-col items-stretch gap-1 sm:items-end'>
            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger
                  render={
                    <span
                      className='inline-flex w-full sm:w-auto'
                      title={!canRequest ? unavailableReason : undefined}
                      aria-label={!canRequest ? unavailableReason : undefined}
                      aria-disabled={!canRequest}
                      tabIndex={!canRequest ? 0 : undefined}
                    >
                      <Button
                        size='sm'
                        className='w-full sm:w-auto'
                        onClick={() => setOpen(true)}
                        disabled={requestDisabled}
                      >
                        <HandCoins className='size-4' />
                        {t('cmsg.profile.quotaRequest.requestButton', {
                          amount: formatQuota(
                            requestStatus?.request_amount_quota ?? 0
                          ),
                        })}
                      </Button>
                    </span>
                  }
                ></TooltipTrigger>
                {!canRequest && (
                  <TooltipContent>
                    <p>{unavailableReason}</p>
                  </TooltipContent>
                )}
              </Tooltip>
            </TooltipProvider>
            {!canRequest && (
              <div className='text-muted-foreground text-xs sm:hidden'>
                {unavailableReason}
              </div>
            )}
          </div>
        </CardFooter>
      </Card>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {t('cmsg.profile.quotaRequest.dialogTitle')}
            </DialogTitle>
            <DialogDescription>
              {t('cmsg.profile.quotaRequest.policy')}
            </DialogDescription>
          </DialogHeader>
          <div className='bg-muted/40 rounded-lg border px-3 py-2 text-sm'>
            {t('cmsg.profile.quotaRequest.requestAmount')}:{' '}
            {formatQuota(requestStatus?.request_amount_quota ?? 0)}
          </div>
          <DialogFooter>
            <Button variant='outline' onClick={() => setOpen(false)}>
              {t('Cancel')}
            </Button>
            <Button onClick={handleSubmit} disabled={submitting}>
              <HandCoins className='size-4' />
              {submitting
                ? t('Submitting')
                : t('cmsg.profile.quotaRequest.submit')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
