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
import { useMutation } from '@tanstack/react-query'
import { Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'

import { deleteInvalidRedemptions } from '../api'
import { useRedemptions } from './redemptions-provider'

export function RedemptionsPrimaryButtons(props: {
  agentId?: number
  center?: boolean
  onSimpleCreate?: () => void
}) {
  const { t } = useTranslation()
  const { setOpen, triggerRefresh } = useRedemptions()
  const [showDeleteInvalidConfirm, setShowDeleteInvalidConfirm] =
    useState(false)
  const deletion = useMutation({
    mutationFn: deleteInvalidRedemptions,
    retry: false,
    onSuccess: (count) => {
      toast.success(
        t('Successfully deleted {{count}} invalid redemption codes', { count }),
        {
          description: t(
            'Each cleanup removes up to 1,000 used, disabled, or expired codes. Repeat cleanup if invalid codes remain.'
          ),
        }
      )
      setShowDeleteInvalidConfirm(false)
      triggerRefresh()
    },
    onError: (error) => handleServerError(error),
  })

  return (
    <>
      <div className='flex flex-wrap gap-2'>
        {!props.center && (
          <Button
            size='sm'
            variant='outline'
            onClick={() => setShowDeleteInvalidConfirm(true)}
          >
            <Trash2 className='text-destructive h-4 w-4' />
            {t('Delete Invalid')}
          </Button>
        )}
        <Button
          size='sm'
          onClick={() => {
            if (props.center && props.onSimpleCreate) {
              props.onSimpleCreate()
            } else {
              setOpen('create')
            }
          }}
        >
          <Plus className='h-4 w-4' />
          {props.center ? t('Issue Redemption Code') : t('Create Code')}
        </Button>
      </div>
      <ConfirmDialog
        destructive
        open={showDeleteInvalidConfirm}
        onOpenChange={(nextOpen) => {
          if (!deletion.isPending) setShowDeleteInvalidConfirm(nextOpen)
        }}
        handleConfirm={() => {
          if (!deletion.isPending) deletion.mutate()
        }}
        isLoading={deletion.isPending}
        className='max-w-md'
        title={t('Delete Invalid Redemption Codes?')}
        desc={
          <>
            {t(
              'Each cleanup removes up to 1,000 used, disabled, or expired codes. Repeat cleanup if invalid codes remain.'
            )}
            <br />
            {t('This action cannot be undone.')}
          </>
        }
        confirmText={t('Delete Invalid')}
      />
    </>
  )
}
