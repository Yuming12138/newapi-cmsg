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
import { Check, Clock3, HandCoins, X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { formatCurrencyUSD, formatTimestamp } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { getQuotaRequests, reviewQuotaRequest } from '../api'
import type { QuotaRequestRecord } from '../types'
import { useUsers } from './users-provider'

function requestUserLabel(request: QuotaRequestRecord) {
  return request.display_name || request.username || `#${request.user_id}`
}

export function QuotaRequestsPanel() {
  const { t } = useTranslation()
  const { refreshTrigger, triggerRefresh } = useUsers()
  const [reviewingKey, setReviewingKey] = useState<string | null>(null)

  const {
    data: requests = [],
    error,
    isLoading,
  } = useQuery({
    queryKey: ['quota-requests', refreshTrigger],
    queryFn: async () => {
      const result = await getQuotaRequests()
      if (!result.success) {
        throw new Error(result.message || 'Failed to load quota requests')
      }
      return result.data || []
    },
  })

  const handleReview = async (
    request: QuotaRequestRecord,
    approve: boolean
  ) => {
    const key = `${request.id}:${approve ? 'approve' : 'reject'}`
    setReviewingKey(key)
    try {
      const result = await reviewQuotaRequest({
        request_id: request.id,
        approve,
      })
      if (result.success) {
        toast.success(
          approve ? t('Quota request approved') : t('Quota request rejected')
        )
        triggerRefresh()
      } else {
        toast.error(
          result.message
            ? t(result.message)
            : t('Failed to review quota request')
        )
      }
    } catch (error: unknown) {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to review quota request')
      )
    } finally {
      setReviewingKey(null)
    }
  }

  const errorMessage = error instanceof Error ? error.message : ''

  if (!isLoading && !errorMessage && requests.length === 0) return null

  return (
    <div className='bg-card overflow-hidden rounded-lg border'>
      <div className='flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3'>
        <div className='flex min-w-0 items-center gap-2'>
          <HandCoins className='text-muted-foreground size-4 shrink-0' />
          <div className='min-w-0'>
            <div className='text-sm font-medium'>
              {t('Pending quota requests')}
            </div>
            <div className='text-muted-foreground text-xs'>
              {t('Review second low-balance requests')}
            </div>
          </div>
        </div>
        <Badge variant='outline'>{requests.length}</Badge>
      </div>

      <div className='divide-y'>
        {isLoading && (
          <div className='text-muted-foreground px-4 py-3 text-sm'>
            {t('Loading')}
          </div>
        )}
        {!isLoading && errorMessage && (
          <div className='text-destructive px-4 py-3 text-sm'>
            {t(errorMessage)}
          </div>
        )}
        {!isLoading &&
          !errorMessage &&
          requests.map((request) => (
            <div
              key={request.id}
              className='grid gap-3 px-4 py-3 md:grid-cols-[minmax(0,1fr)_auto]'
            >
              <div className='min-w-0 space-y-1'>
                <div className='flex flex-wrap items-center gap-2'>
                  <span className='truncate text-sm font-medium'>
                    {requestUserLabel(request)}
                  </span>
                  <Badge variant='outline'>
                    <Clock3 className='size-3' />
                    {t('Pending')}
                  </Badge>
                </div>
                <div className='text-muted-foreground flex flex-wrap gap-x-3 gap-y-1 text-xs'>
                  <span>ID: {request.user_id}</span>
                  <span>{formatCurrencyUSD(request.amount_usd)}</span>
                  <span>{formatTimestamp(request.created_at)}</span>
                  <span>
                    {t('Request')} #{request.request_count ?? 0}
                  </span>
                </div>
              </div>

              <div className='flex items-center gap-2'>
                <Button
                  size='sm'
                  onClick={() => handleReview(request, true)}
                  disabled={reviewingKey !== null}
                >
                  <Check className='size-4' />
                  {reviewingKey === `${request.id}:approve`
                    ? t('Approving')
                    : t('Approve')}
                </Button>
                <Button
                  size='sm'
                  variant='outline'
                  onClick={() => handleReview(request, false)}
                  disabled={reviewingKey !== null}
                >
                  <X className='size-4' />
                  {reviewingKey === `${request.id}:reject`
                    ? t('Rejecting')
                    : t('Reject')}
                </Button>
              </div>
            </div>
          ))}
      </div>
    </div>
  )
}
