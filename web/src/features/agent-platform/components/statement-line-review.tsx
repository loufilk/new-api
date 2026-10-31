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
import type { OperationPayload, PlatformPage } from '../types'
import {
  statementAction,
  statementLineColumns,
  statementReason,
} from './statement-definitions'
import { WorkflowOperation, type WorkflowAction } from './workflow-operation'
import {
  WorkflowTable,
  WorkflowValue,
  type WorkflowRow,
} from './workflow-table'

export function StatementLineReview(props: {
  actionsEnabled?: boolean
  lineId: number
  onClose: () => void
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [operation, setOperation] = useState<WorkflowAction | null>(null)
  const line = useQuery({
    queryKey: ['agent-platform', user?.id, '/statements/lines', props.lineId],
    queryFn: ({ signal }) =>
      getPlatform<WorkflowRow>(
        `/statements/lines/${props.lineId}`,
        undefined,
        signal
      ),
  })
  const current = line.data
  const review =
    current?.review &&
    typeof current.review === 'object' &&
    !Array.isArray(current.review)
      ? current.review
      : null
  const candidates = useQuery({
    queryKey: [
      'agent-platform',
      user?.id,
      '/statements/candidates',
      props.lineId,
    ],
    queryFn: ({ signal }) =>
      getPlatform<OperationPayload[]>(
        `/statements/lines/${props.lineId}/candidates`,
        undefined,
        signal
      ),
  })
  const matches = useQuery({
    queryKey: ['agent-platform', user?.id, '/statements/matches', props.lineId],
    queryFn: ({ signal }) =>
      getPlatform<PlatformPage<WorkflowRow>>(
        '/statements/matches',
        { line_id: props.lineId, p: 1, page_size: 100 },
        signal
      ),
  })
  const imported = useQuery({
    queryKey: [
      'agent-platform',
      user?.id,
      '/statements/import',
      current?.import_id,
    ],
    queryFn: ({ signal }) =>
      getPlatform<WorkflowRow>(
        `/statements/imports/${current?.import_id}`,
        undefined,
        signal
      ),
    enabled: !!current?.import_id,
  })
  const evidence = useQuery({
    queryKey: [
      'agent-platform',
      user?.id,
      '/statements/evidence',
      props.lineId,
    ],
    queryFn: ({ signal }) =>
      getPlatform<OperationPayload>(
        `/statements/lines/${props.lineId}/evidence`,
        undefined,
        signal
      ),
    enabled: review?.state === 'confirmed',
  })
  const activeMatch = matches.data?.items.find(
    (row) => row.id === review?.match_id
  )
  const canCheck =
    !!activeMatch &&
    !!imported.data &&
    activeMatch.proposed_by !== user?.id &&
    activeMatch.owner_user_id !== user?.id &&
    imported.data.imported_by !== user?.id
  const payload = {
    line_id: props.lineId,
    expected_version: review?.version ?? 0,
  }
  if (operation) {
    return (
      <WorkflowOperation
        operation={operation}
        resource='/statements/lines'
        onClose={() => setOperation(null)}
      />
    )
  }
  return (
    <Dialog
      open
      title={t('Review statement line')}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
    >
      {line.isPending && <LoadingState />}
      {line.isError && (
        <ErrorState
          onRetry={() => {
            void line.refetch()
          }}
        />
      )}
      {current && review && (
        <div className='flex flex-col gap-5'>
          <Alert>
            <AlertDescription>
              {t(
                'Candidates are exact business-reference matches, not proof of payment. An independent reviewer must verify the original receipt, amount, currency and beneficiary. UNKNOWN obligations cannot be marked paid here.'
              )}
            </AlertDescription>
          </Alert>
          <dl className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
            {statementLineColumns.map((column) => (
              <div key={column.key}>
                <dt className='text-muted-foreground text-sm'>
                  {t(column.label)}
                </dt>
                <dd>
                  <WorkflowValue row={current} column={column} />
                </dd>
              </div>
            ))}
          </dl>
          {candidates.isError && (
            <ErrorState
              onRetry={() => {
                void candidates.refetch()
              }}
            />
          )}
          {candidates.data && (
            <WorkflowTable
              resource='/statements/candidates'
              rows={candidates.data.map((row, index) => ({
                ...row,
                id: index,
              }))}
              columns={[
                {
                  key: 'target_type',
                  label: 'Match target type',
                  kind: 'state',
                },
                { key: 'target_id', label: 'Match target ID' },
                { key: 'business_reference', label: 'Business reference' },
                {
                  key: 'amount_minor',
                  label: 'Amount',
                  kind: 'money',
                  catalogExponent: true,
                },
                {
                  key: 'exception_code',
                  label: 'Exception code',
                  kind: 'state',
                },
              ]}
            />
          )}
          {props.actionsEnabled && review.state === 'unmatched' && (
            <Button
              variant='outline'
              onClick={() =>
                setOperation(
                  statementAction(
                    'match.propose',
                    'Propose statement match',
                    [
                      {
                        name: 'target_type',
                        label: 'Match target type',
                        kind: 'select',
                        options: [
                          {
                            value: 'incoming_receipt',
                            label: 'Incoming receipt',
                          },
                          {
                            value: 'outgoing_receipt',
                            label: 'Outgoing receipt',
                          },
                          { value: 'purchase', label: 'Procurement order' },
                          {
                            value: 'purchase_refund',
                            label: 'Purchase refund',
                          },
                          {
                            value: 'withdrawal',
                            label: 'Commission withdrawal',
                          },
                          {
                            value: 'credit_repayment',
                            label: 'Credit repayment',
                          },
                          { value: 'deposit', label: 'Deposit receipt' },
                          { value: 'deposit_refund', label: 'Deposit return' },
                        ],
                      },
                      {
                        name: 'target_id',
                        label: 'Match target ID',
                        kind: 'integer',
                        min: 1,
                      },
                      statementReason,
                    ],
                    payload
                  )
                )
              }
            >
              {t('Propose statement match')}
            </Button>
          )}
          {review.state === 'proposed' && (
            <div className='flex flex-wrap gap-3'>
              <Button
                disabled={!props.actionsEnabled || !canCheck}
                onClick={() =>
                  setOperation(
                    statementAction(
                      'match.confirm',
                      'Confirm statement match',
                      [statementReason],
                      { ...payload, match_id: review.match_id }
                    )
                  )
                }
              >
                {t('Confirm statement match')}
              </Button>
              <Button
                variant='outline'
                disabled={!props.actionsEnabled || !canCheck}
                onClick={() =>
                  setOperation(
                    statementAction(
                      'match.reject',
                      'Reject statement match',
                      [statementReason],
                      { ...payload, match_id: review.match_id }
                    )
                  )
                }
              >
                {t('Reject statement match')}
              </Button>
              {!canCheck && (
                <p className='text-muted-foreground text-sm'>
                  {t('Another independent operator must review this match.')}
                </p>
              )}
            </div>
          )}
          <h3 className='font-medium'>{t('Match history')}</h3>
          <WorkflowTable
            resource='/statements/matches'
            filters={{ line_id: props.lineId }}
            columns={[
              { key: 'id', label: 'Match ID' },
              { key: 'target_type', label: 'Match target type', kind: 'state' },
              { key: 'target_id', label: 'Match target ID' },
              { key: 'proposed_by', label: 'Proposed by' },
              { key: 'target_digest', label: 'Frozen target digest' },
            ]}
          />
          <h3 className='font-medium'>{t('Reconciliation exceptions')}</h3>
          <WorkflowTable
            resource='/statements/exceptions'
            filters={{ line_id: props.lineId }}
            columns={[
              { key: 'id', label: 'Exception ID' },
              { key: 'state', label: 'Status', kind: 'state' },
              { key: 'code', label: 'Exception code', kind: 'state' },
              { key: 'resolution', label: 'Resolution', kind: 'state' },
            ]}
            actions={(row) =>
              props.actionsEnabled && row.state === 'open'
                ? [
                    {
                      ...statementAction(
                        'exception.resolve',
                        'Review exception resolution',
                        [
                          {
                            name: 'resolution',
                            label: 'Resolution',
                            kind: 'select',
                            options: [
                              {
                                value: 'retry',
                                label: 'Retry matching after correction',
                              },
                              {
                                value: 'exclude_duplicate',
                                label:
                                  'Exclude confirmed duplicate transaction',
                              },
                              {
                                value: 'exclude_invalid_import',
                                label:
                                  'Exclude an erroneous import with verified replacement',
                              },
                              {
                                value: 'exclude_unrelated',
                                label: 'Exclude verified unrelated transaction',
                              },
                            ],
                          },
                          {
                            name: 'replacement_line_id',
                            label: 'Confirmed replacement line ID',
                            kind: 'integer',
                            min: 1,
                            required: false,
                          },
                          {
                            name: 'evidence_reference',
                            label: 'Controlled evidence dossier ID',
                            required: false,
                            max: 128,
                          },
                          statementReason,
                        ],
                        { ...payload, exception_id: row.id }
                      ),
                      transform: (_values, body) => {
                        if (body.resolution === 'exclude_invalid_import') {
                          if (
                            !body.replacement_line_id ||
                            !body.evidence_reference
                          ) {
                            throw new Error(
                              t(
                                'A confirmed replacement line and original-document evidence are required.'
                              )
                            )
                          }
                        } else {
                          delete body.replacement_line_id
                          delete body.evidence_reference
                        }
                        return body
                      },
                    },
                  ]
                : []
            }
          />
          {evidence.data && (
            <section className='space-y-2'>
              <h3 className='font-medium'>
                {t('Confirmed reconciliation evidence')}
              </h3>
              <dl className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
                {[
                  { key: 'business_reference', label: 'Business reference' },
                  {
                    key: 'evidence_reference',
                    label: 'Controlled evidence dossier ID',
                  },
                  { key: 'source_digest', label: 'Original source digest' },
                ].map((column) => (
                  <div key={column.key}>
                    <dt>{t(column.label)}</dt>
                    <dd>
                      <WorkflowValue row={evidence.data} column={column} />
                    </dd>
                  </div>
                ))}
              </dl>
            </section>
          )}
        </div>
      )}
    </Dialog>
  )
}
