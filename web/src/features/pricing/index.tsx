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
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { PublicLayout } from '@/components/layout'
import { PageTransition } from '@/components/page-transition'

import {
  LoadingSkeleton,
  EmptyState,
  PricingTable,
  PricingSidebar,
  SearchBar,
  PricingToolbar,
  ModelCardGrid,
  ModelDetailsDrawer,
} from './components'
import { EXCLUDED_GROUPS, FILTER_ALL, VIEW_MODES } from './constants'
import { useFilters } from './hooks/use-filters'
import { usePricingData } from './hooks/use-pricing-data'

export function Pricing() {
  const { t } = useTranslation()
  const [selectedModelName, setSelectedModelName] = useState<string | null>(
    null
  )

  const {
    models,
    groupCatalog,
    groupMarketActivity,
    displayTiers,
    scope,
    error,
    refetch,
    groupRatio,
    usableGroup,
    endpointMap,
    autoGroups,
    modelOrder,
    isLoading,
    priceRate,
    usdExchangeRate,
  } = usePricingData()

  const availableGroups = useMemo(
    () =>
      Object.keys(usableGroup || {}).filter(
        (g) => !EXCLUDED_GROUPS.includes(g)
      ),
    [usableGroup]
  )

  const {
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
  } = useFilters(
    models,
    groupCatalog,
    scope,
    availableGroups,
    modelOrder,
    displayTiers
  )

  const handleModelClick = useCallback((modelName: string) => {
    setSelectedModelName(modelName)
  }, [])

  const selectedModel = useMemo(
    () =>
      selectedModelName
        ? facets.visibleModels.find(
            (model) => model.model_name === selectedModelName
          ) || null
        : null,
    [facets.visibleModels, selectedModelName]
  )

  const renderPricingContent = () => {
    if (filteredModels.length === 0) {
      return (
        <EmptyState
          searchQuery={searchInput}
          hasActiveFilters={hasActiveFilters}
          onClearFilters={clearFilters}
        />
      )
    }

    // A different result set starts on the first page in either view.
    const resultKey = JSON.stringify([
      scope,
      platform,
      searchInput,
      groupFilter,
      quotaTypeFilter,
      endpointTypeFilter,
      sortBy,
    ])
    if (viewMode === VIEW_MODES.CARD) {
      return (
        <ModelCardGrid
          key={resultKey}
          models={filteredModels}
          platformPriceGroups={platformPriceGroups}
          endpointMap={endpointMap}
          onModelClick={handleModelClick}
          priceRate={priceRate}
          usdExchangeRate={usdExchangeRate}
          tokenUnit={tokenUnit}
          showRechargePrice={showRechargePrice}
          selectedGroup={groupFilter}
        />
      )
    }

    return (
      <PricingTable
        key={resultKey}
        models={filteredModels}
        platformPriceGroups={platformPriceGroups}
        endpointMap={endpointMap}
        priceRate={priceRate}
        usdExchangeRate={usdExchangeRate}
        tokenUnit={tokenUnit}
        showRechargePrice={showRechargePrice}
        selectedGroup={groupFilter}
        onModelClick={handleModelClick}
      />
    )
  }

  if (error) {
    return (
      <PublicLayout>
        <ErrorState
          onRetry={() => {
            void refetch()
          }}
        />
      </PublicLayout>
    )
  }

  if (isLoading) {
    return (
      <PublicLayout showMainContainer={false}>
        <div className='mx-auto w-full max-w-[1440px] px-3 pt-16 pb-8 sm:px-6 sm:pt-20 sm:pb-10 xl:px-8'>
          <LoadingSkeleton viewMode={viewMode} />
        </div>
      </PublicLayout>
    )
  }

  return (
    <PublicLayout showMainContainer={false}>
      <PageTransition className='mx-auto w-full max-w-[1440px] min-w-0 px-3 pt-16 pb-8 sm:px-6 sm:pt-20 sm:pb-10 xl:px-8'>
        <header className='mx-auto mb-5 max-w-3xl pt-5 text-center sm:mb-7 sm:pt-7'>
          <h1 className='text-[clamp(2rem,4vw,3rem)] leading-tight font-bold tracking-tight'>
            {t('Model Square')}
          </h1>
          <p className='text-muted-foreground mt-2 text-sm sm:text-base'>
            {t('This site currently has {{count}} models enabled', {
              count: models.length,
            })}
          </p>
        </header>
        <main className='min-w-0 space-y-5'>
          <PricingSidebar
            facets={facets}
            displayTiers={displayTiers}
            platform={platform}
            onPlatformChange={setPlatform}
            groups={availableGroups}
            groupCatalog={groupCatalog}
            groupMarketActivity={groupMarketActivity}
            groupRatios={groupRatio}
            groupFilter={groupFilter}
            onGroupChange={setGroupFilter}
            quotaTypeFilter={quotaTypeFilter}
            onQuotaTypeChange={setQuotaTypeFilter}
            endpointTypeFilter={endpointTypeFilter}
            onEndpointTypeChange={setEndpointTypeFilter}
            models={models}
          />
          <SearchBar
            value={searchInput}
            onChange={setSearchInput}
            onClear={clearSearch}
            placeholder={t('Search model name, provider, endpoint, or tag...')}
            className='mx-auto w-full max-w-2xl'
          />
          <PricingToolbar
            filteredCount={filteredModels.length}
            totalCount={models.length}
            platformBest={Boolean(
              platformPriceGroups && groupFilter === FILTER_ALL
            )}
            hasActiveFilters={hasActiveFilters || Boolean(searchInput)}
            onClearFilters={clearFilters}
            sortBy={sortBy}
            modelOrder={modelOrder}
            onSortChange={setSortBy}
            tokenUnit={tokenUnit}
            onTokenUnitChange={setTokenUnit}
            showRechargePrice={showRechargePrice}
            onRechargePriceChange={setShowRechargePrice}
            viewMode={viewMode}
            onViewModeChange={setViewMode}
          />

          {renderPricingContent()}
        </main>

        {selectedModel && (
          <ModelDetailsDrawer
            selectedGroup={groupFilter}
            open={Boolean(selectedModel)}
            onOpenChange={(open) => {
              if (!open) setSelectedModelName(null)
            }}
            model={selectedModel}
            groupRatio={groupRatio || {}}
            usableGroup={usableGroup || {}}
            endpointMap={
              (endpointMap as Record<
                string,
                { path?: string; method?: string }
              >) || {}
            }
            autoGroups={autoGroups || []}
            priceRate={priceRate ?? 1}
            usdExchangeRate={usdExchangeRate ?? 1}
            tokenUnit={tokenUnit}
            showRechargePrice={showRechargePrice}
          />
        )}
      </PageTransition>
    </PublicLayout>
  )
}
