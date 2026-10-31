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
import i18next from 'i18next'

import type { OperationPayload } from '../types'
import type { ActionField } from './action-form'
import type { WorkflowAction } from './workflow-operation'
import type { WorkflowColumn } from './workflow-table'

export const statementReason: ActionField = {
  name: 'reason',
  label: 'Reason',
  kind: 'textarea',
  validate: (value) =>
    String(value).trim().length >= 3
      ? undefined
      : i18next.t('Enter a reason with at least three characters.'),
}
export const statementLineColumns: WorkflowColumn[] = [
  { key: 'id', label: 'Statement line ID' },
  { key: 'review.state', label: 'Status', kind: 'state' },
  { key: 'business_reference', label: 'Business reference' },
  { key: 'direction', label: 'Direction', kind: 'state' },
  {
    key: 'amount_minor',
    label: 'Amount',
    kind: 'money',
    catalogExponent: true,
  },
  { key: 'occurred_at', label: 'Transaction date', kind: 'date' },
  { key: 'evidence_reference', label: 'Controlled evidence dossier ID' },
]
export function statementAction(
  action: string,
  label: string,
  fields: ActionField[],
  payload: OperationPayload
): WorkflowAction {
  return {
    action: `statement.${action}`,
    label,
    fields,
    payload,
    endpoint: `/statements/${action.replaceAll('.', '-')}`,
  }
}
