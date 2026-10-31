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
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import type { AgentPolicy } from '../types'
import type { ActionField } from './action-form'
import { CampaignClaimDialog } from './campaign-claim'
import type { WorkflowAction } from './workflow-operation'
import {
  WorkflowTable,
  WorkflowValue,
  type WorkflowColumn,
  type WorkflowRow,
} from './workflow-table'

const campaignColumns: WorkflowColumn[] = [
  { key: 'name', label: 'Campaign name' },
  { key: 'status', label: 'Status', kind: 'state' },
  { key: 'grant_quota', label: 'Grant quota', kind: 'quota' },
  { key: 'claim_start_at', label: 'Claim window opens', kind: 'date' },
  { key: 'claim_end_at', label: 'Claim window closes', kind: 'date' },
  { key: 'expires_at', label: 'Benefit expires', kind: 'date' },
  { key: 'manual_review', label: 'Manual review required', kind: 'boolean' },
  { key: 'eligibility_reason', label: 'Eligibility', kind: 'state' },
]
const termColumns: WorkflowColumn[] = [
  ...campaignColumns,
  { key: 'family_key', label: 'Campaign family' },
  { key: 'exclusion_group', label: 'Mutual exclusion group' },
  { key: 'total_budget_quota', label: 'Total quota budget', kind: 'quota' },
  { key: 'daily_budget_quota', label: 'Daily quota budget', kind: 'quota' },
  {
    key: 'per_agent_budget_quota',
    label: 'Per-agent quota budget',
    kind: 'quota',
  },
  {
    key: 'require_linuxdo',
    label: 'Verified LinuxDO binding required',
    kind: 'boolean',
  },
  {
    key: 'require_agent_invitation',
    label: 'Agent invitation required',
    kind: 'boolean',
  },
  {
    key: 'review_window_seconds',
    label: 'Review window (seconds)',
    kind: 'integer',
  },
  { key: 'refund_terms', label: 'Refund restoration rule', kind: 'state' },
  {
    key: 'revocable',
    label: 'Original terms permit revocation',
    kind: 'boolean',
  },
  { key: 'policy_version', label: 'Policy version' },
  { key: 'terms_hash', label: 'Terms digest' },
]
const campaignFields: ActionField[] = [
  { name: 'name', label: 'Campaign name', max: 96 },
  { name: 'family_key', label: 'Campaign family', max: 64 },
  {
    name: 'exclusion_group',
    label: 'Mutual exclusion group',
    required: false,
    max: 64,
  },
  { name: 'grant_quota', label: 'Grant quota units', kind: 'integer', min: 1 },
  {
    name: 'total_budget_quota',
    label: 'Total quota budget',
    kind: 'integer',
    min: 1,
  },
  {
    name: 'daily_budget_quota',
    label: 'Daily quota budget',
    kind: 'integer',
    min: 1,
  },
  {
    name: 'per_agent_budget_quota',
    label: 'Per-agent quota budget',
    kind: 'integer',
    min: 1,
  },
  { name: 'claim_start_at', label: 'Claim window opens', kind: 'datetime' },
  { name: 'claim_end_at', label: 'Claim window closes', kind: 'datetime' },
  { name: 'expires_at', label: 'Benefit expires', kind: 'datetime' },
  {
    name: 'require_linuxdo',
    label: 'Verified LinuxDO binding required',
    kind: 'checkbox',
  },
  {
    name: 'require_agent_invitation',
    label: 'Agent invitation required',
    kind: 'checkbox',
  },
  {
    name: 'manual_review',
    label: 'Manual review required',
    kind: 'checkbox',
    description:
      'If unchecked, eligible claims may receive noncash quota automatically under approved terms. This never sends cash.',
  },
  {
    name: 'review_window_seconds',
    label: 'Review window (seconds)',
    kind: 'integer',
    min: 0,
    max: 2592000,
  },
  {
    name: 'refund_terms',
    label: 'Refund restoration rule',
    kind: 'select',
    options: [
      {
        value: 'restore_original_expiry',
        label: 'Restore the original source and expiry only',
      },
    ],
  },
  {
    name: 'revocable',
    label: 'Original terms permit revocation',
    kind: 'checkbox',
  },
  { name: 'reason', label: 'Reason', kind: 'textarea' },
]
export function CampaignPanel(props: { finance?: boolean }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [selected, setSelected] = useState<WorkflowRow | null>(null)
  const [claim, setClaim] = useState<WorkflowRow | null>(null)
  const policy = useQuery({
    queryKey: ['agent-platform', user?.id, 'policy'],
    queryFn: ({ signal }) =>
      getPlatform<AgentPolicy>('/policy/current', undefined, signal),
  })
  const prefix = props.finance ? '/admin' : ''
  const proposal: WorkflowAction = {
    action: 'campaign.propose',
    label: 'Propose campaign version',
    endpoint: '/admin/campaigns/propose',
    fields: campaignFields,
    includeRequestId: true,
    initial: { manual_review: true },
    description:
      'Campaign benefits are noncash, source-tracked and subject to the approved budget and expiry. Historical benefits are not retroactively expired.',
    transform: (values, body) => {
      if (
        !/^[a-z0-9_-]{1,64}$/.test(String(body.family_key)) ||
        (body.exclusion_group &&
          !/^[a-z0-9_-]{1,64}$/.test(String(body.exclusion_group)))
      ) {
        throw new Error(
          t(
            'Use lowercase letters, digits, hyphens or underscores for campaign keys.'
          )
        )
      }
      if (
        Number(body.claim_end_at) <= Number(body.claim_start_at) ||
        Number(body.expires_at) <= Number(body.claim_end_at)
      ) {
        throw new Error(
          t('Claim and expiry windows must be ordered and non-overlapping.')
        )
      }
      if (
        Number(body.grant_quota) > Number(body.daily_budget_quota) ||
        Number(body.daily_budget_quota) > Number(body.total_budget_quota) ||
        Number(body.grant_quota) > Number(body.per_agent_budget_quota) ||
        Number(body.per_agent_budget_quota) > Number(body.total_budget_quota)
      ) {
        throw new Error(
          t(
            'Each grant must fit the daily and per-agent budgets, and both must fit the total budget.'
          )
        )
      }
      if (
        (body.manual_review === true &&
          Number(body.review_window_seconds) < 60) ||
        (body.manual_review !== true &&
          Number(body.review_window_seconds) !== 0)
      ) {
        throw new Error(
          t(
            'Manual review requires at least 60 seconds; automatic noncash grants require an explicit zero review window.'
          )
        )
      }
      return {
        terms: {
          ...body,
          exclusion_group: String(values.exclusion_group ?? ''),
        },
      }
    },
  }
  const claimActions = (row: WorkflowRow): WorkflowAction[] => {
    if (
      !props.finance ||
      row.user_id === user?.id ||
      !Array.isArray(row.allowed_actions)
    ) {
      return []
    }
    return [
      'campaign.review.approve',
      'campaign.review.reject',
      'campaign.revoke',
    ]
      .filter(
        (action) =>
          Array.isArray(row.allowed_actions) &&
          row.allowed_actions.includes(action)
      )
      .map((action) => ({
        action,
        label:
          {
            'campaign.review.approve': 'Approve campaign grant',
            'campaign.review.reject': 'Reject campaign grant',
            'campaign.revoke': 'Revoke remaining source benefit',
          }[action] ?? '',
        endpoint:
          action === 'campaign.revoke'
            ? '/admin/campaigns/revoke'
            : '/admin/campaigns/review',
        includeRequestId: true,
        fields: [{ name: 'reason', label: 'Reason', kind: 'textarea' }],
        payload: {
          claim_id: row.id,
          expected_version: row.version,
          ...(action.startsWith('campaign.review.')
            ? { decision: action.endsWith('approve') ? 'approve' : 'reject' }
            : {}),
        },
      }))
  }
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Campaign benefits are noncash, source-tracked and subject to the approved budget and expiry. Historical benefits are not retroactively expired.'
          )}
        </AlertDescription>
      </Alert>
      <Tabs defaultValue='campaigns'>
        <TabsList
          className='h-auto max-w-full flex-wrap'
          aria-label={t('Campaign records')}
        >
          <TabsTrigger value='campaigns'>{t('Campaigns')}</TabsTrigger>
          <TabsTrigger value='claims'>{t('Campaign claims')}</TabsTrigger>
          <TabsTrigger value='sources'>{t('Bonus sources')}</TabsTrigger>
          <TabsTrigger value='allocations'>{t('Source debits')}</TabsTrigger>
          <TabsTrigger value='restorations'>
            {t('Source restorations')}
          </TabsTrigger>
          <TabsTrigger value='removals'>{t('Source removals')}</TabsTrigger>
          {props.finance && (
            <>
              <TabsTrigger value='budgets'>{t('Campaign budgets')}</TabsTrigger>
              <TabsTrigger value='anomalies'>
                {t('Bonus reconciliation exceptions')}
              </TabsTrigger>
            </>
          )}
        </TabsList>
        <TabsContent value='campaigns'>
          <WorkflowTable
            resource={`${prefix}/campaigns`}
            columns={campaignColumns}
            create={
              props.finance && user?.role === ROLE.SUPER_ADMIN ? [proposal] : []
            }
            disabled={
              !policy.data?.campaign_enabled ||
              !policy.data.campaign_terms_approved
            }
            onInspect={setSelected}
            actions={(row) => {
              if (
                !props.finance ||
                !Array.isArray(row.allowed_actions) ||
                !row.allowed_actions.includes('campaign.approve') ||
                row.proposed_by === user?.id
              ) {
                return []
              }
              return [
                {
                  action: 'campaign.approve',
                  label: 'Approve campaign version',
                  endpoint: '/admin/campaigns/approve',
                  includeRequestId: true,
                  fields: [
                    { name: 'reason', label: 'Reason', kind: 'textarea' },
                  ],
                  payload: {
                    campaign_id: row.id,
                    expected_version: row.version,
                  },
                },
              ]
            }}
          />
        </TabsContent>
        <TabsContent value='claims'>
          <WorkflowTable
            resource={`${prefix}/campaign-claims`}
            columns={[
              { key: 'id', label: 'Claim ID' },
              { key: 'campaign_id', label: 'Campaign ID' },
              { key: 'status', label: 'Status', kind: 'state' },
              { key: 'user_id', label: 'User ID' },
              { key: 'quota', label: 'Grant quota', kind: 'quota' },
              {
                key: 'review_deadline',
                label: 'Review deadline',
                kind: 'date',
              },
              { key: 'created_at', label: 'Created', kind: 'date' },
            ]}
            actions={claimActions}
          />
        </TabsContent>
        <TabsContent value='sources' className='space-y-4'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Outstanding source quota includes original debits and unresolved holds; it is not proof of consumption. Refunds restore the original source and expiry, never a new gift.'
            )}
          </p>
          <WorkflowTable
            resource={`${prefix}/bonus-sources`}
            columns={[
              { key: 'lot.id', label: 'Source ID' },
              { key: 'lot.kind', label: 'Source type', kind: 'state' },
              { key: 'lot.user_id', label: 'User ID' },
              {
                key: 'lot.grant_quota',
                label: 'Original quota',
                kind: 'quota',
              },
              {
                key: 'state.available',
                label: 'Available quota',
                kind: 'quota',
              },
              {
                key: 'state.outstanding',
                label: 'Outstanding source debits',
                kind: 'quota',
              },
              { key: 'state.expired', label: 'Expired quota', kind: 'quota' },
              { key: 'state.revoked', label: 'Revoked quota', kind: 'quota' },
              {
                key: 'state.legacy_restored',
                label: 'Already restored before cutover',
                kind: 'quota',
              },
              { key: 'lot.expires_at', label: 'Benefit expires', kind: 'date' },
              {
                key: 'lot.refund_terms',
                label: 'Refund restoration rule',
                kind: 'state',
              },
            ]}
          />
        </TabsContent>
        <TabsContent value='allocations'>
          <WorkflowTable
            resource={`${prefix}/bonus-allocations`}
            columns={[
              { key: 'id', label: 'Allocation ID' },
              { key: 'lot_id', label: 'Source ID' },
              { key: 'ledger_id', label: 'Ledger entry ID' },
              { key: 'reference_type', label: 'Reference type', kind: 'state' },
              { key: 'reference_id', label: 'Reference ID' },
              { key: 'amount', label: 'Allocated quota', kind: 'quota' },
              { key: 'refunded', label: 'Restored quota', kind: 'quota' },
              { key: 'billing_state', label: 'Billing state', kind: 'state' },
              { key: 'task_state', label: 'Task state', kind: 'state' },
              { key: 'reason', label: 'Reason', kind: 'state' },
              { key: 'created_at', label: 'Created', kind: 'date' },
            ]}
          />
        </TabsContent>
        <TabsContent value='restorations'>
          <WorkflowTable
            resource={`${prefix}/bonus-restorations`}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'allocation_id', label: 'Allocation ID' },
              { key: 'ledger_id', label: 'Ledger entry ID' },
              { key: 'quota', label: 'Restored quota', kind: 'quota' },
              { key: 'created_at', label: 'Created', kind: 'date' },
            ]}
          />
        </TabsContent>
        <TabsContent value='removals'>
          <WorkflowTable
            resource={`${prefix}/bonus-removals`}
            columns={[
              { key: 'id', label: 'ID' },
              { key: 'lot_id', label: 'Source ID' },
              { key: 'ledger_id', label: 'Ledger entry ID' },
              { key: 'kind', label: 'Removal reason', kind: 'state' },
              { key: 'quota', label: 'Removed quota', kind: 'quota' },
              { key: 'created_at', label: 'Created', kind: 'date' },
            ]}
          />
        </TabsContent>
        {props.finance && (
          <>
            <TabsContent value='budgets'>
              <WorkflowTable
                resource='/admin/campaign-budgets'
                columns={[
                  { key: 'id', label: 'Budget ID' },
                  { key: 'campaign_id', label: 'Campaign ID' },
                  { key: 'scope', label: 'Budget scope', kind: 'state' },
                  { key: 'scope_key', label: 'Budget scope key' },
                  { key: 'limit_quota', label: 'Quota budget', kind: 'quota' },
                  {
                    key: 'reserved_quota',
                    label: 'Reserved quota',
                    kind: 'quota',
                  },
                  { key: 'spent_quota', label: 'Granted quota', kind: 'quota' },
                  {
                    key: 'released_quota',
                    label: 'Released quota',
                    kind: 'quota',
                  },
                ]}
              />
            </TabsContent>
            <TabsContent value='anomalies'>
              <WorkflowTable
                resource='/admin/bonus-anomalies'
                columns={[
                  { key: 'id', label: 'ID' },
                  { key: 'user_id', label: 'User ID' },
                  { key: 'code', label: 'Exception code' },
                  { key: 'detail', label: 'Details' },
                  { key: 'created_at', label: 'Created', kind: 'date' },
                ]}
              />
            </TabsContent>
          </>
        )}
      </Tabs>
      {selected && (
        <Dialog
          open
          title={t('Campaign terms')}
          onOpenChange={(open) => {
            if (!open) setSelected(null)
          }}
          footer={
            !props.finance ? (
              <Button
                disabled={selected.can_claim !== true}
                onClick={() => {
                  setClaim(selected)
                  setSelected(null)
                }}
              >
                {t('Review campaign claim')}
              </Button>
            ) : undefined
          }
        >
          <dl className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
            {termColumns.map((column) => (
              <div key={column.key}>
                <dt className='text-muted-foreground text-sm'>
                  {t(column.label)}
                </dt>
                <dd>
                  <WorkflowValue row={selected} column={column} />
                </dd>
              </div>
            ))}
          </dl>
        </Dialog>
      )}
      {claim && (
        <CampaignClaimDialog campaign={claim} onClose={() => setClaim(null)} />
      )}
    </div>
  )
}
