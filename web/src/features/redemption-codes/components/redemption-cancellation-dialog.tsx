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

import { ActionForm } from '@/features/agent-platform/components/action-form'
import { OperationDialog } from '@/features/agent-platform/components/operation-dialog'
import type { OperationDraft } from '@/features/agent-platform/types'

import type { Redemption } from '../types'

export function RedemptionCancellationDialog(props: {
  code: Redemption
  onClose: () => void
  onComplete: () => void
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  if (draft) {
    return (
      <OperationDialog
        key={draft.request_id}
        draft={draft}
        onClose={() => setDraft(null)}
        onComplete={props.onComplete}
      />
    )
  }
  return (
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
          max: 255,
        },
      ]}
      onClose={props.onClose}
      onSubmit={(values) => {
        if (values.customer_obligations_resolved !== true) {
          throw new Error(
            'Confirm that customer obligations are resolved before cancellation.'
          )
        }
        if (!Number.isSafeInteger(props.code.version)) return
        const payload = {
          redemption_id: props.code.id,
          version: Number(props.code.version),
          customer_obligations_resolved: true,
          evidence_reference: String(values.evidence_reference).trim(),
        }
        setDraft({
          action: 'delivery.cancel',
          request_id: crypto.randomUUID(),
          payload,
          endpoint: '/deliveries/cancel-code',
          resource: '/redemption-codes',
          review: [
            { label: 'Name', after: props.code.name },
            {
              label: 'Customer obligation evidence reference',
              after: payload.evidence_reference,
            },
          ],
        })
      }}
    />
  )
}
