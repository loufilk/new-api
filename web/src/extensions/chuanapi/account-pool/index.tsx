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
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePagination } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { useStatus } from '@/hooks/use-status'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import {
  STATUS_QUERY_KEY,
  type StatusRecord,
  writeCachedStatus,
} from '@/lib/status-query'

import { isAccountPoolDisabled } from './access'
import { AccountTable } from './account-table'
import { getPoolSnapshot } from './api'
import type { PoolFilters } from './types'

const plans = ['free', 'plus', 'pro', 'promax', 'team']
const statuses = ['available', 'rate_limited', 'unavailable', 'paused', 'error']
const emptyAccounts: never[] = []

export function AccountPoolPage() {
  return (
    <SectionPageLayout>
      <SectionPageLayout.Content>
        <AccountPoolDashboard />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export function AccountPoolDashboard() {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const { status } = useStatus()
  const enabled = status?.account_pool_enabled === true
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [filters, setFilters] = useState<PoolFilters>({
    q: '',
    type: '',
    status: '',
    group: '',
    page: 1,
    page_size: 20,
  })
  const query = useQuery({
    queryKey: ['account-pool', 'snapshot', filters],
    enabled,
    queryFn: async ({ signal }) => {
      try {
        return await getPoolSnapshot(filters, signal)
      } catch (error) {
        if (isAccountPoolDisabled(error)) {
          client.setQueryData<StatusRecord>(STATUS_QUERY_KEY, (previous) => ({
            ...previous,
            account_pool_enabled: false,
          }))
          writeCachedStatus({
            ...client.getQueryData<StatusRecord>(STATUS_QUERY_KEY),
            account_pool_enabled: false,
          })
        }
        throw error
      }
    },
    staleTime: 0,
    gcTime: 0,
    refetchOnMount: 'always',
    refetchOnWindowFocus: 'always',
    retry: false,
    meta: { errorToast: false },
  })
  // A previously fetched list is never proof that the page is still enabled.
  const snapshot =
    enabled && query.isFetchedAfterMount && !query.isError && !query.isFetching
      ? query.data
      : undefined
  useEffect(() => {
    if (!enabled) {
      void client
        .cancelQueries({ queryKey: ['account-pool', 'snapshot'] })
        .then(() => {
          client.removeQueries({ queryKey: ['account-pool', 'snapshot'] })
        })
    }
  }, [client, enabled])
  const table = useReactTable({
    data: snapshot?.items ?? emptyAccounts,
    columns: [],
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    rowCount: snapshot?.total ?? 0,
    state: {
      pagination: { pageIndex: filters.page - 1, pageSize: filters.page_size },
    },
    onPaginationChange: (update) =>
      setFilters((previous) => {
        const next =
          typeof update === 'function'
            ? update({
                pageIndex: previous.page - 1,
                pageSize: previous.page_size,
              })
            : update
        return {
          ...previous,
          page: next.pageIndex + 1,
          page_size: next.pageSize,
        }
      }),
  })
  const setFilter = (key: keyof PoolFilters, value: string) =>
    setFilters((previous) => ({ ...previous, [key]: value, page: 1 }))

  if (!enabled) return <EmptyState title={t('Account pool is disabled')} />

  return (
    <div className='mx-auto flex max-w-7xl flex-col gap-6 py-4'>
      <header className='flex flex-wrap items-start justify-between gap-4'>
        <div className='flex min-w-0 flex-col gap-2'>
          <Badge variant='secondary'>{t('pool.readOnly')}</Badge>
          <h1 className='text-3xl font-semibold tracking-tight'>
            {t('pool.title')}
          </h1>
          <p className='text-muted-foreground text-sm'>{t('pool.subtitle')}</p>
        </div>
        <Button
          variant='outline'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          <RefreshCw aria-hidden />
          {t('Refresh')}
        </Button>
      </header>
      <Alert>
        <AlertDescription>
          <p>{t('pool.scope')}</p>
          <p>{t('pool.metricScope')}</p>
        </AlertDescription>
      </Alert>
      <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
        <div className='flex flex-col gap-2'>
          <Label htmlFor='pool-search'>{t('pool.search')}</Label>
          <Input
            id='pool-search'
            value={filters.q}
            onChange={(event) => setFilter('q', event.target.value)}
            maxLength={80}
            placeholder={t('pool.searchPlaceholder')}
          />
        </div>
        <div className='flex flex-col gap-2'>
          <Label htmlFor='pool-type'>{t('pool.type')}</Label>
          <NativeSelect
            id='pool-type'
            value={filters.type}
            onChange={(event) => setFilter('type', event.target.value)}
            className='w-full'
          >
            <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
            {plans.map((plan) => (
              <NativeSelectOption key={plan} value={plan}>
                {plan === 'promax'
                  ? plan
                  : plan.charAt(0).toUpperCase() + plan.slice(1)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className='flex flex-col gap-2'>
          <Label htmlFor='pool-status'>{t('Status')}</Label>
          <NativeSelect
            id='pool-status'
            value={filters.status}
            onChange={(event) => setFilter('status', event.target.value)}
            className='w-full'
          >
            <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
            {statuses.map((status) => (
              <NativeSelectOption key={status} value={status}>
                {t(`pool.status.${status}`)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className='flex flex-col gap-2'>
          <Label htmlFor='pool-group'>{t('pool.group')}</Label>
          <NativeSelect
            id='pool-group'
            value={filters.group}
            onChange={(event) => setFilter('group', event.target.value)}
            className='w-full'
          >
            <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
            {filters.group &&
              !snapshot?.groups.some(
                (group) => group.name === filters.group
              ) && (
                <NativeSelectOption value={filters.group}>
                  {filters.group}
                </NativeSelectOption>
              )}
            {snapshot?.groups.map((group) => (
              <NativeSelectOption key={group.name} value={group.name}>
                {group.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
      </div>
      {(query.isPending || query.isFetching) && <LoadingState />}
      {query.isError && (
        <ErrorState
          title={t('pool.loadFailed')}
          onRetry={() => void query.refetch()}
        />
      )}
      {snapshot && (
        <>
          {(snapshot.state === 'stale' ||
            snapshot.state === 'unavailable' ||
            query.isError) && (
            <Alert>
              <AlertDescription>{t('pool.stale')}</AlertDescription>
            </Alert>
          )}
          <p className='text-muted-foreground text-sm' role='status'>
            {t('Last updated:')}{' '}
            {snapshot.updated_at > 0
              ? new Date(snapshot.updated_at * 1000).toLocaleString(locale)
              : '—'}
          </p>
          {!snapshot.configured ? (
            <EmptyState
              title={t('pool.notConfigured')}
              description={t('pool.noSyntheticData')}
            />
          ) : (
            <>
              <section
                aria-label={t('pool.groupSummary')}
                className='flex flex-col gap-3'
              >
                <h2 className='text-lg font-semibold'>
                  {t('pool.groupSummary')}
                </h2>
                <p className='text-muted-foreground text-xs'>
                  {t('pool.summaryScope')}
                </p>
                <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
                  {snapshot.groups.map((group) => (
                    <Card key={group.name} className='min-w-0'>
                      <CardHeader>
                        <CardTitle>
                          <h3 className='break-all'>
                            {group.name} · {formatNumber(group.ratio, locale)}×
                          </h3>
                        </CardTitle>
                      </CardHeader>
                      <CardContent className='flex flex-col gap-2 text-sm'>
                        <span>
                          {t('pool.availableCount', {
                            count: group.available,
                            total: group.total,
                          })}
                        </span>
                        <span className='text-muted-foreground'>
                          {t('pool.concurrency')}: {group.concurrency ?? '—'} /{' '}
                          {group.concurrency_limit ?? '—'}
                        </span>
                      </CardContent>
                    </Card>
                  ))}
                </div>
              </section>
              {snapshot.items.length ? (
                <AccountTable accounts={snapshot.items} />
              ) : (
                <EmptyState title={t('pool.noMatches')} />
              )}
              <div className='flex flex-wrap items-center gap-3'>
                <Label htmlFor='pool-page-size'>{t('Rows per page')}</Label>
                <NativeSelect
                  id='pool-page-size'
                  value={filters.page_size}
                  onChange={(event) =>
                    setFilters((previous) => ({
                      ...previous,
                      page: 1,
                      page_size: Number(event.target.value),
                    }))
                  }
                >
                  {[10, 20, 50].map((size) => (
                    <NativeSelectOption key={size} value={size}>
                      {size}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                <div className='min-w-0 flex-1'>
                  <DataTablePagination table={table} compact />
                </div>
              </div>
            </>
          )}
        </>
      )}
    </div>
  )
}
