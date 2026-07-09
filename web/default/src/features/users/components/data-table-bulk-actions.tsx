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
import { type Table } from '@tanstack/react-table'
import { Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTableBulkActions as BulkActionsToolbar } from '@/components/data-table'
import { batchDeleteUsers } from '../api'
import { ERROR_MESSAGES } from '../constants'
import { type User } from '../types'
import { useUsers } from './users-provider'

interface DataTableBulkActionsProps {
  table: Table<User>
}

export function DataTableBulkActions({ table }: DataTableBulkActionsProps) {
  const { t } = useTranslation()
  const { triggerRefresh } = useUsers()
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false)
  const [isDeleting, setIsDeleting] = useState(false)
  const selectedRows = table.getFilteredSelectedRowModel().rows
  const selectedIds = selectedRows.reduce<number[]>((ids, row) => {
    const id = row.original.id
    if (typeof id === 'number') {
      ids.push(id)
    }
    return ids
  }, [])

  const handleBatchDelete = async () => {
    setIsDeleting(true)
    try {
      const result = await batchDeleteUsers(selectedIds)
      if (result.success) {
        const count = result.data || selectedIds.length
        toast.success(t('Successfully deleted {{count}} user(s)', { count }))
        table.resetRowSelection()
        triggerRefresh()
        setShowDeleteConfirm(false)
      } else {
        toast.error(result.message || t(ERROR_MESSAGES.BATCH_DELETE_FAILED))
      }
    } catch (_error) {
      toast.error(t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setIsDeleting(false)
    }
  }

  return (
    <>
      <BulkActionsToolbar table={table} entityName='user'>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='destructive'
                size='icon'
                onClick={() => setShowDeleteConfirm(true)}
                className='size-8'
                aria-label={t('Delete selected users')}
              />
            }
          >
            <Trash2 />
            <span className='sr-only'>{t('Delete selected users')}</span>
          </TooltipTrigger>
          <TooltipContent>
            <p>{t('Delete selected users')}</p>
          </TooltipContent>
        </Tooltip>
      </BulkActionsToolbar>

      <ConfirmDialog
        destructive
        open={showDeleteConfirm}
        onOpenChange={setShowDeleteConfirm}
        handleConfirm={handleBatchDelete}
        isLoading={isDeleting}
        className='max-w-md'
        title={t('Delete {{count}} user(s)?', { count: selectedRows.length })}
        desc={
          <>
            {t('You are about to permanently delete {{count}} user(s).', {
              count: selectedRows.length,
            })}{' '}
            <br />
            {t('This action cannot be undone.')}
          </>
        }
        confirmText={t('Delete')}
      />
    </>
  )
}
