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
import type { Row } from '@tanstack/react-table'
import { Trash2, Edit, Power, PowerOff } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DataTableRowActionMenu } from '@/components/data-table/core/row-action-menu'
import { Button } from '@/components/ui/button'
import {
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
} from '@/components/ui/dropdown-menu'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { OperationDialog } from '@/features/agent-platform/components/operation-dialog'
import type { OperationDraft } from '@/features/agent-platform/types'
import { useAuthStore } from '@/stores/auth-store'

import { REDEMPTION_STATUS, SUCCESS_MESSAGES } from '../constants'
import { isRedemptionExpired } from '../lib'
import { isProtectedRedemption, redemptionOperation } from '../lib/transaction'
import { redemptionSchema } from '../types'
import { RedemptionCancellationDialog } from './redemption-cancellation-dialog'
import { useRedemptions } from './redemptions-provider'

interface DataTableRowActionsProps<TData> {
  row: Row<TData>
}

export function DataTableRowActions<TData>({
  row,
}: DataTableRowActionsProps<TData>) {
  const { t } = useTranslation()
  const redemption = redemptionSchema.parse(row.original)
  const { setOpen, setCurrentRow, triggerRefresh } = useRedemptions()
  const isEnabled = redemption.status === REDEMPTION_STATUS.ENABLED
  const isUsed = redemption.status === REDEMPTION_STATUS.USED
  const isExpired = isRedemptionExpired(
    redemption.expired_time,
    redemption.status
  )

  const user = useAuthStore((state) => state.auth.user)
  const role = user?.role
  const protectedCode = isProtectedRedemption(redemption)
  const [cancelling, setCancelling] = useState(false)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const handleToggleStatus = () => {
    if (
      draft ||
      protectedCode ||
      isUsed ||
      isExpired ||
      !Number.isSafeInteger(redemption.version)
    ) {
      return
    }
    const status = isEnabled
      ? REDEMPTION_STATUS.DISABLED
      : REDEMPTION_STATUS.ENABLED
    setDraft(
      redemptionOperation(
        'update',
        role,
        {
          id: redemption.id,
          version: Number(redemption.version),
          status,
          name: '',
          quota: 0,
          expired_time: 0,
          status_only: true,
        },
        '/api/redemption/?status_only=true',
        [
          { label: 'Name', after: redemption.name },
          {
            label: 'Status',
            before: t(isEnabled ? 'Enabled' : 'Disabled'),
            after: t(isEnabled ? 'Disabled' : 'Enabled'),
          },
        ]
      )
    )
  }

  const canEdit = isEnabled && !isExpired && !protectedCode
  const canToggle =
    !protectedCode &&
    !isUsed &&
    !isExpired &&
    Number.isSafeInteger(redemption.version)

  const canCancel =
    redemption.budget_agent_id === user?.id &&
    (redemption.budget_agent_id ?? 0) > 0 &&
    redemption.delivery_id === 0 &&
    redemption.customer_user_id === 0 &&
    redemption.budget_reserved === true &&
    isEnabled &&
    Number.isSafeInteger(redemption.version)

  return (
    <>
      <div className='-ml-1.5 flex items-center gap-1'>
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='ghost'
                size='icon-sm'
                onClick={() => {
                  setCurrentRow(redemption)
                  setOpen('update')
                }}
                disabled={!canEdit || draft !== null}
                aria-label={t('Edit')}
              />
            }
          >
            <Edit />
          </TooltipTrigger>
          <TooltipContent>{t('Edit')}</TooltipContent>
        </Tooltip>

        <DataTableRowActionMenu ariaLabel={t('Open menu')} modal={false}>
          {canToggle && (
            <DropdownMenuItem
              onClick={handleToggleStatus}
              disabled={draft !== null}
            >
              {isEnabled ? (
                <>
                  {t('Disable')}
                  <DropdownMenuShortcut>
                    <PowerOff size={16} />
                  </DropdownMenuShortcut>
                </>
              ) : (
                <>
                  {t('Enable')}
                  <DropdownMenuShortcut>
                    <Power size={16} />
                  </DropdownMenuShortcut>
                </>
              )}
            </DropdownMenuItem>
          )}
          {canToggle && <DropdownMenuSeparator />}
          {!protectedCode && (
            <DropdownMenuItem
              disabled={draft !== null}
              onClick={() => {
                setCurrentRow(redemption)
                setOpen('delete')
              }}
              className='text-destructive focus:text-destructive'
            >
              {t('Delete')}
              <DropdownMenuShortcut>
                <Trash2 size={16} />
              </DropdownMenuShortcut>
            </DropdownMenuItem>
          )}
          {canCancel && (
            <DropdownMenuItem
              disabled={draft !== null}
              onClick={() => setCancelling(true)}
            >
              {t('Review cancellation')}
            </DropdownMenuItem>
          )}
          {protectedCode && !canCancel && (
            <DropdownMenuItem disabled>
              {t('Customer rights protected')}
            </DropdownMenuItem>
          )}
        </DataTableRowActionMenu>
      </div>
      {cancelling && (
        <RedemptionCancellationDialog
          code={redemption}
          onClose={() => setCancelling(false)}
          onComplete={() => {
            setCancelling(false)
            triggerRefresh()
          }}
        />
      )}
      {draft && (
        <OperationDialog
          key={draft.request_id}
          draft={draft}
          onClose={() => setDraft(null)}
          onComplete={() => {
            toast.success(
              t(
                draft.payload.status === REDEMPTION_STATUS.DISABLED
                  ? SUCCESS_MESSAGES.REDEMPTION_DISABLED
                  : SUCCESS_MESSAGES.REDEMPTION_ENABLED
              )
            )
            setDraft(null)
            triggerRefresh()
          }}
        />
      )}
    </>
  )
}
