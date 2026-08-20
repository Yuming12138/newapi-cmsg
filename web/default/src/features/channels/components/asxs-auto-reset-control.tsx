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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  getASXSAutoDailyResetControl,
  updateASXSAutoDailyResetControl,
} from '../api'

const queryKey = ['asxs-auto-daily-reset-control'] as const

export function ASXSAutoResetControl() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [pendingEnabled, setPendingEnabled] = useState<boolean | null>(null)
  const controlQuery = useQuery({
    queryKey,
    queryFn: getASXSAutoDailyResetControl,
    refetchInterval: 30_000,
  })
  const mutation = useMutation({
    mutationFn: async (enabled: boolean) => {
      const response = await updateASXSAutoDailyResetControl(enabled)
      if (!response.success || !response.data) {
        throw new Error(
          response.message || t('Failed to update ASXS auto reset')
        )
      }
      return response
    },
    onSuccess: (response) => {
      queryClient.setQueryData(queryKey, response)
      toast.success(
        response.data!.enabled
          ? t('ASXS auto reset enabled')
          : t('ASXS auto reset disabled')
      )
      setPendingEnabled(null)
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? error.message
          : t('Failed to update ASXS auto reset')
      )
    },
  })

  const enabled = controlQuery.data?.data?.enabled ?? false
  const unavailable =
    controlQuery.isError || controlQuery.data?.success === false
  const label = unavailable
    ? t('ASXS auto reset unavailable')
    : enabled
      ? t('ASXS auto reset: On')
      : t('ASXS auto reset: Off')

  return (
    <>
      <Button
        variant={enabled ? 'outline' : 'secondary'}
        size='sm'
        disabled={controlQuery.isLoading || unavailable || mutation.isPending}
        onClick={() => setPendingEnabled(!enabled)}
        className={
          enabled ? 'border-emerald-500/60 text-emerald-600' : undefined
        }
      >
        <RefreshCw
          className={mutation.isPending ? 'h-4 w-4 animate-spin' : 'h-4 w-4'}
        />
        <span className='max-lg:hidden'>{label}</span>
      </Button>

      <ConfirmDialog
        open={pendingEnabled !== null}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setPendingEnabled(null)
        }}
        title={
          pendingEnabled
            ? t('Enable ASXS automatic subscription reset?')
            : t('Disable ASXS automatic subscription reset?')
        }
        desc={
          pendingEnabled
            ? t(
                'The public Aliyun node will resume checking once per minute and may consume an eligible ASXS reset when usage reaches the configured threshold.'
              )
            : t(
                'The public Aliyun node will stop consuming ASXS subscription resets from the next timer run. Existing channel routing is not changed.'
              )
        }
        confirmText={pendingEnabled ? t('Enable') : t('Disable')}
        destructive={pendingEnabled === false}
        isLoading={mutation.isPending}
        handleConfirm={() => {
          if (pendingEnabled !== null) mutation.mutate(pendingEnabled)
        }}
      />
    </>
  )
}
