import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { formatQuotaWithCurrency } from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { getAgentLedger } from '../api'
import type { AgentLedgerEntry } from '../types'

export function LedgerTable(props: { agentId: number }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const query = useQuery({
    queryKey: ['agent-redemption', user?.id, user?.role, 'ledger', props.agentId, pagination],
    queryFn: ({ signal }) => getAgentLedger(props.agentId, { p: pagination.pageIndex + 1, page_size: pagination.pageSize }, signal),
  })
  const kindLabels: Record<string, string> = { allocation: t('Quota adjustment'), reserve: t('Reservation'), edit: t('Code edited'), enable: t('Code enabled'), disable: t('Code disabled'), delete: t('Code deleted'), expire: t('Code expired'), redeem: t('Code redeemed') }
  const columns: ColumnDef<AgentLedgerEntry>[] = [
    { accessorKey: 'id', header: t('ID'), meta: { mobileHidden: true } },
    { accessorKey: 'kind', header: t('Type'), meta: { mobileTitle: true }, cell: ({ row }) => kindLabels[row.original.kind] ?? row.original.kind },
    { accessorKey: 'delta', header: t('Available change'), cell: ({ row }) => formatQuotaWithCurrency(row.original.delta, { abbreviate: false }) },
    { accessorKey: 'available_quota_after', header: t('Available quota'), cell: ({ row }) => formatQuotaWithCurrency(row.original.available_quota_after, { abbreviate: false }) },
    { accessorKey: 'reserved_quota_after', header: t('Reserved quota'), cell: ({ row }) => formatQuotaWithCurrency(row.original.reserved_quota_after, { abbreviate: false }) },
    { accessorKey: 'spent_quota_after', header: t('Redeemed quota'), cell: ({ row }) => formatQuotaWithCurrency(row.original.spent_quota_after, { abbreviate: false }) },
    { accessorKey: 'redemption_id', header: t('Redemption code ID'), cell: ({ row }) => row.original.redemption_id || '—' },
    { accessorKey: 'request_id', header: t('Request ID'), cell: ({ row }) => <span className='break-all font-mono text-xs'>{row.original.request_id || '—'}</span> },
    { accessorKey: 'actor_user_id', header: t('Operator ID') },
    { accessorKey: 'reason', header: t('Reason'), cell: ({ row }) => <span className='break-words'>{row.original.reason || '—'}</span> },
    { accessorKey: 'created_at', header: t('Created'), cell: ({ row }) => formatTimestampToDate(row.original.created_at) },
  ]
  const { table } = useDataTable({ data: query.data?.items ?? [], columns, pagination, onPaginationChange: setPagination, manualPagination: true, totalCount: query.data?.total ?? 0, enableSorting: false })
  if (query.isError) return <ErrorState onRetry={() => { void query.refetch() }} />
  return <DataTablePage table={table} columns={columns} isLoading={query.isLoading} isFetching={query.isFetching} emptyTitle={t('No quota ledger entries')} toolbar={null} paginationInFooter={false} />
}
