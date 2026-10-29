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

import { CopyButton } from '@/components/copy-button'
import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getAgentInvitationCode, getAgentInvitations } from '../api'
import {
  invitationBonusLabel,
  invitationSourceLabel,
} from '../lib/invitation-labels'
import type { AgentInvitation } from '../types'

export function InvitationsTable(props: { agentId?: number }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const isAgent = user?.role === ROLE.AGENT
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const query = useQuery({
    queryKey: [
      'agent-redemption',
      user?.id,
      user?.role,
      'invitations',
      props.agentId,
      pagination,
    ],
    queryFn: ({ signal }) =>
      getAgentInvitations(
        {
          p: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
          agent_user_id: props.agentId,
        },
        signal
      ),
  })
  const codeQuery = useQuery({
    queryKey: ['agent-redemption', user?.id, user?.role, 'invitation-code'],
    queryFn: ({ signal }) => getAgentInvitationCode(signal),
    enabled: isAgent,
  })
  const invitationLink = codeQuery.data
    ? `${window.location.origin}/sign-up?aff=${encodeURIComponent(codeQuery.data)}`
    : ''
  const columns: ColumnDef<AgentInvitation>[] = [
    {
      accessorKey: 'user_id',
      header: t('Invited user ID'),
      meta: { mobileTitle: true },
    },
    ...(!isAgent
      ? [
          { accessorKey: 'inviter_id', header: t('Agent ID') },
          { accessorKey: 'inviter_username', header: t('Inviter') },
        ]
      : []),
    {
      accessorKey: 'bonus_quota',
      header: t('Test quota granted'),
      cell: ({ row }) =>
        formatQuotaWithCurrency(row.original.bonus_quota, {
          abbreviate: false,
        }),
    },
    {
      accessorKey: 'source',
      header: t('Source'),
      cell: ({ row }) => invitationSourceLabel(row.original.source, t),
    },
    {
      accessorKey: 'bonus_reason',
      header: t('Test grant status'),
      cell: ({ row }) => invitationBonusLabel(row.original.bonus_reason, t),
    },
    {
      accessorKey: 'created_at',
      header: t('Created'),
      cell: ({ row }) => formatTimestampToDate(row.original.created_at),
    },
  ]
  const { table } = useDataTable({
    data: query.data?.items ?? [],
    columns,
    pagination,
    onPaginationChange: setPagination,
    manualPagination: true,
    totalCount: query.data?.total ?? 0,
    enableSorting: false,
  })
  return (
    <div className='space-y-4'>
      {isAgent && (
        <div className='space-y-2'>
          <Label htmlFor='agent-invitation-link'>
            {t('Your invitation link')}
          </Label>
          <div className='flex gap-2'>
            <Input
              id='agent-invitation-link'
              readOnly
              value={invitationLink}
              placeholder={t('Loading...')}
            />
            {invitationLink && (
              <CopyButton
                value={invitationLink}
                aria-label={t('Copy invitation link')}
                variant='outline'
              />
            )}
          </div>
          {codeQuery.isError && (
            <ErrorState
              onRetry={() => {
                void codeQuery.refetch()
              }}
            />
          )}
          <p className='text-muted-foreground text-sm'>
            {t(
              'Invitations are single-level. Test quota is granted once at registration according to the current policy.'
            )}
          </p>
        </div>
      )}
      {query.isError ? (
        <ErrorState
          onRetry={() => {
            void query.refetch()
          }}
        />
      ) : (
        <DataTablePage
          table={table}
          columns={columns}
          isLoading={query.isLoading}
          isFetching={query.isFetching}
          emptyTitle={t('No invitations yet')}
          toolbar={null}
          paginationInFooter={false}
        />
      )}
    </div>
  )
}
