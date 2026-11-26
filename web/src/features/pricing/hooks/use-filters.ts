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
import { useSearch, useNavigate, useLocation } from '@tanstack/react-router'
import { useMemo, useCallback, useState } from 'react'

import { useDebounce } from '@/hooks/use-debounce'

import {
  FILTER_ALL,
  MODEL_ORDER_SORTS,
  SORT_OPTIONS,
  canSortCatalogBy,
  normalizeSortOption,
  QUOTA_TYPES,
  ENDPOINT_TYPES,
  DEFAULT_TOKEN_UNIT,
  VIEW_MODES,
  type ViewMode,
} from '../constants'
import { filterAndSortModels } from '../lib/filters'
import { marketFacets } from '../lib/market-facets'
import { activeBillingExpressions } from '../lib/market-groups'
import type { PricingSearch } from '../lib/search'
import type {
  MarketGroupCatalog,
  ModelOrder,
  PricingModel,
  TokenUnit,
} from '../types'
import { useBillingTime } from './use-billing-time'

function normalizeViewMode(value: unknown): ViewMode {
  if (value === VIEW_MODES.TABLE) {
    return VIEW_MODES.TABLE
  }
  return VIEW_MODES.CARD
}

declare module '@tanstack/history' {
  interface HistoryState {
    marketGroupRef?: string
  }
}

