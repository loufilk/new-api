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
import { useQueryClient } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useAuthStore } from '@/stores/auth-store'

import { postPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import { minorMoneyInput, parseMinorMoney } from '../lib/money'
import type { OperationDraft, PurchaseOrder } from '../types'
import { ActionForm, type ActionField, type ActionValues } from './action-form'
import { OperationDialog } from './operation-dialog'
import { PurchaseCreate } from './purchase-create'
import { ResourceTable } from './resource-table'
import { BusinessDate, BusinessState, Money, Quota } from './values'

export function Purchases(props: {
  finance?: boolean
  salesEnabled?: boolean
  purchaseEnabled?: boolean
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const client = useQueryClient()
  const requestId = useRequestId()
  const [create, setCreate] = useState(false)
  const [evidence, setEvidence] = useState<PurchaseOrder | null>(null)
  const [verify, setVerify] = useState<PurchaseOrder | null>(null)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const path = props.finance ? '/finance/purchases' : '/purchases'
  const columns: ColumnDef<PurchaseOrder>[] = [
    { accessorKey: 'id', header: t('Order ID'), meta: { mobileTitle: true } },
    ...(props.finance
      ? [{ accessorKey: 'agent_user_id', header: t('Agent ID') }]
      : []),
    {
      accessorKey: 'status',
      header: t('Status'),
      meta: { mobileBadge: true },
      cell: ({ row }) => <BusinessState value={row.original.status} />,
    },
    {
      accessorKey: 'quota',
      header: t('Procurement quota'),
      cell: ({ row }) => <Quota value={row.original.quota} />,
    },
    {
      accessorKey: 'paid_minor',
      header: t('Quoted payment'),
      cell: ({ row }) => (
        <Money
          minor={row.original.paid_minor}
          currency={row.original.currency}
          exponent={row.original.currency_exponent}
        />
      ),
    },
    { accessorKey: 'policy_version', header: t('Policy version') },
    {
      accessorKey: 'evidence_reference',
      header: t('Payment evidence reference'),
      cell: ({ row }) => (
        <span className='break-all'>
          {row.original.evidence_reference || '—'}
        </span>
      ),
    },
    { accessorKey: 'verified_by', header: t('Receipt verifier') },
    { accessorKey: 'reviewed_by', header: t('Independent reviewer') },
    {
      accessorKey: 'created_at',
      header: t('Created'),
      cell: ({ row }) => <BusinessDate value={row.original.created_at} />,
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => {
        const order = row.original
        if (!props.finance) {
          return order.status === 'awaiting_payment' && props.salesEnabled ? (
            <Button
              size='sm'
              variant='outline'
              onClick={() => setEvidence(order)}
            >
              {t('Submit payment evidence')}
            </Button>
          ) : null
        }
        if (order.agent_user_id === userId) {
          return <span>{t('Another operator must verify your payment')}</span>
        }
        if (order.status === 'evidence_submitted') {
          return (
            <Button
              size='sm'
              variant='outline'
              onClick={() => setVerify(order)}
            >
              {t('Verify actual receipt')}
            </Button>
          )
        }
        if (order.status === 'verified') {
          return (
            <Button
              size='sm'
              variant='outline'
              disabled={order.verified_by === userId}
              onClick={() => {
                const payload = { order_id: order.id, version: order.version }
                setDraft({
                  action: 'purchase.payment.confirm',
                  request_id: requestId(payload),
                  payload,
                  endpoint: `${path}/${order.id}/review`,
                  resource: path,
                })
              }}
            >
              {t('Independent review')}
            </Button>
          )
        }
        return null
      },
    },
  ]
  const verificationFields: ActionField[] = [
    {
      name: 'provider',
      label: 'Payment provider',
      kind: 'select',
      options: [
        'stripe',
        'creem',
        'epay',
        'waffo',
        'pancake',
        'bank_transfer',
      ].map((value) => ({ value, label: value })),
    },
    { name: 'merchant_scope', label: 'Merchant account reference' },
    {
      name: 'environment',
      label: 'Payment environment',
      kind: 'select',
      options: [{ value: 'live', label: 'Live' }],
    },
    {
      name: 'external_reference',
      label: 'Canonical payment reference',
      description:
        'Use the actual payment identifier, not a checkout session or a manual alias.',
    },
    { name: 'paid_amount', label: 'Actual received amount' },
    { name: 'paid_at', label: 'Actual payment time', kind: 'datetime' },
    {
      name: 'evidence_reference',
      label: 'Verified receipt evidence reference',
      kind: 'textarea',
    },
  ]
  const prepareVerification = (values: ActionValues) => {
    if (!verify) return
    const amount = parseMinorMoney(
      String(values.paid_amount),
      verify.currency_exponent
    )
    if (!amount) {
      throw new Error(
        t('Enter an exact positive amount in the original currency.')
      )
    }
    const payload = {
      order_id: verify.id,
      version: verify.version,
      provider: String(values.provider),
      merchant_scope: String(values.merchant_scope),
      environment: String(values.environment),
      external_reference: String(values.external_reference),
      paid_minor: amount,
      currency: verify.currency,
      paid_at: Math.floor(Date.parse(String(values.paid_at)) / 1000),
      evidence_reference: String(values.evidence_reference),
    }
    setDraft({
      action: 'purchase.payment.record',
      request_id: requestId(payload),
      payload,
      endpoint: `${path}/${verify.id}/verify`,
      resource: path,
      review: verificationFields.map((field) => ({
        label: field.label,
        after: String(values[field.name]),
      })),
    })
    setVerify(null)
  }
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Payment evidence is a declaration, not credited funds. A buyer cannot verify their own payment; the receipt verifier and final reviewer must be different people.'
          )}
        </AlertDescription>
      </Alert>
      {!props.finance && (
        <Button
          className='self-start'
          disabled={!props.salesEnabled || !props.purchaseEnabled}
          onClick={() => setCreate(true)}
        >
          {t('New procurement')}
        </Button>
      )}
      {!props.finance && !props.purchaseEnabled && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'New procurement is disabled by the current policy. Existing history and customer rights remain available.'
          )}
        </p>
      )}
      <ResourceTable resource={path} columns={columns} />
      {create && <PurchaseCreate onClose={() => setCreate(false)} />}
      {evidence && (
        <ActionForm
          title={t('Submit payment evidence')}
          description={t(
            'Do not enter passwords, full payment credentials or API keys. Reference an authorized receipt document.'
          )}
          fields={[
            {
              name: 'evidence_reference',
              label: 'Payment evidence reference',
              kind: 'textarea',
            },
          ]}
          onClose={() => setEvidence(null)}
          onSubmit={async (values) => {
            const payload = {
              evidence_reference: String(values.evidence_reference),
            }
            await postPlatform(`/purchases/${evidence.id}/evidence`, {
              ...payload,
              request_id: requestId({ id: evidence.id, ...payload }),
            })
            toast.success(t('Payment evidence submitted'))
            setEvidence(null)
            void client.invalidateQueries({ queryKey: ['agent-platform'] })
          }}
        />
      )}
      {verify && (
        <ActionForm
          title={t('Verify actual receipt')}
          description={`${t('Original currency')}: ${verify.currency}`}
          fields={verificationFields}
          initial={{
            paid_amount: minorMoneyInput(
              verify.paid_minor,
              verify.currency_exponent
            ),
            evidence_reference: verify.evidence_reference,
          }}
          onClose={() => setVerify(null)}
          onSubmit={prepareVerification}
        />
      )}
      {draft && (
        <OperationDialog
          key={draft.request_id}
          draft={draft}
          onClose={() => setDraft(null)}
          onComplete={() => {
            requestId.reset()
            setDraft(null)
          }}
        />
      )}
    </div>
  )
}
