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
import type { Table } from '@tanstack/react-table'
import { Trash2 } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { DataTableBulkActions as BulkActionsToolbar } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { OperationDialog } from '@/features/agent-platform/components/operation-dialog'
import type { OperationDraft } from '@/features/agent-platform/types'
import { useAuthStore } from '@/stores/auth-store'

import { isProtectedRedemption, redemptionOperation } from '../lib/transaction'
import type { Redemption } from '../types'
import { useRedemptions } from './redemptions-provider'

type DataTableBulkActionsProps = {
  table: Table<Redemption>
}

export function DataTableBulkActions(props: DataTableBulkActionsProps) {
  const { t } = useTranslation()
  const { triggerRefresh } = useRedemptions()
  const role = useAuthStore((state) => state.auth.user?.role)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const [deleteTargets, setDeleteTargets] = useState<Redemption[] | null>(null)
  const selectedRows = props.table.getFilteredSelectedRowModel().rows

  const contentToCopy = useMemo(() => {
    const selectedCodes = selectedRows.map((row) => {
      const redemption = row.original
      return `${redemption.name}\t${redemption.key}`
    })
    return selectedCodes.join('\n')
  }, [selectedRows])

  return (
    <>
      <BulkActionsToolbar table={props.table} entityName={t('redemption code')}>
        <CopyButton
          value={contentToCopy}
          variant='outline'
          size='icon'
          className='size-8'
          tooltip={t('Copy selected codes')}
          successTooltip={t('Codes copied!')}
          aria-label={t('Copy selected codes')}
        />
        <Tooltip>
          <TooltipTrigger
            render={
              <Button
                variant='destructive'
                size='icon'
                className='size-8'
                aria-label={t('Delete selected redemption codes')}
                disabled={
                  draft !== null ||
                  selectedRows.some((row) =>
                    isProtectedRedemption(row.original)
                  )
                }
                onClick={() =>
                  setDeleteTargets(selectedRows.map((row) => row.original))
                }
              />
            }
          >
            <Trash2 aria-hidden='true' />
          </TooltipTrigger>
          <TooltipContent>
            {selectedRows.some((row) => isProtectedRedemption(row.original))
              ? t('Customer rights protected')
              : t('Delete selected redemption codes')}
          </TooltipContent>
        </Tooltip>
      </BulkActionsToolbar>
      <ConfirmDialog
        destructive
        open={deleteTargets !== null && !draft}
        onOpenChange={(open) => {
          if (!open && !draft) setDeleteTargets(null)
        }}
        title={t('Delete {{count}} redemption codes?', {
          count: deleteTargets?.length ?? 0,
        })}
        desc={t('This action cannot be undone.')}
        confirmText={t('Delete')}
        disabled={
          !deleteTargets?.length || deleteTargets.some(isProtectedRedemption)
        }
        handleConfirm={() => {
          if (
            !deleteTargets?.length ||
            draft ||
            deleteTargets.some(isProtectedRedemption)
          ) {
            return
          }
          setDraft(
            redemptionOperation(
              'delete',
              role,
              {
                ids: deleteTargets.map((code) => code.id),
                invalid_only: false,
                agent_user_id: 0,
              },
              '/api/redemption/batch',
              [
                {
                  label: 'Selected redemption IDs',
                  after: deleteTargets.map((code) => code.id).join(', '),
                },
              ]
            )
          )
        }}
      />
      {draft && (
        <OperationDialog
          key={draft.request_id}
          draft={draft}
          onClose={() => setDraft(null)}
          onComplete={(count) => {
            if (typeof count === 'number') {
              toast.success(
                t('Successfully deleted {{count}} redemption codes', { count })
              )
            }
            props.table.setRowSelection((previous) => {
              const next = { ...previous }
              for (const code of deleteTargets ?? []) {
                delete next[String(code.id)]
              }
              return next
            })
            setDraft(null)
            setDeleteTargets(null)
            triggerRefresh()
          }}
        />
      )}
    </>
  )
}
