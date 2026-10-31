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
import type {
  ColumnDef,
  PaginationState,
  OnChangeFn,
} from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { formatQuotaWithCurrency } from '@/lib/currency'

import type { AgentAccount, AgentPage } from '../types'

export function AccountsTable(props: {
  data?: AgentPage<AgentAccount>
  isLoading: boolean
  isFetching: boolean
  pagination: PaginationState
  onPaginationChange: OnChangeFn<PaginationState>
  onSelect: (account: AgentAccount) => void
  onAdjust: (account: AgentAccount) => void
}) {
  const { t } = useTranslation()
  const columns: ColumnDef<AgentAccount>[] = [
    {
      accessorKey: 'agent_user_id',
      header: t('Agent ID'),
      meta: { mobileTitle: true },
    },
    {
      accessorKey: 'username',
      header: t('Username'),
      cell: ({ row }) => (
        <span className='break-all'>{row.original.username || '—'}</span>
      ),
    },
    {
      id: 'status',
      header: t('Status'),
      meta: { mobileBadge: true },
      cell: ({ row }) => {
        if (row.original.user_deleted) {
          return (
            <StatusBadge
              label={t('Deleted')}
              variant='danger'
              copyable={false}
            />
          )
        }
        if (row.original.active_agent !== true) {
          return (
            <StatusBadge
              label={t('Sales unavailable')}
              variant='neutral'
              copyable={false}
            />
          )
        }
        if (
          row.original.user_status !== undefined &&
          row.original.user_status !== 1
        ) {
          return (
            <StatusBadge
              label={t('Disabled')}
              variant='neutral'
              copyable={false}
            />
          )
        }
        return (
          <StatusBadge
            label={t('Enabled')}
            variant='success'
            copyable={false}
          />
        )
      },
    },
    {
      accessorKey: 'available_quota',
      header: t('Available quota'),
      cell: ({ row }) =>
        formatQuotaWithCurrency(row.original.available_quota, {
          abbreviate: false,
        }),
    },
    {
      accessorKey: 'reserved_quota',
      header: t('Reserved quota'),
      cell: ({ row }) =>
        formatQuotaWithCurrency(row.original.reserved_quota, {
          abbreviate: false,
        }),
    },
    {
      accessorKey: 'spent_quota',
      header: t('Redeemed quota'),
      cell: ({ row }) =>
        formatQuotaWithCurrency(row.original.spent_quota, {
          abbreviate: false,
        }),
    },
    {
      accessorKey: 'allocated_quota',
      header: t('Allocated quota'),
      cell: ({ row }) =>
        formatQuotaWithCurrency(row.original.allocated_quota, {
          abbreviate: false,
        }),
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <div className='flex flex-wrap gap-2'>
          <Button
            variant='outline'
            size='sm'
            onClick={() => props.onSelect(row.original)}
          >
            {t('View ledger')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            onClick={() => props.onAdjust(row.original)}
          >
            {t('Adjust quota')}
          </Button>
        </div>
      ),
    },
  ]
  const { table } = useDataTable({
    data: props.data?.items ?? [],
    columns,
    pagination: props.pagination,
    onPaginationChange: props.onPaginationChange,
    manualPagination: true,
    totalCount: props.data?.total ?? 0,
    enableSorting: false,
  })
  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={props.isLoading}
      isFetching={props.isFetching}
      emptyTitle={t('No agent accounts found')}
      toolbar={null}
      paginationInFooter={false}
    />
  )
}
