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
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { useAuthStore } from '@/stores/auth-store'

import type { ActionField } from './action-form'
import { statementAction, statementReason } from './statement-definitions'
import { WorkflowTable } from './workflow-table'

const evidenceFields: ActionField[] = [
  {
    name: 'dossier_reference',
    label: 'Verified original payer dossier ID',
    max: 128,
    description:
      'Use controlled dossier IDs only. Do not paste identity documents, bank account numbers or credentials.',
  },
  { name: 'paid_at', label: 'Actual payment time', kind: 'datetime' },
  {
    name: 'evidence_reference',
    label: 'Controlled evidence dossier ID',
    max: 128,
  },
  statementReason,
]
export function StatementEvidence({ enabled }: { enabled: boolean }) {
  const { t } = useTranslation()
  const actorId = useAuthStore((state) => state.auth.user?.id)
  const propose = statementAction(
    'evidence.propose',
    'Propose missing receipt evidence',
    [
      {
        name: 'receipt_type',
        label: 'Receipt type',
        kind: 'select',
        options: [
          { value: 'incoming_receipt', label: 'Incoming receipt' },
          { value: 'outgoing_receipt', label: 'Outgoing receipt' },
        ],
      },
      { name: 'receipt_id', label: 'Receipt ID', kind: 'integer', min: 1 },
      ...evidenceFields,
    ],
    {}
  )
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Supplementary evidence can fill missing payer or payment-time facts on already verified final receipts. It cannot replace existing facts, resolve UNKNOWN payments or change money. A different reviewer must independently re-enter the same facts.'
          )}
        </AlertDescription>
      </Alert>
      <WorkflowTable
        resource='/statements/evidence'
        create={[propose]}
        disabled={!enabled}
        columns={[
          { key: 'id', label: 'Evidence ID' },
          { key: 'state', label: 'Status', kind: 'state' },
          { key: 'receipt_type', label: 'Receipt type', kind: 'state' },
          { key: 'receipt_id', label: 'Receipt ID' },
          { key: 'version', label: 'Version' },
          { key: 'counterparty_digest', label: 'Verified payer digest' },
          { key: 'paid_at', label: 'Actual payment time', kind: 'date' },
          {
            key: 'evidence_reference',
            label: 'Controlled evidence dossier ID',
          },
          { key: 'proposed_by', label: 'Proposed by' },
          { key: 'decision.reviewed_by', label: 'Independent reviewer' },
        ]}
        actions={(row) => {
          if (
            !enabled ||
            row.state !== 'proposed' ||
            row.proposed_by === actorId ||
            row.owner_user_id === actorId
          ) {
            return []
          }
          const payload = { evidence_id: row.id, expected_version: row.version }
          return [
            statementAction(
              'evidence.confirm',
              'Confirm supplementary receipt evidence',
              evidenceFields,
              payload
            ),
            statementAction(
              'evidence.reject',
              'Reject supplementary receipt evidence',
              [statementReason],
              payload
            ),
          ]
        }}
      />
    </div>
  )
}
