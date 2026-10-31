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
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform } from '../api'
import { validGovernanceReference } from '../lib/financial-inputs'
import type { AgentPolicy, OperationPayload } from '../types'
import { AccountWorkspace } from './account-workspace'
import type { ActionField } from './action-form'
import { BusinessState, ExactQuota } from './values'
import type { WorkflowAction } from './workflow-operation'
import {
  WorkflowTable,
  WorkflowValue,
  type WorkflowColumn,
  type WorkflowRow,
} from './workflow-table'

interface GovernanceView {
  profile: OperationPayload | null
  governance: OperationPayload | null
  submission: OperationPayload | null
  review_required: boolean
  review_reasons: string[]
  audit: WorkflowRow[]
  daily_risk?: OperationPayload
}
const governanceColumns: WorkflowColumn[] = [
  { key: 'status', label: 'Qualification status', kind: 'state' },
  { key: 'version', label: 'Qualification version' },
  {
    key: 'identity_verified',
    label: 'Identity evidence verified',
    kind: 'boolean',
  },
  {
    key: 'agreement_verified',
    label: 'Agreement evidence verified',
    kind: 'boolean',
  },
  { key: 'exceptional', label: 'Documented exception', kind: 'boolean' },
  { key: 'reviewed_by', label: 'Independent reviewer' },
  { key: 'review_due_at', label: 'Next review due', kind: 'date' },
  {
    key: 'last_business_activity_at',
    label: 'Last recorded issuance or transfer',
    kind: 'date',
  },
  {
    key: 'risk_limits.enabled',
    label: 'Daily risk limits enabled',
    kind: 'boolean',
  },
  {
    key: 'risk_limits.daily_issuance_quota',
    label: 'Daily gross issuance cap',
    kind: 'quota',
  },
  {
    key: 'risk_limits.daily_transfer_quota',
    label: 'Daily gross transfer cap',
    kind: 'quota',
  },
]
export function GovernancePanel(props: { finance?: boolean }) {
  return (
    <AccountWorkspace<GovernanceView>
      resource='/governance/self'
      finance={props.finance}
      paginate={false}
      pathForTarget={
        props.finance ? (id) => `/finance/governance/${id}` : undefined
      }
    >
      {(data, userId) => (
        <GovernanceRecords
          key={userId}
          {...props}
          data={data}
          userId={userId}
        />
      )}
    </AccountWorkspace>
  )
}
function GovernanceRecords(props: {
  data: GovernanceView
  userId: number
  finance?: boolean
}) {
  const { t } = useTranslation()
  const actor = useAuthStore((state) => state.auth.user)
  const policy = useQuery({
    queryKey: ['agent-platform', actor?.id, 'policy'],
    queryFn: ({ signal }) =>
      getPlatform<AgentPolicy>('/policy/current', undefined, signal),
  })
  const current = props.data.governance
  const submission = props.data.submission
  const referenceField = (
    name: string,
    label: string,
    prefix: string
  ): ActionField => ({
    name,
    label,
    max: 128,
    placeholder: `${prefix}:case-0001`,
    description:
      'Only opaque dossier references are accepted. Never upload or paste identity documents, identity numbers, bank details or credentials.',
    validate: (value) =>
      validGovernanceReference(String(value), prefix)
        ? undefined
        : t(
            'Enter an opaque reference with the required prefix and at least eight identifier characters.'
          ),
  })
  const submit: WorkflowAction = {
    label: 'Submit qualification dossier',
    action: 'governance.submit',
    endpoint: '/governance/submit',
    payload: { expected_version: current?.version ?? 0 },
    fields: [
      referenceField(
        'identity_dossier_reference',
        'Identity dossier reference',
        'dossier'
      ),
      referenceField(
        'agreement_reference',
        'Accepted agreement reference',
        'agreement'
      ),
      {
        name: 'agreement_digest',
        label: 'Accepted agreement SHA-256 digest',
        max: 64,
        validate: (value) =>
          /^[a-f0-9]{64}$/.test(String(value))
            ? undefined
            : t('Enter a lowercase 64-character SHA-256 digest.'),
      },
      {
        name: 'accept_terms',
        label:
          'I accept the referenced agreement and retain my existing customer obligations',
        kind: 'checkbox',
        mustBeTrue: true,
      },
    ],
  }
  const reviewFields: ActionField[] = [
    {
      name: 'decision',
      label: 'Review decision',
      kind: 'select',
      options: [
        { value: 'approved', label: 'Approve' },
        { value: 'rejected', label: 'Reject' },
      ],
    },
    {
      name: 'identity_verified',
      label: 'Identity evidence verified',
      kind: 'checkbox',
    },
    {
      name: 'agreement_verified',
      label: 'Agreement evidence verified',
      kind: 'checkbox',
    },
    {
      name: 'review_due_at',
      label: 'Next review due',
      kind: 'datetime',
      required: false,
    },
    {
      name: 'risk_enabled',
      label: 'Daily risk limits enabled',
      kind: 'checkbox',
    },
    {
      name: 'daily_issuance_quota',
      label: 'Daily gross issuance cap',
      kind: 'integer',
    },
    {
      name: 'daily_transfer_quota',
      label: 'Daily gross transfer cap',
      kind: 'integer',
    },
    { name: 'reason', label: 'Reason', kind: 'textarea' },
  ]
  const reviewPayload = {
    user_id: props.userId,
    expected_version: current?.version ?? 0,
    expected_profile_version: props.data.profile?.version ?? 0,
  }
  const review: WorkflowAction = {
    label: 'Review qualification dossier',
    action: 'governance.review',
    endpoint: `/finance/governance/${props.userId}/review`,
    fields: reviewFields,
    payload: reviewPayload,
    transform: (_values, payload) => {
      if (payload.decision === 'rejected') payload.review_due_at = 0
      else if (
        typeof payload.review_due_at !== 'number' ||
        payload.review_due_at <= Date.now() / 1000 ||
        payload.review_due_at > Date.now() / 1000 + 10 * 366 * 86400
      ) {
        throw new Error(
          t(
            'Approval and exceptions require a future review date within ten years.'
          )
        )
      }

      if (
        payload.decision === 'approved' &&
        (payload.identity_verified !== true ||
          payload.agreement_verified !== true)
      ) {
        throw new Error(
          t(
            'Approval requires independently verified identity and agreement evidence.'
          )
        )
      }
      if (
        (payload.risk_enabled === true &&
          (!payload.daily_issuance_quota || !payload.daily_transfer_quota)) ||
        (payload.risk_enabled !== true &&
          (payload.daily_issuance_quota !== 0 ||
            payload.daily_transfer_quota !== 0))
      ) {
        throw new Error(
          t(
            'Enabled risk limits require positive caps. Disabled limits require explicit zero caps.'
          )
        )
      }
      payload.risk_limits = {
        enabled: payload.risk_enabled,
        daily_issuance_quota: payload.daily_issuance_quota,
        daily_transfer_quota: payload.daily_transfer_quota,
      }
      delete payload.risk_enabled
      delete payload.daily_issuance_quota
      delete payload.daily_transfer_quota
      return payload
    },
  }
  const override: WorkflowAction = {
    ...review,
    label: 'Record documented qualification exception',
    action: 'governance.override',
    endpoint: `/finance/governance/${props.userId}/override`,
    fields: [
      referenceField(
        'exception_reference',
        'Approved exception reference',
        'exception'
      ),
      ...reviewFields.filter(
        (field) =>
          !['decision', 'identity_verified', 'agreement_verified'].includes(
            field.name
          )
      ),
    ],
    description:
      'An exception is visibly separate from verified qualification. It does not mark identity or agreement evidence verified and does not activate agent eligibility.',
  }
  const actions: WorkflowAction[] = []
  if (!props.finance && policy.data?.governance_enabled) actions.push(submit)
  if (props.finance && current && props.userId !== actor?.id) {
    actions.push(review)
    if (actor?.role === ROLE.SUPER_ADMIN) actions.push(override)
  }
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Qualification review and agent eligibility are separate. Approval never promotes your account, changes invitation attribution, unlocks old disabled API keys or removes existing customer delivery obligations.'
          )}
        </AlertDescription>
      </Alert>
      {props.data.review_required && policy.data?.governance_enabled && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'Qualification review is required before restricted new business. Existing customer rights and ordinary account services remain available.'
            )}{' '}
            {props.data.review_reasons.map((reason) => (
              <span className='block' key={reason}>
                <BusinessState value={reason} />
              </span>
            ))}
          </AlertDescription>
        </Alert>
      )}
      {current ? (
        <dl className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
          {governanceColumns.map((column) => (
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
      ) : (
        <p>{t('No qualification dossier has been submitted.')}</p>
      )}
      {submission && (
        <dl className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
          {[
            {
              key: 'identity_dossier_digest',
              label: 'Identity dossier digest',
            },
            {
              key: 'agreement_reference_digest',
              label: 'Agreement reference digest',
            },
            {
              key: 'agreement_digest',
              label: 'Accepted agreement SHA-256 digest',
            },
            {
              key: 'accept_terms',
              label: 'Agreement accepted',
              kind: 'boolean' as const,
            },
            ...(props.finance
              ? [
                  {
                    key: 'identity_dossier_reference',
                    label: 'Identity dossier reference',
                  },
                  {
                    key: 'agreement_reference',
                    label: 'Accepted agreement reference',
                  },
                ]
              : []),
          ].map((column) => (
            <div key={column.key}>
              <dt className='text-muted-foreground text-sm'>
                {t(column.label)}
              </dt>
              <dd className='break-all'>
                <WorkflowValue row={submission} column={column} />
              </dd>
            </div>
          ))}
        </dl>
      )}
      {props.data.daily_risk && (
        <section className='space-y-3'>
          <h3 className='font-medium'>{t('Gross risk usage today (UTC)')}</h3>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Cancellations and refunds do not restore gross daily limits. Uncapped is different from a zero remaining limit.'
            )}
          </p>
          <dl className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
            {[
              ['gross_issuance_quota', 'Gross quota issued today'],
              ['gross_transfer_quota', 'Gross quota transferred today'],
              ['remaining_issuance_quota', 'Remaining issuance allowance'],
              ['remaining_transfer_quota', 'Remaining transfer allowance'],
            ].map(([key, label]) => (
              <div key={key}>
                <dt className='text-muted-foreground text-sm'>{t(label)}</dt>
                <dd>
                  {props.data.daily_risk?.[key] === null ? (
                    t('Uncapped')
                  ) : (
                    <ExactQuota
                      value={String(props.data.daily_risk?.[key] ?? '')}
                    />
                  )}
                </dd>
              </div>
            ))}
          </dl>
        </section>
      )}
      <WorkflowTable
        resource='/governance'
        rows={props.data.audit}
        create={actions}
        columns={[
          { key: 'version', label: 'Qualification version' },
          { key: 'action', label: 'Operation' },
          { key: 'status', label: 'Qualification status', kind: 'state' },
          { key: 'actor_user_id', label: 'Operator ID' },
          {
            key: 'exceptional',
            label: 'Documented exception',
            kind: 'boolean',
          },
          { key: 'reason', label: 'Reason' },
          { key: 'created_at', label: 'Created', kind: 'date' },
        ]}
      />
    </div>
  )
}
