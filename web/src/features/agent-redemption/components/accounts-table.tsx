import type { ColumnDef, PaginationState, OnChangeFn } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
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
    { accessorKey: 'agent_user_id', header: t('Agent ID'), meta: { mobileTitle: true } },
    { accessorKey: 'username', header: t('Username'), cell: ({ row }) => <span className='break-all'>{row.original.username || '—'}</span> },
    { accessorKey: 'available_quota', header: t('Available quota'), cell: ({ row }) => formatQuotaWithCurrency(row.original.available_quota, { abbreviate: false }) },
    { accessorKey: 'reserved_quota', header: t('Reserved quota'), cell: ({ row }) => formatQuotaWithCurrency(row.original.reserved_quota, { abbreviate: false }) },
    { accessorKey: 'spent_quota', header: t('Redeemed quota'), cell: ({ row }) => formatQuotaWithCurrency(row.original.spent_quota, { abbreviate: false }) },
    { accessorKey: 'allocated_quota', header: t('Allocated quota'), cell: ({ row }) => formatQuotaWithCurrency(row.original.allocated_quota, { abbreviate: false }) },
    { id: 'actions', header: t('Actions'), cell: ({ row }) => <div className='flex flex-wrap gap-2'>
      <Button variant='outline' size='sm' onClick={() => props.onSelect(row.original)}>{t('View ledger')}</Button>
      <Button variant='outline' size='sm' onClick={() => props.onAdjust(row.original)}>{t('Adjust quota')}</Button>
    </div> },
  ]
  const { table } = useDataTable({ data: props.data?.items ?? [], columns, pagination: props.pagination, onPaginationChange: props.onPaginationChange, manualPagination: true, totalCount: props.data?.total ?? 0, enableSorting: false })
  return <DataTablePage table={table} columns={columns} isLoading={props.isLoading} isFetching={props.isFetching} emptyTitle={t('No agent accounts found')} toolbar={null} paginationInFooter={false} />
}
