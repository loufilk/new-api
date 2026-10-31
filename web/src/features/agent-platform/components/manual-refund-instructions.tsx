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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import type { OperationPayload } from '../types'
import { WorkflowValue, type WorkflowColumn } from './workflow-table'

interface RefundTarget {
  id: number
  business_key: string
  state: string
  version: number
  currency: string
  currency_exponent: number
  amount_minor: string
}
export function ManualRefundInstructionsButton(props: {
  kind: 'purchase' | 'deposit'
  target: RefundTarget
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button size='sm' variant='outline' onClick={() => setOpen(true)}>
        {t('View restricted manual refund instructions')}
      </Button>
      {open && (
        <ManualRefundInstructions {...props} onClose={() => setOpen(false)} />
      )}
    </>
  )
}
function ManualRefundInstructions(props: {
  kind: 'purchase' | 'deposit'
  target: RefundTarget
  onClose: () => void
}) {
  const { t } = useTranslation()
  const actor = useAuthStore((state) => state.auth.user)
  const query = useQuery({
    queryKey: [
      'agent-platform',
      actor?.id,
      actor?.role,
      'restricted-refund-instructions',
      props.kind,
      props.target,
    ],
    gcTime: 0,
    staleTime: 0,
    retry: false,
    queryFn: async ({ signal }) => {
      const result = await getPlatform<OperationPayload>(
        `/finance/${props.kind}-refunds/${props.target.id}/instructions`,
        undefined,
        signal
      )
      const expected = props.target
      if (
        result.manual_only !== true ||
        result.business_key !== expected.business_key ||
        result.state !== expected.state ||
        result.version !== expected.version ||
        result.currency !== expected.currency ||
        result.currency_exponent !== expected.currency_exponent ||
        result.amount_minor !== expected.amount_minor ||
        !result.recipient_digest ||
        (result.recipient_kind === 'original_payer_dossier'
          ? !result.payer_dossier_reference
          : result.recipient_kind !== 'original_provider_payment' ||
            !result.original_payment_reference)
      ) {
        throw new Error(
          t(
            'Manual refund instructions changed. Reload the record before continuing.'
          )
        )
      }
      return result
    },
  })
  const columns: WorkflowColumn[] = [
    { key: 'business_key', label: 'Payment business number' },
    { key: 'state', label: 'Status', kind: 'state' },
    { key: 'amount_minor', label: 'Net refund', kind: 'money' },
    { key: 'provider', label: 'Original payment provider' },
    { key: 'merchant_scope', label: 'Bank ledger scope' },
    { key: 'environment', label: 'Payment environment', kind: 'state' },
    { key: 'recipient_kind', label: 'Original recipient type', kind: 'state' },
    { key: 'recipient_digest', label: 'Original recipient digest' },
    {
      key: 'payer_dossier_reference',
      label: 'Verified original payer dossier ID',
    },
    {
      key: 'original_payment_reference',
      label: 'Original provider payment reference',
    },
  ]
  return (
    <Dialog
      open
      title={t('Restricted manual refund instructions')}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
    >
      <div className='space-y-4'>
        <Alert>
          <AlertDescription>
            {t(
              'This view sends no money. Independently verify the controlled original-payer dossier or original provider payment before offline processing. Never replace the beneficiary or send an UNKNOWN payment again.'
            )}
          </AlertDescription>
        </Alert>
        {query.data?.reconciliation_only === true && !query.isError && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t(
                'Historical in-flight refund: investigate and reconcile the original payment only. Do not send another payment.'
              )}
            </AlertDescription>
          </Alert>
        )}
        {query.isPending && <LoadingState />}
        {query.isError && (
          <ErrorState
            onRetry={() => {
              void query.refetch()
            }}
          />
        )}
        {query.data && !query.isError && (
          <dl className='grid grid-cols-1 gap-3'>
            {columns.map((column) => (
              <div key={column.key}>
                <dt className='text-muted-foreground text-sm'>
                  {t(column.label)}
                </dt>
                <dd>
                  <WorkflowValue row={query.data} column={column} />
                </dd>
              </div>
            ))}
          </dl>
        )}
      </div>
    </Dialog>
  )
}
