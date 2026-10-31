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
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import { parseReceivableAllocations } from '../lib/financial-inputs'
import type { AgentPolicy, OperationPayload } from '../types'
import { AccountWorkspace } from './account-workspace'
import type { ActionField } from './action-form'
import type { WorkflowAction } from './workflow-operation'
import {
  WorkflowTable,
  WorkflowValue,
  type WorkflowColumn,
  type WorkflowRow,
} from './workflow-table'

export interface CreditOverview {
  current_limit?: WorkflowRow | null
  available_minor?: string
  overdue_minor?: string
  draw_allowed?: boolean
  audits?: WorkflowRow[]
  account: OperationPayload | null
  limits: WorkflowRow[]
  limit_requests: WorkflowRow[]
  draws: WorkflowRow[]
  receivables: WorkflowRow[]
  invoices: WorkflowRow[]
  repayments: WorkflowRow[]
  adjustments: WorkflowRow[]
  ledger: WorkflowRow[]
}
const evidence: ActionField = {
  name: 'evidence_reference',
  label: 'Controlled evidence dossier ID',
  description:
    'Use controlled dossier IDs only. Do not paste identity documents, bank account numbers or credentials.',
}
const reason: ActionField = {
  name: 'reason',
  label: 'Reason',
  kind: 'textarea',
}
const amount: ActionField = {
  name: 'amount_minor',
  label: 'Amount (minor units)',
  kind: 'minor',
  min: 1,
}
const identity: WorkflowColumn[] = [
  { key: 'id', label: 'ID' },
  { key: 'state', label: 'Status', kind: 'state' },
]
function creditAction(
  action: string,
  label: string,
  fields: ActionField[],
  payload: OperationPayload
): WorkflowAction {
  return {
    action: `credit.${action}`,
    label,
    fields,
    payload,
    endpoint: `/credit/actions/${action}`,
  }
}
export function CreditPanel(props: {
  finance?: boolean
  salesEnabled?: boolean
  settlementEnabled?: boolean
}) {
  return (
    <AccountWorkspace<CreditOverview>
      resource='/credit'
      finance={props.finance}
    >
      {(data, agentUserId) => (
        <CreditRecords
          key={agentUserId}
          {...props}
          data={data}
          agentUserId={agentUserId}
        />
      )}
    </AccountWorkspace>
  )
}
function CreditRecords(props: {
  data: CreditOverview
  agentUserId: number
  finance?: boolean
  salesEnabled?: boolean
  settlementEnabled?: boolean
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const policy = useQuery({
    queryKey: ['agent-platform', user?.id, 'policy'],
    queryFn: ({ signal }) =>
      getPlatform<AgentPolicy>('/policy/current', undefined, signal),
  })
  const account = props.data.account
  const currentLimitId = Number(
    props.data.current_limit?.id ?? account?.limit_version_id ?? 0
  )
  const newCreditEnabled =
    policy.data?.credit_enabled === true &&
    policy.data.credit_terms_approved === true &&
    (props.finance || props.salesEnabled)
  const target = { agent_user_id: props.agentUserId }
  const summaryColumns: WorkflowColumn[] = [
    { key: 'limit_version_id', label: 'Credit limit version' },
    {
      key: 'draw_allowed',
      label: 'New credit draw permitted',
      kind: 'boolean',
    },
    {
      key: 'available_minor',
      label: 'Available credit principal',
      kind: 'money',
    },
    { key: 'overdue_minor', label: 'Overdue principal', kind: 'money' },
    {
      key: 'reserved_minor',
      label: 'Reserved credit principal',
      kind: 'money',
    },
    {
      key: 'outstanding_minor',
      label: 'Outstanding receivables',
      kind: 'money',
    },
    {
      key: 'written_off_minor',
      label: 'Written-off risk exposure',
      kind: 'money',
    },
  ]
  const requestLimit = creditAction(
    'limit.request',
    'Apply for credit limit',
    [
      { ...amount, min: 0 },
      {
        name: 'term_seconds',
        label: 'Credit term (seconds)',
        kind: 'integer',
        min: 1,
      },
      { name: 'expires_at', label: 'Expires at', kind: 'datetime' },
      { name: 'frozen', label: 'Freeze new credit', kind: 'checkbox' },
      reason,
      evidence,
    ],
    { ...target, expected_limit_id: currentLimitId }
  )
  const draw = creditAction(
    'draw',
    'Draw approved credit',
    [
      {
        name: 'quota',
        label: 'Procurement quota units',
        kind: 'integer',
        min: 1,
      },
    ],
    { ...target, expected_limit_id: currentLimitId }
  )
  const recordRepayment = creditAction(
    'repayment.record',
    'Record offline repayment',
    [
      amount,
      { name: 'merchant_scope', label: 'Bank ledger scope' },
      { name: 'external_reference', label: 'Bank transaction reference' },
      { name: 'paid_at', label: 'Payment date', kind: 'datetime' },
      evidence,
      {
        name: 'allocation_lines',
        label: 'Receivable allocations',
        kind: 'textarea',
        max: 12000,
        description:
          'One receivable per line: receivable ID, amount in minor units. The exact sum must equal the received amount.',
        validate: (value) => {
          try {
            parseReceivableAllocations(String(value))
            return undefined
          } catch {
            return t('Enter valid, unique receivable allocations.')
          }
        },
      },
    ],
    {
      ...target,
      provider: 'bank_transfer',
      environment: 'live',
      currency: String(account?.currency ?? ''),
      currency_exponent: Number(account?.currency_exponent ?? 0),
    }
  )
  recordRepayment.transform = (values, payload) => {
    const allocations = parseReceivableAllocations(
      String(values.allocation_lines),
      String(values.amount_minor)
    )
    delete payload.allocation_lines
    return { ...payload, allocations }
  }
  const overdueReceivables = props.data.receivables.map((row) => ({
    ...row,
    state:
      row.state === 'open' && Number(row.due_at) < Math.floor(Date.now() / 1000)
        ? 'overdue'
        : row.state,
  }))
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Credit is separate from your wallet. Debt is recognized only when credit-funded quota is spent. Existing customer deliveries remain redeemable even when new credit is frozen or overdue. No interest or automatic deposit offset is applied.'
          )}
        </AlertDescription>
      </Alert>
      {account ? (
        <dl className='grid grid-cols-1 gap-4 sm:grid-cols-2'>
          {summaryColumns.map((column) => (
            <div key={column.key}>
              <dt className='text-muted-foreground text-sm'>
                {t(column.label)}
              </dt>
              <dd>
                <WorkflowValue
                  row={{
                    ...account,
                    available_minor: props.data.available_minor ?? null,
                    overdue_minor: props.data.overdue_minor ?? null,
                    draw_allowed: props.data.draw_allowed ?? null,
                  }}
                  column={column}
                />
              </dd>
            </div>
          ))}
        </dl>
      ) : (
        <p>{t('No credit account has been established.')}</p>
      )}
      {props.data.current_limit && (
        <section className='space-y-3'>
          <h3 className='font-medium'>{t('Current approved credit limit')}</h3>
          <dl className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
            {(
              [
                { key: 'amount_minor', label: 'Amount', kind: 'money' },
                {
                  key: 'term_seconds',
                  label: 'Credit term (seconds)',
                  kind: 'integer',
                },
                { key: 'expires_at', label: 'Expires at', kind: 'date' },
                { key: 'frozen', label: 'Freeze new credit', kind: 'boolean' },
                { key: 'policy_version', label: 'Policy version' },
              ] as WorkflowColumn[]
            ).map((column) => (
              <div key={column.key}>
                <dt className='text-muted-foreground text-sm'>
                  {t(column.label)}
                </dt>
                <dd>
                  <WorkflowValue
                    row={props.data.current_limit ?? {}}
                    column={column}
                  />
                </dd>
              </div>
            ))}
          </dl>
        </section>
      )}
      {!newCreditEnabled && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'New credit is disabled. Historical records and permitted recovery operations remain available.'
          )}
        </p>
      )}
      <Tabs defaultValue='limits'>
        <TabsList
          className='h-auto max-w-full flex-wrap'
          aria-label={t('Credit records')}
        >
          {[
            'limits',
            'draws',
            'receivables',
            'repayments',
            'adjustments',
            'ledger',
            'audit',
          ].map((value) => (
            <TabsTrigger key={value} value={value}>
              {t(
                {
                  limits: 'Credit limits',
                  draws: 'Credit draws',
                  receivables: 'Receivables and invoices',
                  repayments: 'Repayments',
                  adjustments: 'Receivable adjustments',
                  ledger: 'Credit ledger',
                  audit: 'Review audit',
                }[value] ?? ''
              )}
            </TabsTrigger>
          ))}
          {props.finance && (
            <TabsTrigger value='reconciliation'>
              {t('Credit consistency findings')}
            </TabsTrigger>
          )}
        </TabsList>
        <TabsContent value='limits' className='space-y-5'>
          <WorkflowTable
            resource='/credit'
            rows={props.data.limit_requests}
            columns={[
              ...identity,
              { key: 'amount_minor', label: 'Requested limit', kind: 'money' },
              {
                key: 'term_seconds',
                label: 'Credit term (seconds)',
                kind: 'integer',
              },
              { key: 'expires_at', label: 'Expires at', kind: 'date' },
              { key: 'frozen', label: 'Frozen', kind: 'boolean' },
              { key: 'reason', label: 'Reason' },
              { key: 'requested_by', label: 'Requested by' },
            ]}
            create={[requestLimit]}
            disabled={!newCreditEnabled}
            actions={(row) => {
              if (
                !props.finance ||
                row.state !== 'requested' ||
                row.requested_by === user?.id ||
                row.agent_user_id === user?.id
              ) {
                return []
              }
              const payload = { resource_id: row.id, version: row.version }
              return [
                creditAction(
                  'limit.approve',
                  'Approve credit limit',
                  [
                    {
                      name: 'risk_approved',
                      label: 'I approve the documented credit risk',
                      kind: 'checkbox',
                      mustBeTrue: true,
                    },
                    evidence,
                  ],
                  payload
                ),
                creditAction(
                  'limit.reject',
                  'Reject credit application',
                  [reason],
                  payload
                ),
              ]
            }}
          />
          <h3 className='font-medium'>
            {t('Immutable credit limit versions')}
          </h3>
          <WorkflowTable
            resource='/credit'
            rows={props.data.limits}
            columns={[
              { key: 'id', label: 'Credit limit version' },
              { key: 'amount_minor', label: 'Approved limit', kind: 'money' },
              {
                key: 'term_seconds',
                label: 'Credit term (seconds)',
                kind: 'integer',
              },
              { key: 'expires_at', label: 'Expires at', kind: 'date' },
              { key: 'frozen', label: 'Frozen', kind: 'boolean' },
              { key: 'policy_version', label: 'Policy version' },
              { key: 'approved_by', label: 'Approver' },
            ]}
          />
        </TabsContent>
        <TabsContent value='draws'>
          <WorkflowTable
            resource='/credit'
            rows={props.data.draws}
            create={props.finance ? [] : [draw]}
            disabled={
              !newCreditEnabled ||
              !currentLimitId ||
              props.data.draw_allowed !== true
            }
            columns={[
              { key: 'id', label: 'Credit draw ID' },
              { key: 'batch_id', label: 'Funding batch' },
              { key: 'original_quota', label: 'Original quota', kind: 'quota' },
              {
                key: 'principal_minor',
                label: 'Reserved credit principal',
                kind: 'money',
              },
              {
                key: 'term_seconds',
                label: 'Credit term (seconds)',
                kind: 'integer',
              },
              {
                key: 'recognition_mode',
                label: 'Receivable recognition rule',
                kind: 'state',
              },
              { key: 'limit_version_id', label: 'Credit limit version' },
              { key: 'policy_version', label: 'Policy version' },
            ]}
            actions={(row) =>
              !props.finance && props.settlementEnabled
                ? [
                    creditAction(
                      'release',
                      'Release unused credit',
                      [
                        {
                          name: 'quota',
                          label: 'Unused quota units to release',
                          kind: 'integer',
                          min: 1,
                          max: Number(row.original_quota),
                        },
                      ],
                      { resource_id: row.id }
                    ),
                  ]
                : []
            }
          />
        </TabsContent>
        <TabsContent value='receivables' className='space-y-5'>
          <WorkflowTable
            resource='/credit'
            rows={overdueReceivables}
            columns={[
              ...identity,
              { key: 'draw_id', label: 'Credit draw ID' },
              {
                key: 'principal_minor',
                label: 'Original principal',
                kind: 'money',
              },
              { key: 'paid_minor', label: 'Repaid principal', kind: 'money' },
              { key: 'offset_minor', label: 'Deposit offset', kind: 'money' },
              {
                key: 'written_off_minor',
                label: 'Written-off principal',
                kind: 'money',
              },
              { key: 'corrected_minor', label: 'Corrections', kind: 'money' },
              { key: 'due_at', label: 'Due date', kind: 'date' },
              {
                key: 'term_seconds',
                label: 'Credit term (seconds)',
                kind: 'integer',
              },
              { key: 'policy_version', label: 'Policy version' },
            ]}
            actions={(row) =>
              props.finance
                ? [
                    creditAction(
                      'invoice.issue',
                      'Issue receivable invoice',
                      [
                        {
                          name: 'invoice_number',
                          label: 'Invoice number',
                          max: 96,
                        },
                        evidence,
                      ],
                      { resource_id: row.id, version: row.version }
                    ),
                    creditAction(
                      'adjustment.request',
                      'Request receivable adjustment',
                      [
                        amount,
                        {
                          name: 'adjustment_kind',
                          label: 'Adjustment type',
                          kind: 'select',
                          options: [
                            {
                              value: 'write_off',
                              label: 'Write off without repayment',
                            },
                            {
                              value: 'reverse_write_off',
                              label: 'Reverse prior write-off',
                            },
                            {
                              value: 'correction_credit',
                              label: 'Correct receivable principal',
                            },
                          ],
                        },
                        reason,
                        evidence,
                      ],
                      { resource_id: row.id, version: row.version }
                    ),
                  ]
                : []
            }
          />
          <h3 className='font-medium'>{t('Issued invoices')}</h3>
          <WorkflowTable
            resource='/credit'
            rows={props.data.invoices}
            columns={[
              { key: 'invoice_number', label: 'Invoice number' },
              { key: 'receivable_id', label: 'Receivable ID' },
              {
                key: 'principal_minor',
                label: 'Original principal',
                kind: 'money',
              },
              { key: 'due_at', label: 'Due date', kind: 'date' },
              { key: 'issued_by', label: 'Operator ID' },
            ]}
          />
        </TabsContent>
        <TabsContent value='repayments'>
          <WorkflowTable
            resource='/credit'
            rows={props.data.repayments}
            create={props.finance && account ? [recordRepayment] : []}
            columns={[
              ...identity,
              { key: 'amount_minor', label: 'Repayment amount', kind: 'money' },
              {
                key: 'allocations',
                label: 'Receivable allocations',
                kind: 'allocations',
              },
              { key: 'recorded_by', label: 'Recorded by' },
              { key: 'confirmed_by', label: 'Independent reviewer' },
              { key: 'receipt_id', label: 'Receipt ID' },
              { key: 'created_at', label: 'Created', kind: 'date' },
            ]}
            actions={(row) => {
              if (
                !props.finance ||
                row.state !== 'recorded' ||
                row.agent_user_id === user?.id
              ) {
                return []
              }
              if (row.recorded_by === user?.id) {
                const operation = creditAction(
                  'repayment.reallocate',
                  'Correct recorded repayment allocations',
                  [
                    ...recordRepayment.fields.filter(
                      (field) => field.name === 'allocation_lines'
                    ),
                    evidence,
                  ],
                  { resource_id: row.id, version: row.version }
                )
                operation.transform = (values, body) => {
                  const allocations = parseReceivableAllocations(
                    String(values.allocation_lines),
                    String(row.amount_minor)
                  )
                  delete body.allocation_lines
                  return { ...body, allocations }
                }
                operation.review = [
                  {
                    label: 'Repayment amount (minor units)',
                    after: String(row.amount_minor),
                  },
                ]
                return [operation]
              }
              const operation = creditAction(
                'repayment.confirm',
                'Confirm offline repayment',
                [evidence],
                { resource_id: row.id, version: row.version }
              )
              operation.review = [
                {
                  label: 'Receivable allocations (minor units)',
                  after: JSON.stringify(row.allocations ?? []),
                },
              ]
              return [operation]
            }}
          />
        </TabsContent>
        <TabsContent value='adjustments'>
          <Alert className='mb-4'>
            <AlertDescription>
              {t(
                'A write-off is not a repayment and does not restore available credit. Adjustments require a separate reviewer and preserve the original receivable history.'
              )}
            </AlertDescription>
          </Alert>
          <WorkflowTable
            resource='/credit'
            rows={props.data.adjustments.map((row) => ({
              ...row,
              currency_exponent: account?.currency_exponent ?? null,
            }))}
            columns={[
              ...identity,
              { key: 'receivable_id', label: 'Receivable ID' },
              { key: 'kind', label: 'Adjustment type', kind: 'state' },
              { key: 'amount_minor', label: 'Amount', kind: 'money' },
              { key: 'requested_by', label: 'Requested by' },
              { key: 'reviewed_by', label: 'Independent reviewer' },
              { key: 'reason', label: 'Reason' },
            ]}
            actions={(row) =>
              props.finance &&
              row.state === 'requested' &&
              row.requested_by !== user?.id &&
              row.agent_user_id !== user?.id
                ? [
                    creditAction(
                      'adjustment.approve',
                      'Approve adjustment',
                      [reason, evidence],
                      { resource_id: row.id, version: row.version }
                    ),
                    creditAction(
                      'adjustment.reject',
                      'Reject adjustment',
                      [reason, evidence],
                      { resource_id: row.id, version: row.version }
                    ),
                  ]
                : []
            }
          />
        </TabsContent>
        <TabsContent value='audit'>
          <WorkflowTable
            resource='/credit'
            rows={props.data.audits ?? []}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'action', label: 'Operation' },
              { key: 'actor_user_id', label: 'Operator ID' },
              { key: 'resource_id', label: 'Resource ID' },
              { key: 'state', label: 'Status', kind: 'state' },
              { key: 'reason', label: 'Reason' },
              { key: 'evidence_hash', label: 'Evidence digest' },
              { key: 'created_at', label: 'Created', kind: 'date' },
            ]}
          />
        </TabsContent>
        {props.finance && (
          <TabsContent value='reconciliation'>
            <CreditReconciliation agentUserId={props.agentUserId} />
          </TabsContent>
        )}
        <TabsContent value='ledger'>
          <WorkflowTable
            resource='/credit'
            rows={props.data.ledger}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'kind', label: 'Operation', kind: 'state' },
              {
                key: 'reserved_delta',
                label: 'Reserved principal change',
                kind: 'money',
              },
              {
                key: 'outstanding_delta',
                label: 'Receivable change',
                kind: 'money',
              },
              {
                key: 'written_off_delta',
                label: 'Written-off exposure change',
                kind: 'money',
              },
              {
                key: 'reserved_after',
                label: 'Reserved credit principal',
                kind: 'money',
              },
              {
                key: 'outstanding_after',
                label: 'Outstanding receivables',
                kind: 'money',
              },
              { key: 'created_at', label: 'Created', kind: 'date' },
            ]}
          />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function CreditReconciliation({ agentUserId }: { agentUserId: number }) {
  const { t } = useTranslation()
  const actor = useAuthStore((state) => state.auth.user)
  const query = useQuery({
    queryKey: [
      'agent-platform',
      actor?.id,
      actor?.role,
      '/credit/reconciliation',
      agentUserId,
    ],
    queryFn: ({ signal }) =>
      getPlatform<{
        findings: { code: string; resource_id: number }[]
        external_bank_reconciled: false
      }>('/credit/reconciliation', { agent_user_id: agentUserId }, signal),
  })
  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'These read-only findings check this account’s internal credit journals. No findings does not prove complete bank reconciliation and never changes money.'
        )}
      </p>
      {query.isPending && <LoadingState />}
      {query.isError && (
        <ErrorState
          onRetry={() => {
            void query.refetch()
          }}
        />
      )}
      {!query.isPending && !query.isError && (
        <WorkflowTable
          resource='/credit/reconciliation'
          rows={(query.data?.findings ?? []).map((row, index) => ({
            ...row,
            id: index,
          }))}
          columns={[
            { key: 'code', label: 'Exception code' },
            { key: 'resource_id', label: 'Resource ID' },
          ]}
        />
      )}
    </div>
  )
}
