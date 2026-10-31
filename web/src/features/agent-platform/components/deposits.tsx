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

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import { parseReceivableAllocations } from '../lib/financial-inputs'
import type { AgentPolicy, OperationPayload } from '../types'
import { AccountWorkspace } from './account-workspace'
import type { ActionField } from './action-form'
import { ManualRefundInstructionsButton } from './manual-refund-instructions'
import type { WorkflowAction } from './workflow-operation'
import {
  WorkflowTable,
  WorkflowValue,
  type WorkflowColumn,
  type WorkflowRow,
} from './workflow-table'

interface DepositOverview {
  account: OperationPayload | null
  receipts: WorkflowRow[]
  requests: WorkflowRow[]
  ledger: WorkflowRow[]
  audits?: WorkflowRow[]
}
const evidence: ActionField = {
  name: 'evidence_reference',
  label: 'Controlled evidence dossier ID',
  description:
    'Use controlled dossier IDs only. Do not paste identity documents, bank account numbers or credentials.',
}
const payer: ActionField = {
  name: 'payer_reference',
  label: 'Verified original payer dossier ID',
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
function depositAction(
  action: string,
  label: string,
  fields: ActionField[],
  payload: OperationPayload
): WorkflowAction {
  return {
    action: `deposit.${action}`,
    label,
    fields,
    payload,
    endpoint: `/deposit/actions/${action}`,
  }
}
export function DepositPanel(props: {
  finance?: boolean
  settlementEnabled?: boolean
}) {
  return (
    <AccountWorkspace<DepositOverview>
      resource='/deposit'
      finance={props.finance}
    >
      {(data, agentUserId) => (
        <DepositRecords
          key={agentUserId}
          {...props}
          data={data}
          agentUserId={agentUserId}
        />
      )}
    </AccountWorkspace>
  )
}
function DepositRecords(props: {
  data: DepositOverview
  agentUserId: number
  finance?: boolean
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
  const enabled =
    policy.data?.deposit_enabled === true &&
    policy.data.deposit_terms_approved === true
  const summaryColumns: WorkflowColumn[] = [
    {
      key: 'received_minor',
      label: 'Verified deposits received',
      kind: 'money',
    },
    { key: 'available_minor', label: 'Available deposit', kind: 'money' },
    { key: 'held_minor', label: 'Held deposit', kind: 'money' },
    { key: 'offset_minor', label: 'Deposit offset', kind: 'money' },
    { key: 'refunded_minor', label: 'Returned deposits', kind: 'money' },
  ]
  const record = depositAction(
    'receipt.record',
    'Record offline deposit',
    [
      amount,
      payer,
      { name: 'merchant_scope', label: 'Bank ledger scope' },
      { name: 'external_reference', label: 'Bank transaction reference' },
      { name: 'paid_at', label: 'Payment date', kind: 'datetime' },
      evidence,
    ],
    {
      agent_user_id: props.agentUserId,
      provider: 'bank_transfer',
      environment: 'live',
      currency: String(
        account?.currency ?? policy.data?.deposit_currency ?? ''
      ),
      currency_exponent: Number(
        account?.currency_exponent ??
          policy.data?.deposit_currency_exponent ??
          0
      ),
    }
  )
  const receiptActions = (row: WorkflowRow): WorkflowAction[] => {
    if (props.finance) {
      if (
        row.state === 'recorded' &&
        row.recorded_by !== user?.id &&
        row.agent_user_id !== user?.id
      ) {
        return [
          depositAction(
            'receipt.confirm',
            'Confirm deposit receipt',
            [payer, evidence],
            { resource_id: row.id, version: row.version }
          ),
        ]
      }
      return []
    }
    if (
      !enabled ||
      !props.settlementEnabled ||
      row.state !== 'confirmed' ||
      !/^[1-9]\d*$/.test(String(row.available_minor))
    ) {
      return []
    }
    const actions: WorkflowAction[] = []
    if (policy.data?.deposit_refund_enabled) {
      actions.push(
        depositAction(
          'refund.request',
          'Request deposit return',
          [amount, reason],
          { deposit_id: row.id }
        )
      )
    }
    if (policy.data?.deposit_offset_enabled) {
      const action = depositAction(
        'offset.request',
        'Request reviewed deposit offset',
        [
          amount,
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
          reason,
        ],
        { deposit_id: row.id }
      )
      action.transform = (values, payload) => {
        const allocations = parseReceivableAllocations(
          String(values.allocation_lines),
          String(values.amount_minor)
        )
        delete payload.allocation_lines
        return { ...payload, allocations }
      }
      actions.push(action)
    }
    return actions
  }
  const requestActions = (row: WorkflowRow): WorkflowAction[] => {
    if (!props.finance || row.agent_user_id === user?.id) return []
    const payload = { resource_id: row.id, version: row.version }
    if (row.state === 'requested' && row.requested_by !== user?.id) {
      return [
        {
          ...depositAction(
            `${row.kind}.approve`,
            'Approve deposit request',
            [reason, evidence],
            payload
          ),
          review: [
            {
              label: 'Receivable allocations (minor units)',
              after: JSON.stringify(row.allocations ?? []),
            },
            {
              label: 'Original recipient digest',
              after: String(row.recipient_digest ?? ''),
            },
          ],
        },
        depositAction(
          `${row.kind}.reject`,
          'Reject deposit request',
          [reason, evidence],
          payload
        ),
      ]
    }
    if (row.kind !== 'refund') return []
    if (row.state === 'approved') {
      return [
        depositAction(
          'refund.submit',
          'Record offline deposit payment',
          [evidence],
          { ...payload, business_key: row.business_key }
        ),
      ]
    }
    if (
      (row.state !== 'payment_pending' && row.state !== 'payment_unknown') ||
      row.submitted_by === user?.id
    ) {
      return []
    }
    const resolve = depositAction(
      'refund.resolve',
      'Reconcile deposit payment result',
      [
        {
          name: 'outcome',
          label: 'Actual payment result',
          kind: 'select',
          options: [
            { value: 'success', label: 'Confirmed successful' },
            { value: 'failure', label: 'Confirmed unpaid' },
            { value: 'unknown', label: 'Unknown; keep funds held' },
          ],
        },
        evidence,
        { name: 'merchant_scope', label: 'Bank ledger scope', required: false },
        {
          name: 'external_reference',
          label: 'Bank transaction reference',
          required: false,
        },
        {
          name: 'paid_at',
          label: 'Actual payment time',
          kind: 'datetime',
          required: false,
        },
        {
          name: 'no_payment_confirmed',
          label:
            'I have verified that no payment occurred (failed result only)',
          kind: 'checkbox',
        },
      ],
      { ...payload, business_key: row.business_key }
    )
    resolve.description =
      'Manual processing only. Keep this business number for submission and reconciliation. An unknown payment must never be sent again.'
    resolve.transform = (values, body) => {
      if (values.outcome === 'success') {
        if (
          !String(values.merchant_scope).trim() ||
          !String(values.external_reference).trim() ||
          !body.paid_at
        ) {
          throw new Error(
            t(
              'Successful offline payments require a bank ledger scope and unique transaction reference.'
            )
          )
        }
        delete body.no_payment_confirmed
        return { ...body, provider: 'bank_transfer', environment: 'live' }
      }
      delete body.merchant_scope
      delete body.external_reference
      delete body.paid_at
      if (
        values.outcome === 'failure' &&
        values.no_payment_confirmed !== true
      ) {
        throw new Error(
          t('Confirm that no payment occurred before releasing held funds.')
        )
      }
      if (values.outcome === 'unknown') delete body.no_payment_confirmed
      return body
    }
    return [resolve]
  }
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Deposits are separate liabilities, not wallet credit or sales revenue. Offsets require an explicit request and independent review. Returns go only to the original verified sender through offline human payment; unknown results stay held.'
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
                <WorkflowValue row={account} column={column} />
              </dd>
            </div>
          ))}
        </dl>
      ) : (
        <p>{t('No deposit liability has been established.')}</p>
      )}
      <Tabs defaultValue='receipts'>
        <TabsList
          className='h-auto flex-wrap'
          aria-label={t('Deposit records')}
        >
          <TabsTrigger value='receipts'>{t('Deposit receipts')}</TabsTrigger>
          <TabsTrigger value='requests'>{t('Deposit requests')}</TabsTrigger>
          <TabsTrigger value='ledger'>{t('Deposit ledger')}</TabsTrigger>
          <TabsTrigger value='audit'>{t('Review audit')}</TabsTrigger>
        </TabsList>
        <TabsContent value='receipts'>
          <WorkflowTable
            resource='/deposit'
            rows={props.data.receipts}
            create={props.finance ? [record] : []}
            disabled={!enabled || props.agentUserId === user?.id}
            columns={[
              { key: 'id', label: 'Deposit receipt ID' },
              { key: 'state', label: 'Status', kind: 'state' },
              { key: 'amount_minor', label: 'Original amount', kind: 'money' },
              {
                key: 'available_minor',
                label: 'Available deposit',
                kind: 'money',
              },
              { key: 'held_minor', label: 'Held deposit', kind: 'money' },
              { key: 'offset_minor', label: 'Deposit offset', kind: 'money' },
              {
                key: 'refunded_minor',
                label: 'Returned deposits',
                kind: 'money',
              },
              { key: 'payer_digest', label: 'Verified payer digest' },
              { key: 'recorded_by', label: 'Recorded by' },
              { key: 'confirmed_by', label: 'Independent reviewer' },
            ]}
            actions={receiptActions}
          />
        </TabsContent>
        <TabsContent value='requests'>
          <WorkflowTable
            resource='/deposit'
            rows={props.data.requests}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'business_key', label: 'Payment business number' },
              { key: 'kind', label: 'Operation', kind: 'state' },
              { key: 'state', label: 'Status', kind: 'state' },
              { key: 'deposit_id', label: 'Deposit receipt ID' },
              { key: 'amount_minor', label: 'Amount', kind: 'money' },
              { key: 'requested_by', label: 'Requested by' },
              { key: 'approved_by', label: 'Approver' },
              { key: 'submitted_by', label: 'Payment operator' },
              { key: 'reason', label: 'Reason' },
            ]}
            actions={requestActions}
            renderActions={(row) =>
              props.finance &&
              row.agent_user_id !== user?.id &&
              row.kind === 'refund' &&
              ['approved', 'payment_pending', 'payment_unknown'].includes(
                String(row.state)
              ) ? (
                <ManualRefundInstructionsButton
                  kind='deposit'
                  target={{
                    id: row.id,
                    business_key: String(row.business_key),
                    state: String(row.state),
                    version: Number(row.version),
                    currency: String(row.currency),
                    currency_exponent: Number(row.currency_exponent),
                    amount_minor: String(row.amount_minor),
                  }}
                />
              ) : null
            }
          />
        </TabsContent>
        <TabsContent value='audit'>
          <WorkflowTable
            resource='/deposit'
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
        <TabsContent value='ledger'>
          <WorkflowTable
            resource='/deposit'
            rows={props.data.ledger}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'kind', label: 'Operation', kind: 'state' },
              {
                key: 'available_delta',
                label: 'Available deposit change',
                kind: 'money',
              },
              {
                key: 'held_delta',
                label: 'Held deposit change',
                kind: 'money',
              },
              {
                key: 'offset_delta',
                label: 'Deposit offset change',
                kind: 'money',
              },
              {
                key: 'refunded_delta',
                label: 'Returned deposit change',
                kind: 'money',
              },
              {
                key: 'available_after',
                label: 'Available deposit',
                kind: 'money',
              },
              { key: 'held_after', label: 'Held deposit', kind: 'money' },
              { key: 'created_at', label: 'Created', kind: 'date' },
            ]}
          />
        </TabsContent>
      </Tabs>
    </div>
  )
}
