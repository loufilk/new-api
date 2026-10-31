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

import { useRequestId } from '../hooks/use-request-id'
import { operationFormPayload } from '../lib/operation-form'
import type { OperationDraft, OperationPayload } from '../types'
import { ActionForm, type ActionField, type ActionValues } from './action-form'
import { OperationDialog } from './operation-dialog'

export interface WorkflowAction {
  label: string
  action: string
  endpoint: string
  fields: ActionField[]
  initial?: ActionValues
  description?: string
  includeRequestId?: boolean
  payload?: OperationPayload
  review?: OperationDraft['review']
  transform?: (
    values: ActionValues,
    payload: OperationPayload
  ) => OperationPayload
}

export function WorkflowOperation(props: {
  operation: WorkflowAction
  resource: string
  onClose: () => void
  onComplete?: () => void
}) {
  const { t } = useTranslation()
  const requestId = useRequestId()
  const [values, setValues] = useState<ActionValues | undefined>(
    props.operation.initial
  )
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  if (draft) {
    return (
      <OperationDialog
        key={draft.request_id}
        draft={draft}
        onClose={() => setDraft(null)}
        onComplete={() => {
          requestId.reset()
          props.onComplete?.()
          props.onClose()
        }}
      />
    )
  }
  return (
    <ActionForm
      title={t(props.operation.label)}
      description={
        props.operation.description ? t(props.operation.description) : undefined
      }
      fields={props.operation.fields}
      initial={values}
      onClose={props.onClose}
      onSubmit={(nextValues) => {
        let payload = {
          ...props.operation.payload,
          ...operationFormPayload(props.operation.fields, nextValues),
        }
        if (props.operation.transform) {
          payload = props.operation.transform(nextValues, payload)
        }
        setValues(nextValues)
        const id = requestId({ action: props.operation.action, ...payload })
        if (props.operation.includeRequestId) {
          payload = { ...payload, request_id: id }
        }
        setDraft({
          action: props.operation.action,
          request_id: id,
          payload,
          endpoint: props.operation.endpoint,
          resource: props.resource,
          review: [
            ...(props.operation.review ?? []),
            ...props.operation.fields.map((field) => ({
              label: field.label,
              after: String(nextValues[field.name] ?? ''),
            })),
          ],
        })
      }}
    />
  )
}
