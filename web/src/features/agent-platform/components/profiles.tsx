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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useAuthStore, type AgentProfile } from '@/stores/auth-store'

import { getPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import type { OperationDraft } from '../types'
import { ActionForm, type ActionField } from './action-form'
import { OperationDialog } from './operation-dialog'
import { ResourceTable } from './resource-table'
import { BusinessState } from './values'

export function ProfileEditor(props: {
  userId: number
  onClose: () => void
  onComplete?: () => void
}) {
  const { t } = useTranslation()
  const actorId = useAuthStore((state) => state.auth.user?.id)
  const requestId = useRequestId()
  const [draft, setDraft] = useState<OperationDraft | null>(null)
  const query = useQuery({
    queryKey: ['agent-platform', actorId, 'profile', props.userId],
    queryFn: ({ signal }) =>
      getPlatform<{
        user_id: number
        username: string
        profile: AgentProfile | null
      }>(`/profiles/${props.userId}`, undefined, signal),
  })
  if (draft) {
    return (
      <OperationDialog
        key={draft.request_id}
        draft={draft}
        onClose={() => setDraft(null)}
        onComplete={() => {
          props.onComplete?.()
          props.onClose()
        }}
      />
    )
  }
  if (query.isPending || query.isError || !query.data) {
    return (
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
        title={t('Agent eligibility')}
      >
        {query.isPending ? (
          <LoadingState />
        ) : (
          <ErrorState
            onRetry={() => {
              void query.refetch()
            }}
          />
        )}
      </Dialog>
    )
  }
  const profile = query.data.profile
  const fields: ActionField[] = [
    {
      name: 'status',
      label: 'Business eligibility',
      kind: 'select',
      options: [
        { value: 'active', label: 'Active' },
        { value: 'exited', label: 'Exited' },
      ],
    },
    { name: 'sales_frozen', label: 'Freeze new sales', kind: 'checkbox' },
    {
      name: 'settlement_frozen',
      label: 'Freeze value transfers and settlement',
      kind: 'checkbox',
    },
    { name: 'reason', label: 'Reason', kind: 'textarea' },
  ]
  return (
    <ActionForm
      title={t('Agent eligibility')}
      description={t(
        'This changes business eligibility only. Wallet, API keys, model access and account status remain separate. Existing disabled keys are never reactivated.'
      )}
      fields={fields}
      initial={{
        status: profile?.status ?? 'active',
        sales_frozen: profile?.sales_frozen ?? false,
        settlement_frozen: profile?.settlement_frozen ?? false,
        reason: '',
      }}
      onClose={props.onClose}
      onSubmit={(values) => {
        const payload = {
          user_id: props.userId,
          expected_version: profile?.version ?? 0,
          status: String(values.status),
          sales_frozen: values.sales_frozen === true,
          settlement_frozen: values.settlement_frozen === true,
          reason: String(values.reason).trim(),
        }
        setDraft({
          action: 'profile.update',
          request_id: requestId(payload),
          payload,
          endpoint: `/profiles/${props.userId}`,
          method: 'patch',
          resource: '/profiles',
          review: [
            { label: 'User', after: `${query.data.username} #${props.userId}` },
            ...fields.map((field) => ({
              label: field.label,
              after: String(values[field.name]),
            })),
          ],
        })
      }}
    >
      <p className='mb-4 text-sm'>
        {query.data.username} #{props.userId} · {t('Permission version')}{' '}
        {profile?.version ?? 0}
      </p>
    </ActionForm>
  )
}

export function Profiles(props: { initialUserId?: number }) {
  const { t } = useTranslation()
  const [lookup, setLookup] = useState(false)
  const [selected, setSelected] = useState<number | null>(
    props.initialUserId ?? null
  )
  const columns: ColumnDef<AgentProfile>[] = [
    {
      accessorKey: 'user_id',
      header: t('User ID'),
      meta: { mobileTitle: true },
    },
    {
      accessorKey: 'status',
      header: t('Business eligibility'),
      meta: { mobileBadge: true },
      cell: ({ row }) => <BusinessState value={row.original.status} />,
    },
    {
      accessorKey: 'sales_frozen',
      header: t('Sales freeze'),
      cell: ({ row }) =>
        row.original.sales_frozen ? t('Frozen') : t('Not frozen'),
    },
    {
      accessorKey: 'settlement_frozen',
      header: t('Settlement freeze'),
      cell: ({ row }) =>
        row.original.settlement_frozen ? t('Frozen') : t('Not frozen'),
    },
    { accessorKey: 'version', header: t('Permission version') },
    { accessorKey: 'reason', header: t('Reason') },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <Button
          size='sm'
          variant='outline'
          onClick={() => setSelected(row.original.user_id)}
        >
          {t('Manage eligibility')}
        </Button>
      ),
    },
  ]
  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <AlertDescription>
          {t(
            'Sales freeze, settlement freeze and user-account suspension are different controls. Exited agents retain historical records and customer delivery obligations.'
          )}
        </AlertDescription>
      </Alert>
      <Button className='self-start' onClick={() => setLookup(true)}>
        {t('Find user to manage eligibility')}
      </Button>
      <ResourceTable resource='/profiles' columns={columns} />
      {lookup && (
        <ActionForm
          title={t('Find user')}
          fields={[
            { name: 'user_id', label: 'User ID', kind: 'integer', min: 1 },
          ]}
          onClose={() => setLookup(false)}
          onSubmit={(values) => {
            setSelected(Number(values.user_id))
            setLookup(false)
          }}
        />
      )}
      {selected !== null && (
        <ProfileEditor
          key={selected}
          userId={selected}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  )
}
