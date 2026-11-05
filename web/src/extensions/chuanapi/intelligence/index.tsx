import { useQuery } from '@tanstack/react-query'
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
import { Link } from '@tanstack/react-router'
import { RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getSiteTests, getTestCatalogue } from './api'
import { BudgetDialog } from './budget-dialog'
import { TestHistoryList } from './history-list'
import { ModelTestCard } from './model-card'
import { TestResultDialog } from './result-dialog'
import { SelfTestDialog } from './self-test-dialog'
import type { TestRun } from './types'
import { useModelTest } from './use-model-test'

export function IntelligencePage() {
  return (
    <SectionPageLayout>
      <SectionPageLayout.Content>
        <IntelligenceDashboard />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

export function IntelligenceDashboard() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const isAdmin = useAuthStore(
    (state) => (state.auth.user?.role ?? 0) >= ROLE.ADMIN
  )
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<TestRun | null>(null)
  const [selfTest, setSelfTest] = useState<{
    model: string
    group: string
  } | null>(null)
  const test = useModelTest()
  useEffect(() => {
    if (!test.enabled) setSelfTest(null)
  }, [test.enabled])
  const history = test.history
  const catalogue = useQuery({
    queryKey: ['model-tests', 'catalogue'],
    enabled: test.enabled,
    queryFn: ({ signal }) => getTestCatalogue(signal),
    retry: false,
    meta: { errorToast: false },
  })
  const site = useQuery({
    queryKey: ['model-tests', 'site'],
    queryFn: ({ signal }) => getSiteTests(signal),
    retry: false,
    refetchInterval: 30_000,
    meta: { errorToast: false },
  })
  const data = history.data
  const pendingRun = data?.items.find(
    (run) => run.id === test.pending?.request_id
  )
  const unresolvedPending =
    !!test.pending && (!pendingRun || pendingRun.status === 'running')
  const running =
    test.start.isPending ||
    !!data?.items.some((run) => run.status === 'running')
  const currentSelection =
    data?.items.find((run) => run.id === selected?.id) ?? selected
  const personalPoints = data?.effective_points ?? data?.items ?? []
  const groupNames = [
    ...new Set([
      ...Object.keys(
        test.enabled ? (data?.eligible_models_by_group ?? {}) : {}
      ),
      ...(site.data?.targets ?? []).map((target) => target.group),
      ...(site.data?.items ?? []).map((run) => run.group),
      ...personalPoints
        .filter((run) => run.fingerprint_result)
        .map((run) => run.group),
    ]),
  ].sort()
  const visibleGroups = groupNames
    .map((group) => {
      const models = [
        ...new Set([
          ...(test.enabled
            ? (data?.eligible_models_by_group?.[group] ?? [])
            : []),
          ...(site.data?.targets ?? [])
            .filter((target) => target.group === group)
            .map((target) => target.model),
          ...(site.data?.items ?? [])
            .filter((run) => run.group === group)
            .map((run) => run.model),
          ...personalPoints
            .filter((run) => run.group === group && run.fingerprint_result)
            .map((run) => run.model),
        ]),
      ]
        .filter((model) =>
          `${group} ${model}`.toLowerCase().includes(search.toLowerCase())
        )
        .sort()
      return { group, models }
    })
    .filter((entry) => entry.models.length)

  const refresh = () => {
    void history.refetch()
    void site.refetch()
    if (test.enabled) void catalogue.refetch()
  }
  return (
    <div className='mx-auto flex max-w-[100rem] flex-col gap-5 py-4'>
      <header className='flex flex-wrap items-start justify-between gap-4'>
        <div className='flex min-w-0 flex-col gap-2'>
          <h1 className='text-2xl font-semibold tracking-tight'>
            {t('fingerprint.title')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t('fingerprint.subtitle')}
          </p>
        </div>
        <Button
          variant='outline'
          disabled={history.isFetching || site.isFetching}
          onClick={refresh}
        >
          <RefreshCw aria-hidden />
          {t('monitor.refresh')}
        </Button>
      </header>
      {isAdmin && (
        <Button
          variant='outline'
          className='w-fit'
          render={
            <Link
              to='/system-settings/content/$section'
              params={{ section: 'model-tests' }}
            />
          }
        >
          {t('fingerprint.settings')}
        </Button>
      )}
      <Alert role='note'>
        <AlertDescription>{t('fingerprint.disclaimer')}</AlertDescription>
      </Alert>
      {!history.isPending && !test.enabled && (
        <Alert role='status'>
          <AlertDescription>
            {t(
              history.isError || data?.availability_error
                ? 'Model test availability could not be verified. New tests are blocked; history and cancellation remain available.'
                : 'New model tests are disabled. You can still view your history and cancel active tests.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {(history.isPending || site.isPending) && <LoadingState />}
      {(history.isError ||
        site.isError ||
        (test.enabled && catalogue.isError)) && (
        <ErrorState onRetry={refresh} />
      )}
      <div className='flex flex-wrap items-end justify-between gap-3'>
        <p className='text-muted-foreground text-xs'>
          {t('fingerprint.timelineHelp')}
        </p>
        <div className='flex flex-col gap-1'>
          <Label htmlFor='fingerprint-search'>{t('monitor.search')}</Label>
          <Input
            id='fingerprint-search'
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>
      </div>
      {unresolvedPending && (
        <Alert>
          <AlertDescription>
            <div className='flex flex-wrap items-center gap-3'>
              <span>
                {t(
                  test.uncertain
                    ? 'intelligence.uncertain'
                    : 'intelligence.submitted'
                )}
              </span>
              {test.uncertain && (
                <Button
                  variant='outline'
                  onClick={test.retry}
                  disabled={!test.enabled || test.start.isPending}
                >
                  {t('intelligence.retrySame')}
                </Button>
              )}
              <Button
                variant='outline'
                onClick={() => {
                  if (test.pending) test.cancel.mutate(test.pending.request_id)
                }}
                disabled={test.cancel.isPending}
              >
                {t('Cancel')}
              </Button>
            </div>
          </AlertDescription>
        </Alert>
      )}
      {visibleGroups.map(({ group, models }) => {
        const groupInfo = catalogue.data?.groups.find(
          (item) => item.name === group
        )
        return (
          <section
            key={group}
            className='bg-card min-w-0 overflow-hidden rounded-xl border'
            aria-label={group}
          >
            <header className='flex flex-wrap items-center gap-3 px-4 py-3'>
              <h2 className='font-semibold break-all'>{group}</h2>
              {groupInfo && (
                <Badge variant='secondary'>
                  {formatNumber(groupInfo.ratio, locale)}×
                </Badge>
              )}
            </header>
            {models.map((model) => (
              <ModelTestCard
                key={model}
                model={model}
                group={group}
                runs={[
                  ...personalPoints.filter(
                    (run) => run.group === group && run.model === model
                  ),
                  ...(data?.items ?? []).filter(
                    (run) =>
                      run.group === group &&
                      run.model === model &&
                      !personalPoints.some((point) => point.id === run.id)
                  ),
                ]}
                siteRuns={(site.data?.items ?? []).filter(
                  (run) => run.group === group && run.model === model
                )}
                allowTest={
                  test.enabled &&
                  (data?.eligible_models_by_group?.[group] ?? []).includes(
                    model
                  )
                }
                disabled={
                  running ||
                  test.estimate.isPending ||
                  unresolvedPending ||
                  history.isError
                }
                estimating={
                  test.estimate.isPending &&
                  test.estimate.variables?.model === model &&
                  test.estimate.variables?.group === group
                }
                onEstimate={() => setSelfTest({ model, group })}
                onSelect={setSelected}
              />
            ))}
          </section>
        )
      })}
      {!history.isPending && !site.isPending && !visibleGroups.length && (
        <EmptyState
          title={t(
            search ? 'intelligence.noModels' : 'fingerprint.noConfiguredModels'
          )}
        />
      )}
      {data && (
        <section className='flex flex-col gap-3'>
          <h2 className='text-xl font-semibold'>
            {t('intelligence.myHistory')}
          </h2>
          <p className='text-muted-foreground text-xs'>
            {t('fingerprint.legacyHistory')}
          </p>
          <TestHistoryList
            runs={data.items}
            onSelect={setSelected}
            onCancel={(id) => test.cancel.mutate(id)}
            cancelling={test.cancel.isPending}
          />
        </section>
      )}
      {selfTest && test.enabled && !test.quote && (
        <SelfTestDialog
          key={`${selfTest.group}:${selfTest.model}`}
          {...selfTest}
          tokens={data?.eligible_tokens_by_group?.[selfTest.group] ?? []}
          maxOutputTokens={data?.limits.default_output_tokens ?? 2048}
          pending={test.estimate.isPending}
          disabled={running || unresolvedPending}
          onClose={() => {
            test.dismissEstimate()
            setSelfTest(null)
          }}
          onEstimate={(selection) => test.estimate.mutate(selection)}
        />
      )}
      {test.quote && (
        <BudgetDialog
          key={test.quote.confirmation_token}
          quote={test.quote}
          onClose={() => test.setQuote(null)}
          onConfirm={() => {
            setSelfTest(null)
            test.confirm()
          }}
        />
      )}
      {currentSelection && (
        <TestResultDialog
          run={currentSelection}
          onClose={() => setSelected(null)}
        />
      )}
    </div>
  )
}