export function useFilters(
  models: PricingModel[],
  groupCatalog?: MarketGroupCatalog,
  scope = 'public',
  availableGroups?: readonly string[],
  modelOrder?: ModelOrder,
  displayTiers = false
) {
  const filterState = useSearch({ from: '/pricing/' })
  const navigate = useNavigate({ from: '/pricing/' })
  // The browser history carries only an opaque reference, never a private group
  // identifier. A reload, new tab or new session cannot resolve the reference.
  const [privateGroups, setPrivateGroups] = useState<
    Map<string, { scope: string; group: string }>
  >(() => new Map())
  const ref = useLocation({
    select: (location) => location.state.marketGroupRef,
  })
  const selection = ref ? privateGroups.get(ref) : undefined
  const facets = useMemo(
    () =>
      marketFacets(
        models,
        availableGroups ?? [],
        groupCatalog,
        filterState.platform,
        displayTiers
      ),
    [models, availableGroups, groupCatalog, filterState.platform, displayTiers]
  )
  const { platform, priceGroups: platformPriceGroups, visibleModels } = facets

  const searchInput = filterState.search || ''
  const debouncedSearchInput = useDebounce(searchInput, 200)
  const requestedSort = normalizeSortOption(filterState.sort)
  const sortBy = canSortCatalogBy(requestedSort, modelOrder)
    ? requestedSort
    : SORT_OPTIONS.NAME
  const selectedGroup =
    selection?.scope === scope ? selection.group : filterState.group
  const groupFilter =
    selectedGroup &&
    (!availableGroups || facets.visibleGroups.includes(selectedGroup))
      ? selectedGroup
      : FILTER_ALL
  const quotaTypeFilter = filterState.quotaType || QUOTA_TYPES.ALL
  const endpointTypeFilter = filterState.endpointType || ENDPOINT_TYPES.ALL
  const tokenUnit: TokenUnit =
    filterState.tokenUnit === 'K' ? 'K' : DEFAULT_TOKEN_UNIT
  const viewMode = normalizeViewMode(filterState.view)
  const showRechargePrice = filterState.rechargePrice === true

  const updateFilters = useCallback(
    (updates: Partial<PricingSearch>, replace = false, privateRef = ref) => {
      void navigate({
        search: (previous) => ({ ...previous, ...updates }),
        state: (previous) => ({
          ...previous,
          marketGroupRef: privateRef || undefined,
        }),
        replace,
        resetScroll: false,
      })
    },
    [navigate, ref]
  )
  const setPlatform = useCallback(
    (value: string) => {
      if (value !== platform) {
        updateFilters(
          {
            platform: value === FILTER_ALL ? undefined : value,
            group: undefined,
          },
          false,
          ''
        )
      }
    },
    [platform, updateFilters]
  )

  const setSearchInput = useCallback(
    (v: string) => updateFilters({ search: v || undefined }, true),
    [updateFilters]
  )
  const setSortBy = useCallback(
    (v: string) =>
      updateFilters({ sort: v === SORT_OPTIONS.RECOMMENDED ? undefined : v }),
    [updateFilters]
  )
  const setGroupFilter = useCallback(
    (value: string) => {
      if (groupCatalog?.[value]?.exclusive) {
        const privateRef = crypto.randomUUID()
        setPrivateGroups(
          (previous) =>
            new Map([...previous, [privateRef, { scope, group: value }]])
        )
        updateFilters({ group: undefined }, false, privateRef)
        return
      }
      updateFilters(
        { group: value === FILTER_ALL ? undefined : value },
        false,
        ''
      )
    },
    [groupCatalog, scope, updateFilters]
  )
  const setQuotaTypeFilter = useCallback(
    (v: string) =>
      updateFilters({ quotaType: v === QUOTA_TYPES.ALL ? undefined : v }),
    [updateFilters]
  )
  const setEndpointTypeFilter = useCallback(
    (v: string) =>
      updateFilters({
        endpointType: v === ENDPOINT_TYPES.ALL ? undefined : v,
      }),
    [updateFilters]
  )
  const setTokenUnit = useCallback(
    (v: TokenUnit) =>
      updateFilters({ tokenUnit: v === DEFAULT_TOKEN_UNIT ? undefined : v }),
    [updateFilters]
  )
  const setViewMode = useCallback(
    (v: ViewMode) =>
      updateFilters({ view: v === VIEW_MODES.CARD ? undefined : v }),
    [updateFilters]
  )
  const setShowRechargePrice = useCallback(
    (v: boolean) => updateFilters({ rechargePrice: v || undefined }),
    [updateFilters]
  )

  const sortExpressions = useMemo(
    () =>
      sortBy === SORT_OPTIONS.NAME || MODEL_ORDER_SORTS.has(sortBy)
        ? []
        : activeBillingExpressions(models),
    [models, sortBy]
  )
  const clock = useBillingTime(sortExpressions)
  const filteredModels = useMemo(
    () =>
      filterAndSortModels(visibleModels, {
        search: debouncedSearchInput,
        group: groupFilter,
        quotaType: quotaTypeFilter,
        endpointType: endpointTypeFilter,
        sortBy,
        modelOrder,
        platformPriceGroups,
        now: clock === undefined ? undefined : new Date(clock),
      }),
    [
      visibleModels,
      modelOrder,
      debouncedSearchInput,
      platformPriceGroups,
      groupFilter,
      quotaTypeFilter,
      endpointTypeFilter,
      sortBy,
      clock,
    ]
  )
  const hasActiveFilters = [
    platform,
    groupFilter,
    quotaTypeFilter,
    endpointTypeFilter,
  ].some((value) => value !== FILTER_ALL)
  const clearFilters = useCallback(() => {
    updateFilters(
      {
        platform: undefined,
        group: undefined,
        quotaType: undefined,
        endpointType: undefined,
        search: undefined,
      },
      false,
      ''
    )
  }, [updateFilters])

  const clearSearch = useCallback(() => {
    updateFilters({ search: undefined })
  }, [updateFilters])

  return {
    facets,
    platform,
    platformPriceGroups,
    setPlatform,
    searchInput,
    sortBy,
    groupFilter,
    quotaTypeFilter,
    endpointTypeFilter,
    tokenUnit,
    viewMode,
    showRechargePrice,
    setSearchInput,
    setSortBy,
    setGroupFilter,
    setQuotaTypeFilter,
    setEndpointTypeFilter,
    setTokenUnit,
    setViewMode,
    setShowRechargePrice,
    filteredModels,
    hasActiveFilters,
    clearFilters,
    clearSearch,
  }
}
