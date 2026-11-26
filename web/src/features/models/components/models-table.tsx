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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { useModelPricing } from '@/features/model-pricing/api'
import { useMediaQuery } from '@/hooks'
import { useTableUrlState } from '@/hooks/use-table-url-state'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import {
  getServerMessage,
  requireServerSuccess,
} from '@/lib/server-error-message'

import { getModels, searchModels, getVendors } from '../api'
import { DEFAULT_PAGE_SIZE } from '../constants'
import { modelsQueryKeys, vendorsQueryKeys } from '../lib'
import type { Model, ModelSquareState } from '../types'
import { DataTableBulkActions } from './data-table-bulk-actions'
import {
  ModelDeleteDialog,
  type ModelDeleteTarget,
} from './dialogs/model-delete-dialog'
import { useModelsColumns } from './models-columns'
import { useModels } from './models-provider'

const route = getRouteApi('/_authenticated/model-management/$section')

export function ModelsTable() {
  const { t } = useTranslation()
  const { selectedVendor } = useModels()
  const client = useQueryClient()
  const [deletion, setDeletion] = useState<{
    targets: ModelDeleteTarget[]
    epoch: number
    bulk: boolean
  } | null>(null)
  const requestDeletion = useCallback((targets: Model[], bulk = false) => {
    if (!targets.length || targets.some(({ id }) => id <= 0)) return
    setDeletion({
      targets: targets.map(({ id, model_name, name_rule }) => ({
        id,
        model_name,
        name_rule,
      })),
      epoch: getAuthSessionEpoch(),
      bulk,
    })
  }, [])
  const isMobile = useMediaQuery('(max-width: 640px)')

  // URL state management
  const {
    globalFilter,
    onGlobalFilterChange,
    columnFilters,
    onColumnFiltersChange,
    pagination,
    onPaginationChange,
    ensurePageInRange,
  } = useTableUrlState({
    search: route.useSearch(),
    navigate: route.useNavigate(),
    pagination: {
      defaultPage: 1,
      defaultPageSize: isMobile ? 10 : DEFAULT_PAGE_SIZE,
    },
    globalFilter: { enabled: true, key: 'filter' },
    columnFilters: [
      { columnId: 'status', searchKey: 'status', type: 'array' },
      { columnId: 'square_state', searchKey: 'square_state', type: 'array' },
      { columnId: 'vendor_id', searchKey: 'vendor', type: 'array' },
      { columnId: 'sync_official', searchKey: 'sync', type: 'array' },
    ],
  })

  // Extract filters from column filters
  const statusFilter =
    (columnFilters.find((f) => f.id === 'status')?.value as string[]) || []
  const squareState = (
    columnFilters.find((f) => f.id === 'square_state')?.value as
      | ModelSquareState[]
      | undefined
  )?.[0]
  const vendorFilter =
    (columnFilters.find((f) => f.id === 'vendor_id')?.value as string[]) || []
  const syncFilter =
    (columnFilters.find((f) => f.id === 'sync_official')?.value as string[]) ||
    []

  // Fetch vendors for filter
  const { data: vendorsData } = useQuery({
    queryKey: vendorsQueryKeys.list(),
    queryFn: async () =>
      requireServerSuccess(await getVendors({ page_size: 1000 })),
  })

  const vendors = useMemo(
    () => vendorsData?.data?.items || [],
    [vendorsData?.data?.items]
  )

  const vendorOptions = useMemo(() => {
    return vendors.map((v) => ({
      label: v.name,
      value: String(v.id),
    }))
  }, [vendors])

  // Apply selected vendor from context or filter
  const activeVendorFilter =
    selectedVendor ||
    (vendorFilter.length > 0 && !vendorFilter.includes('all')
      ? vendorFilter[0]
      : undefined)

  const statusFilterValue =
    statusFilter.length > 0 && !statusFilter.includes('all')
      ? statusFilter[0]
      : undefined
  const syncFilterValue =
    syncFilter.length > 0 && !syncFilter.includes('all')
      ? syncFilter[0]
      : undefined

  // Use search API whenever any filter is active so status/sync are applied server-side
  const shouldSearch = Boolean(
    globalFilter?.trim() ||
    activeVendorFilter ||
    statusFilterValue ||
    squareState ||
    syncFilterValue
  )

  const listKey = modelsQueryKeys.list({
    include_channel_models: true,
    keyword: globalFilter,
    vendor: activeVendorFilter,
    status: statusFilterValue,
    square_state: squareState,
    sync_official: syncFilterValue,
    p: pagination.pageIndex + 1,
    page_size: pagination.pageSize,
  })
  // Fetch models data
  // eslint-disable-next-line @tanstack/query/exhaustive-deps
  const { data, isLoading, isFetching, isError, error, refetch } = useQuery({
    queryKey: listKey,
    queryFn: async () => {
      if (shouldSearch) {
        return requireServerSuccess(
          await searchModels({
            include_channel_models: true,
            keyword: globalFilter,
            vendor: activeVendorFilter,
            status: statusFilterValue,
            square_state: squareState,
            sync_official: syncFilterValue,
            p: pagination.pageIndex + 1,
            page_size: pagination.pageSize,
          })
        )
      }
      return requireServerSuccess(
        await getModels({
          include_channel_models: true,
          p: pagination.pageIndex + 1,
          page_size: pagination.pageSize,
        })
      )
    },
  })

  const models = data?.data?.items || []
  const totalCount = data?.data?.total || 0
  const vendorCounts = data?.data?.vendor_counts

  // Columns configuration
  const pricingQuery = useModelPricing(
    models
      .filter((item) => item.name_rule === 0)
      .map((item) => item.model_name),
    models.length > 0
  )
  let pricingState: 'loading' | 'error' | undefined
  if (pricingQuery.isError) pricingState = 'error'
  else if (pricingQuery.isLoading) pricingState = 'loading'
  const columns = useModelsColumns(
    requestDeletion,
    vendors,
    pricingQuery.data,
    pricingState
  )

  // React Table instance
  const { table } = useDataTable({
    data: models,
    getRowId: (model) =>
      model.id > 0 ? `metadata:${model.id}` : `channel:${model.model_name}`,
    columns,
    totalCount,
    initialColumnVisibility: {
      description: false,
      id: false,
      vendor_id: false,
      name_rule: false,
      endpoints: false,
      created_time: false,
      updated_time: false,
      status: false,
    },
    columnFilters,
    pagination,
    globalFilter,
    enableRowSelection: true,
    onColumnFiltersChange,
    onPaginationChange,
    onGlobalFilterChange,
    manualPagination: true,
    manualFiltering: true,
    ensurePageInRange,
  })

  // Prepare filter options
  const vendorFilterOptions = [
    {
      label: `${t('All Vendors')}${vendorCounts?.all ? ` (${vendorCounts.all})` : ''}`,
      value: 'all',
    },
    ...vendorOptions.map((option) => ({
      label: `${option.label}${vendorCounts?.[option.value] ? ` (${vendorCounts[option.value]})` : ''}`,
      value: option.value,
    })),
  ]

  // Columns and mobile/desktop cells can remount while pricing or the catalogue
  // refreshes. Keep the frozen deletion at table level, never in a transient row.
  const deletionIsCurrent = () => {
    if (!deletion || deletion.epoch !== getAuthSessionEpoch()) return false
    const latest = client.getQueryState<typeof data>(listKey)
    if (latest?.status !== 'success' || latest.fetchStatus !== 'idle') {
      return false
    }
    return deletion.targets.every((target) =>
      latest.data?.data?.items?.some(
        (item) =>
          item.id === target.id &&
          item.model_name === target.model_name &&
          item.name_rule === target.name_rule
      )
    )
  }
  const deleteDialog = deletion && (
    <ModelDeleteDialog
      models={deletion.targets}
      isCurrent={deletionIsCurrent}
      onClose={() => setDeletion(null)}
      onSuccess={deletion.bulk ? () => table.resetRowSelection() : undefined}
    />
  )
  const catalogue = (
    <DataTablePage
      showMobileBulkActions
      mobileProps={{ enableRowSelection: true }}
      table={table}
      columns={columns}
      isLoading={isLoading}
      isFetching={isFetching}
      emptyTitle={t('No Models Found')}
      emptyDescription={
        shouldSearch
          ? t('Try adjusting your search')
          : t('No models available. Create your first model to get started.')
      }
      skeletonKeyPrefix='model-skeleton'
      applyHeaderSize
      pinnedColumns={[
        { columnId: 'model_name', side: 'left' },
        { columnId: 'actions', side: 'right' },
      ]}
      toolbarProps={{
        searchPlaceholder: t('Filter by model name...'),
        searchDebounceMs: 500,
        filters: [
          {
            columnId: 'status',
            title: t('Display policy'),
            options: [
              { label: t('Allowed'), value: 'enabled' },
              { label: t('Not listed'), value: 'disabled' },
            ],
            singleSelect: true,
          },
          {
            columnId: 'square_state',
            title: t('Model square visibility'),
            options: [
              { label: t('Displayed'), value: 'visible' },
              { label: t('Unavailable'), value: 'unavailable' },
              { label: t('Listing hidden'), value: 'hidden' },
              { label: t('Partly shown'), value: 'partial' },
            ],
            singleSelect: true,
          },
          {
            columnId: 'vendor_id',
            title: t('Vendor'),
            options: vendorFilterOptions,
            singleSelect: true,
          },
          {
            columnId: 'sync_official',
            title: t('Sync policy'),
            options: [
              { label: t('Allow updates'), value: 'yes' },
              { label: t('Keep local'), value: 'no' },
            ],
            singleSelect: true,
          },
        ],
      }}
      bulkActions={
        <DataTableBulkActions table={table} onDelete={requestDeletion} />
      }
    />
  )
  return (
    <>
      {deleteDialog}
      {isError || data?.success === false ? (
        <ErrorState
          description={error?.message ?? getServerMessage(data)}
          onRetry={() => void refetch()}
        />
      ) : (
        catalogue
      )}
    </>
  )
}
