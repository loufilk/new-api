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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'

import { useRequestId } from '../hooks/use-request-id'
import type { Delivery, OperationDraft } from '../types'
import { ActionForm } from './action-form'
import { DeliveryClaimLink } from './delivery-claim-link'
import { OperationDialog } from './operation-dialog'
import { ResourceTable } from './resource-table'
import { BusinessDate, BusinessState, Quota } from './values'

export function Deliveries(props: {
  salesEnabled: boolean
  deliveryEnabled: boolean
}) {
  const { t } = useTranslation()
  const requestId = useRequestId()
  const [creating, setCreating] = useState(false)
  const [cancelling, setCancelling] = useState<Delivery | null>(null)
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const [issued, setIssued] = useState<Delivery | null>(null)
  const columns: ColumnDef<Delivery>[] = [
    {
      accessorKey: 'id',
      header: t('Delivery ID'),
      meta: { mobileTitle: true },
    },
    { accessorKey: 'name', header: t('Name') },
    {
      accessorKey: 'state',
      header: t('Status'),
      meta: { mobileBadge: true },
      cell: ({ row }) => <BusinessState value={row.original.state} />,
    },
    {
      accessorKey: 'quota',
      header: t('Delivered quota'),
      cell: ({ row }) => <Quota value={row.original.quota} />,
    },
    {
      accessorKey: 'customer_user_id',
      header: t('Claiming customer ID'),
      cell: ({ row }) => row.original.customer_user_id || '—',
    },
    {
      accessorKey: 'claimed_at',
      header: t('Claimed at'),
      cell: ({ row }) => <BusinessDate value={row.original.claimed_at} />,
    },
    {
      accessorKey: 'expires_at',
      header: t('Claim deadline'),
      cell: ({ row }) =>
        row.original.expires_at ? (
          <BusinessDate value={row.original.expires_at} />
        ) : (
          t('No expiry')
        ),
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) =>
        row.original.state === 'created' ? (
          <Button
            size='sm'
            variant='outline'
            onClick={() => setCancelling(row.original)}
          >
            {t('Review cancellation')}
          </Button>
        ) : (
          <span className='text-muted-foreground text-xs'>
            {t('Customer rights protected')}
          </span>
        ),
    },
  ]
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Claiming binds the delivery to the customer; it does not prove offline payment. Unclaimed does not mean unsold. Bound deliveries cannot be reduced, shortened, disabled or deleted, even after an agent exits.'
          )}
        </AlertDescription>
      </Alert>
      <Button
        className='self-start'
        disabled={!props.salesEnabled || !props.deliveryEnabled}
        onClick={() => setCreating(true)}
      >
        {t('Create customer delivery')}
      </Button>
      <ResourceTable resource='/deliveries' columns={columns} />
      {creating && (
        <ActionForm
          title={t('Create customer delivery')}
          fields={[
            { name: 'name', label: 'Delivery name', max: 80 },
            { name: 'quota', label: 'Quota units', kind: 'integer', min: 1 },
            {
              name: 'expires_at',
              label: 'Claim deadline',
              kind: 'datetime',
              required: false,
            },
          ]}
          onClose={() => setCreating(false)}
          onSubmit={(values) => {
            const payload = {
              name: String(values.name),
              quota: Number(values.quota),
              expires_at: values.expires_at
                ? Math.floor(Date.parse(String(values.expires_at)) / 1000)
                : 0,
            }
            setDraft({
              action: 'delivery.create',
              request_id: requestId(payload),
              payload,
              endpoint: '/deliveries',
              resource: '/deliveries',
              review: [
                { label: 'Delivery name', after: payload.name },
                {
                  label: 'Claim deadline',
                  after: String(values.expires_at || t('No expiry')),
                },
              ],
            })
            setCreating(false)
          }}
        />
      )}
      {cancelling && (
        <ActionForm
          title={t('Review cancellation')}
          description={t(
            'An unclaimed delivery may already have been sold offline. Verify and document that all customer obligations are resolved before requesting cancellation.'
          )}
          fields={[
            {
              name: 'customer_obligations_resolved',
              label: 'All customer obligations have been verified as resolved',
              kind: 'checkbox',
            },
            {
              name: 'evidence_reference',
              label: 'Customer obligation evidence reference',
              kind: 'textarea',
            },
          ]}
          onClose={() => setCancelling(null)}
          onSubmit={(values) => {
            if (values.customer_obligations_resolved !== true) {
              throw new Error(
                t(
                  'Confirm that customer obligations are resolved before cancellation.'
                )
              )
            }
            const payload = {
              delivery_id: cancelling.id,
              version: cancelling.version,
              customer_obligations_resolved: true,
              evidence_reference: String(values.evidence_reference),
            }
            setDraft({
              action: 'delivery.cancel',
              request_id: requestId(payload),
              payload,
              endpoint: `/deliveries/${cancelling.id}/cancel`,
              resource: '/deliveries',
              review: [
                {
                  label: 'Customer obligation evidence reference',
                  after: payload.evidence_reference,
                },
              ],
            })
            setCancelling(null)
          }}
        />
      )}
      {draft && (
        <OperationDialog
          key={draft.request_id}
          draft={draft}
          onClose={() => setDraft(null)}
          onComplete={(result) => {
            requestId.reset()
            if (
              draft.action === 'delivery.create' &&
              result &&
              typeof result === 'object' &&
              'claim_code' in result &&
              typeof result.claim_code === 'string'
            ) {
              setIssued(result as Delivery)
            }
            setDraft(null)
          }}
        />
      )}
      {issued?.claim_code && (
        <DeliveryClaimLink
          delivery={{ ...issued, claim_code: issued.claim_code }}
          onClose={() => setIssued(null)}
        />
      )}
    </div>
  )
}
