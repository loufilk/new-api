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
import type { ColumnDef } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'

import { usePlatformList } from '../hooks/use-platform-list'

export function ResourceTable<T>(props: {
  resource: string
  columns: ColumnDef<T>[]
  filters?: Record<string, string | number | undefined>
  enabled?: boolean
  toolbar?: React.ReactNode
}) {
  const { t } = useTranslation()
  const { query, pagination, setPagination } = usePlatformList<T>(
    props.resource,
    props.filters,
    props.enabled
  )
  const { table } = useDataTable({
    data: query.data?.items ?? [],
    columns: props.columns,
    pagination,
    onPaginationChange: setPagination,
    manualPagination: true,
    totalCount: query.data?.total ?? 0,
    enableSorting: false,
  })
  if (query.isError) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  return (
    <DataTablePage
      table={table}
      columns={props.columns}
      isLoading={query.isPending}
      isFetching={query.isFetching}
      emptyTitle={t('No records found')}
      toolbar={props.toolbar ?? null}
      paginationInFooter={false}
    />
  )
}
