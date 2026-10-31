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
import { Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { OperationDialog } from '@/features/agent-platform/components/operation-dialog'
import type { OperationDraft } from '@/features/agent-platform/types'
import { useAuthStore } from '@/stores/auth-store'

import { redemptionOperation } from '../lib/transaction'
import { useRedemptions } from './redemptions-provider'

export function RedemptionsPrimaryButtons(props: { agentId?: number }) {
  const { t } = useTranslation()
  const { setOpen, triggerRefresh } = useRedemptions()
  const [showDeleteInvalidConfirm, setShowDeleteInvalidConfirm] =
    useState(false)
  const role = useAuthStore((state) => state.auth.user?.role)
  const [draft, setDraft] = useState<OperationDraft | null>(null)

  const handleDeleteInvalid = () => {
    if (draft) return
    setDraft(
      redemptionOperation(
        'delete',
        role,
        { ids: null, invalid_only: true, agent_user_id: props.agentId ?? 0 },
        '/api/redemption/invalid',
        [
          {
            label: 'Agent',
            after: props.agentId ? String(props.agentId) : t('All'),
          },
        ]
      )
    )
  }

  return (
    <>
      <div className='flex flex-wrap gap-2'>
        <Button
          size='sm'
          variant='outline'
          onClick={() => setShowDeleteInvalidConfirm(true)}
        >
          <Trash2 className='text-destructive h-4 w-4' />
          {t('Delete Invalid')}
        </Button>
        <Button size='sm' onClick={() => setOpen('create')}>
          <Plus className='h-4 w-4' />
          {t('Create Code')}
        </Button>
      </div>

      <ConfirmDialog
        destructive
        open={showDeleteInvalidConfirm && !draft}
        onOpenChange={(nextOpen) => {
          if (!draft) setShowDeleteInvalidConfirm(nextOpen)
        }}
        handleConfirm={handleDeleteInvalid}
        className='max-w-md'
        title={t('Delete Invalid Redemption Codes?')}
        desc={
          <>
            {props.agentId && (
              <p>
                {t(
                  'Only invalid codes belonging to agent #{{id}} will be deleted.',
                  { id: props.agentId }
                )}
              </p>
            )}
            {t(
              'Each cleanup removes up to 1,000 used, disabled, or expired codes. Repeat cleanup if invalid codes remain.'
            )}
            <br />
            {t('This action cannot be undone.')}
          </>
        }
        confirmText={t('Delete Invalid')}
      />
      {draft && (
        <OperationDialog
          key={draft.request_id}
          draft={draft}
          onClose={() => setDraft(null)}
          onComplete={(count) => {
            if (typeof count === 'number') {
              toast.success(
                t('Successfully deleted {{count}} invalid redemption codes', {
                  count,
                }),
                {
                  description: t(
                    'Each cleanup removes up to 1,000 used, disabled, or expired codes. Repeat cleanup if invalid codes remain.'
                  ),
                }
              )
            }
            setDraft(null)
            setShowDeleteInvalidConfirm(false)
            triggerRefresh()
          }}
        />
      )}
    </>
  )
}
