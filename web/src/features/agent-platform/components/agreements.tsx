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
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import { currencyOptions, validCurrencyExponent } from '../lib/currencies'
import { parseAgreementTiers } from '../lib/financial-inputs'
import type { AgentPolicy, OperationPayload } from '../types'
import type { ActionField } from './action-form'
import type { WorkflowAction } from './workflow-operation'
import {
  WorkflowTable,
  WorkflowValue,
  type WorkflowColumn,
  type WorkflowRow,
} from './workflow-table'

const agreementColumns: WorkflowColumn[] = [
  { key: 'agreement_key', label: 'Agreement key' },
  { key: 'terms_version', label: 'Terms version' },
  { key: 'approval.state', label: 'Status', kind: 'state' },
  { key: 'scope', label: 'Pricing scope', kind: 'state' },
  { key: 'agent_user_id', label: 'Agent ID' },
  { key: 'currency', label: 'Currency' },
  {
    key: 'terms.base_rate_bps',
    label: 'Base price (basis points)',
    kind: 'integer',
  },
  { key: 'effective_from', label: 'Effective from', kind: 'date' },
  { key: 'effective_until', label: 'Effective until', kind: 'date' },
  { key: 'policy_version', label: 'Policy version' },
]
export function AgreementPanel() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [selected, setSelected] = useState<WorkflowRow | null>(null)
  const policy = useQuery({
    queryKey: ['agent-platform', user?.id, 'policy'],
    queryFn: ({ signal }) =>
      getPlatform<AgentPolicy>('/policy/current', undefined, signal),
  })
  const fields: ActionField[] = [
    { name: 'agreement_key', label: 'Agreement key', max: 96 },
    { name: 'terms_version', label: 'Terms version', kind: 'integer', min: 1 },
    {
      name: 'scope',
      label: 'Pricing scope',
      kind: 'select',
      options: [
        { value: 'agreement', label: 'Individual agent agreement' },
        { value: 'tier', label: 'Global tier schedule' },
      ],
    },
    {
      name: 'agent_user_id',
      label: 'Agent ID (individual agreement only)',
      kind: 'integer',
      min: 1,
      required: false,
    },
    {
      name: 'currency',
      label: 'Currency',
      kind: 'select',
      options: currencyOptions('currency_exponent'),
    },
    {
      name: 'currency_exponent',
      label: 'Currency decimal places',
      kind: 'integer',
      max: 6,
      readOnly: true,
    },
    {
      name: 'quota_numerator',
      label: 'Quota conversion numerator',
      kind: 'minor',
      min: 1,
    },
    {
      name: 'quota_denominator',
      label: 'Quota conversion denominator',
      kind: 'minor',
      min: 1,
    },
    {
      name: 'base_rate_bps',
      label: 'Base price (basis points)',
      kind: 'integer',
      min: 1,
      max: 10000,
    },
    { name: 'effective_from', label: 'Effective from', kind: 'datetime' },
    { name: 'effective_until', label: 'Effective until', kind: 'datetime' },
    {
      name: 'dossier_reference',
      label: 'Controlled agreement dossier ID',
      max: 128,
    },
    {
      name: 'tier_lines',
      label: 'Pricing tiers',
      kind: 'textarea',
      max: 12000,
      required: false,
      description:
        'Optional rows: key, basis, minimum, maximum, rate in basis points. Basis is quota, face_minor or base_paid_minor. Ranges include the minimum and exclude the maximum.',
      validate: (value) => {
        try {
          parseAgreementTiers(String(value))
          return undefined
        } catch {
          return t(
            'Enter valid non-overlapping pricing tiers with one threshold unit.'
          )
        }
      },
    },
    { name: 'reason', label: 'Reason', kind: 'textarea' },
  ]
  const propose: WorkflowAction = {
    label: 'Propose pricing agreement',
    action: 'agreement.propose',
    endpoint: '/agreements/propose',
    fields,
    transform: (values, body) => {
      if (
        !validCurrencyExponent(
          String(body.currency),
          Number(body.currency_exponent)
        )
      ) {
        throw new Error(
          t(
            'Original currency and decimal places do not match the supported currency catalog.'
          )
        )
      }
      if (Number(body.effective_until) <= Number(body.effective_from)) {
        throw new Error(t('The effective end must be after the start.'))
      }
      if (
        body.scope === 'agreement' &&
        (!body.agent_user_id || body.agent_user_id === user?.id)
      ) {
        throw new Error(
          t('Select another eligible agent for an individual agreement.')
        )
      }
      if (body.scope === 'tier' && body.agent_user_id) {
        throw new Error(
          t('Global tier schedules must not target an individual agent.')
        )
      }
      const reason = body.reason
      const tiers = parseAgreementTiers(String(values.tier_lines ?? ''))
      const floor = policy.data?.agreement_minimum_rate_bps
      if (
        !floor ||
        Number(body.base_rate_bps) < floor ||
        tiers.some((tier) => tier.rate_bps < floor)
      ) {
        throw new Error(
          t(
            'All agreement rates must meet the explicitly approved minimum price.'
          )
        )
      }
      if (
        !policy.data ||
        String(body.currency) !== policy.data.purchase_currency ||
        BigInt(String(body.quota_numerator)) *
          BigInt(policy.data.purchase_quota_denominator) !==
          BigInt(String(body.quota_denominator)) *
            BigInt(policy.data.purchase_quota_numerator)
      ) {
        throw new Error(
          t(
            'Agreement currency and quota units must match the current approved procurement policy.'
          )
        )
      }
      delete body.reason
      delete body.tier_lines
      return {
        reason,
        terms: {
          ...body,
          agent_user_id: body.scope === 'tier' ? 0 : body.agent_user_id,
          tiers,
        },
      }
    },
  }
  const detailTerms =
    selected?.terms &&
    typeof selected.terms === 'object' &&
    !Array.isArray(selected.terms)
      ? selected.terms
      : null
  const tiers = Array.isArray(detailTerms?.tiers)
    ? detailTerms.tiers
        .filter(
          (row): row is OperationPayload =>
            !!row && typeof row === 'object' && !Array.isArray(row)
        )
        .map((row, index) => ({ ...row, id: index }))
    : []
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Approved individual agreements take precedence over global tier schedules, then the global policy. Tier rates replace the base rate; they never compound. Accepted orders retain their original price snapshot.'
          )}
        </AlertDescription>
      </Alert>
      <WorkflowTable
        resource='/agreements'
        columns={agreementColumns}
        create={[propose]}
        disabled={
          !policy.data?.agreement_pricing_enabled ||
          !policy.data.purchase_enabled
        }
        onInspect={setSelected}
        actions={(row) => {
          const approval = row.approval
          if (
            !approval ||
            typeof approval !== 'object' ||
            Array.isArray(approval) ||
            row.agent_user_id === user?.id
          ) {
            return []
          }
          const actions: string[] = []
          if (approval.state === 'proposed' && row.created_by !== user?.id) {
            actions.push('approve', 'reject')
          }
          if (approval.state === 'approved') actions.push('revoke')
          return actions.map((action) => ({
            action: `agreement.${action}`,
            label:
              {
                approve: 'Approve pricing agreement',
                reject: 'Reject pricing agreement',
                revoke: 'Revoke future agreement pricing',
              }[action] ?? '',
            endpoint: `/agreements/${action}`,
            fields: [{ name: 'reason', label: 'Reason', kind: 'textarea' }],
            payload: {
              agreement_id: row.id,
              expected_version: approval.version,
            },
          }))
        }}
      />
      {selected && (
        <Dialog
          open
          title={t('Pricing agreement terms')}
          onOpenChange={(open) => {
            if (!open) setSelected(null)
          }}
        >
          <dl className='mb-5 grid grid-cols-1 gap-3 sm:grid-cols-2'>
            {[
              ...agreementColumns,
              {
                key: 'terms.quota_numerator',
                label: 'Quota conversion numerator',
              },
              {
                key: 'terms.quota_denominator',
                label: 'Quota conversion denominator',
              },
              {
                key: 'terms.dossier_reference',
                label: 'Controlled agreement dossier ID',
              },
              { key: 'terms_digest', label: 'Terms digest' },
              { key: 'profile_version', label: 'Permission version' },
              { key: 'approval.approved_by', label: 'Approver' },
            ].map((column) => (
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
          <WorkflowTable
            resource='/agreements'
            rows={tiers}
            columns={[
              { key: 'key', label: 'Tier key' },
              { key: 'basis', label: 'Threshold unit', kind: 'state' },
              { key: 'minimum', label: 'Inclusive minimum' },
              { key: 'maximum', label: 'Exclusive maximum' },
              {
                key: 'rate_bps',
                label: 'Price (basis points)',
                kind: 'integer',
              },
            ]}
          />
        </Dialog>
      )}
    </div>
  )
}
