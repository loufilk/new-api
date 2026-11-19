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
import {
  type Cell as CoreCell,
  type Column as CoreColumn,
  type ColumnDef as CoreColumnDef,
  type Header as CoreHeader,
  type ReactTable,
  type Row as CoreRow,
  type RowData,
  type TableOptions as CoreOptions,
  type TableState,
  columnFacetingFeature,
  columnFilteringFeature,
  columnResizingFeature,
  columnSizingFeature,
  columnVisibilityFeature,
  createExpandedRowModel,
  createFacetedRowModel,
  createFacetedUniqueValues,
  createFilteredRowModel,
  createPaginatedRowModel,
  createSortedRowModel,
  filterFns,
  globalFilteringFeature,
  metaHelper,
  rowExpandingFeature,
  rowPaginationFeature,
  rowSelectionFeature,
  rowSortingFeature,
  sortFns,
  tableFeatures,
  useTable,
} from '@tanstack/react-table'

import type { ConsoleColumnMeta } from '@/tanstack-table'

export { flexRender } from '@tanstack/react-table/flex-render'
export type {
  ColumnFiltersState,
  ColumnSizingState,
  ColumnVisibilityState as VisibilityState,
  ExpandedState,
  OnChangeFn,
  PaginationState,
  RowSelectionState,
  SortingState,
  Updater,
} from '@tanstack/react-table'

// Row models are configured per table, not globally. In particular server pages
// must not filter, sort or paginate their already-processed current page again.
export function consoleTableFeatures(
  pipeline: {
    filter?: boolean
    sort?: boolean
    paginate?: boolean
    expand?: boolean
    facet?: boolean
  } = {}
) {
  return tableFeatures({
    columnVisibilityFeature,
    columnSizingFeature,
    columnResizingFeature,
    columnFilteringFeature,
    globalFilteringFeature,
    columnFacetingFeature,
    rowSortingFeature,
    rowExpandingFeature,
    rowPaginationFeature,
    rowSelectionFeature,
    filterFns,
    sortFns,
    columnMeta: metaHelper<ConsoleColumnMeta>(),
    ...(pipeline.filter && { filteredRowModel: createFilteredRowModel() }),
    ...(pipeline.sort && { sortedRowModel: createSortedRowModel() }),
    ...(pipeline.paginate && { paginatedRowModel: createPaginatedRowModel() }),
    ...(pipeline.expand && { expandedRowModel: createExpandedRowModel() }),
    ...(pipeline.facet && {
      facetedRowModel: createFacetedRowModel(),
      facetedUniqueValues: createFacetedUniqueValues(),
    }),
  })
}

type Features = ReturnType<typeof consoleTableFeatures>
export type Cell<T extends RowData, V = unknown> = CoreCell<Features, T, V>
export type Row<T extends RowData> = CoreRow<Features, T>
export type Column<T extends RowData, V = unknown> = CoreColumn<Features, T, V>
export type Header<T extends RowData, V = unknown> = CoreHeader<Features, T, V>
export type ColumnDef<T extends RowData, V = unknown> = CoreColumnDef<
  Features,
  T,
  V
>
export type Table<T extends RowData> = ReactTable<
  Features,
  T,
  TableState<Features>
>
export type TableOptions<T extends RowData> = CoreOptions<Features, T>

const baseFeatures = consoleTableFeatures()

export function useConsoleTable<T extends RowData>(
  options: Omit<TableOptions<T>, 'features'> & { features?: Features }
) {
  return useTable({ ...options, features: options.features ?? baseFeatures })
}
