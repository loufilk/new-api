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
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { OperationDialog } from '@/features/agent-platform/components/operation-dialog'
import type { OperationDraft } from '@/features/agent-platform/types'
import { useAuthStore } from '@/stores/auth-store'

import { SUCCESS_MESSAGES } from '../constants'
import { isProtectedRedemption, redemptionOperation } from '../lib/transaction'
import { useRedemptions } from './redemptions-provider'

export function RedemptionsDeleteDialog() {
  const { t } = useTranslation()
  const { open, setOpen, currentRow, triggerRefresh } = useRedemptions()
  const role = useAuthStore((state) => state.auth.user?.role)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  return (
    <>
      <ConfirmDialog
        open={open === 'delete' && !draft}
        onOpenChange={(nextOpen) => {
          if (!nextOpen && !draft) setOpen(null)
        }}
        title={t('Are you sure?')}
        desc={
          <>
            {t('This will permanently delete redemption code')}{' '}
            <span className='font-semibold break-all'>{currentRow?.name}</span>
            {t('. This action cannot be undone.')}
          </>
        }
        destructive
        disabled={!currentRow || isProtectedRedemption(currentRow)}
        confirmText={t('Delete')}
        handleConfirm={() => {
          if (!currentRow || draft || isProtectedRedemption(currentRow)) return
          setDraft(
            redemptionOperation(
              'delete',
              role,
              { ids: [currentRow.id], invalid_only: false, agent_user_id: 0 },
              `/api/redemption/${currentRow.id}/`,
              [{ label: 'Name', after: currentRow.name }]
            )
          )
        }}
      />
      {draft && (
        <OperationDialog
          key={draft.request_id}
          draft={draft}
          onClose={() => setDraft(null)}
          onComplete={() => {
            toast.success(t(SUCCESS_MESSAGES.REDEMPTION_DELETED))
            setDraft(null)
            setOpen(null)
            triggerRefresh()
          }}
        />
      )}
    </>
  )
}
