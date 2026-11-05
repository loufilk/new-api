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
import { Fingerprint, Info, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Main } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { getSiteTests, getTestCatalogue } from './api'
import { BudgetDialog } from './budget-dialog'
import { ModelTestCard } from './model-card'
import { TestResultDialog } from './result-dialog'
import { SelfTestDialog } from './self-test-dialog'
import type { TestRun } from './types'
import { useModelTest } from './use-model-test'

export function IntelligencePage() {
  return (
    <Main>
      <div className='bg-muted/15 min-h-0 flex-1 overflow-auto p-3 sm:p-5 lg:p-6'>
        <IntelligenceDashboard />
      </div>
    </Main>
  )
}

export function IntelligenceDashboard() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
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
  const currentOutcome = pendingRun ?? test.start.data
  const activeRuns = (data?.items ?? []).filter(
    (run) => run.status === 'running' && run.id !== test.pending?.request_id
  )
  const sharedPoints = site.data?.user_items ?? []
  const groupNames = [
    ...new Set([
      ...Object.keys(
        test.enabled ? (data?.eligible_models_by_group ?? {}) : {}
      ),
      ...(site.data?.targets ?? []).map((target) => target.group),
      ...(site.data?.items ?? []).map((run) => run.group),
      ...sharedPoints.map((run) => run.group),
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
          ...sharedPoints
            .filter((run) => run.group === group)
            .map((run) => run.model),
        ]),
      ].sort()
      return { group, models }
    })
    .filter((entry) => entry.models.length)

  const refresh = () => {
    void history.refetch()
    void site.refetch()
    if (test.enabled) void catalogue.refetch()
  }
  return (
    <div className='flex w-full min-w-0 flex-col gap-4'>
      <header className='flex flex-wrap items-start justify-between gap-3'>
        <div className='flex min-w-0 flex-col gap-1.5'>
          <h1 className='text-xl font-semibold tracking-tight'>
            {t('fingerprint.title')}
          </h1>
          <p className='text-muted-foreground text-xs leading-relaxed'>
            {t('fingerprint.subtitle')}
          </p>
        </div>
        <div className='flex shrink-0 items-center gap-2'>
          <Button
            variant='outline'
            size='sm'
            className='bg-card min-h-9 sm:min-h-7'
            disabled={history.isFetching || site.isFetching}
            onClick={refresh}
          >
            <RefreshCw aria-hidden className='size-3.5' />
            {t('monitor.refresh')}
          </Button>
        </div>
      </header>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-2 text-xs'>
          <span className='inline-flex items-center gap-1.5'>
            <span aria-hidden className='size-2 rounded-full bg-emerald-500' />
            {t('fingerprint.effectiveResults')}
          </span>
          <span className='inline-flex items-center gap-1.5'>
            <span
              aria-hidden
              className='bg-muted-foreground/30 size-2 rounded-full'
            />
            {t('fingerprint.emptySlot')}
          </span>
        </div>
        <p className='text-muted-foreground text-[11px]'>
          {t('fingerprint.browserTimezone')}
        </p>
      </div>
      {!history.isPending && !test.enabled && (
        <Alert role='status' className='border-border/70 bg-muted/30'>
          <Info aria-hidden />
          <AlertDescription className='text-xs'>
            {history.isError || data?.availability_error
              ? t('fingerprint.availabilityUnknown')
              : t('fingerprint.testsDisabled')}
          </AlertDescription>
        </Alert>
      )}
      {(history.isPending || site.isPending) && <LoadingState />}
      {(history.isError ||
        site.isError ||
        (test.enabled && catalogue.isError)) && (
        <ErrorState onRetry={refresh} />
      )}
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
      {currentOutcome &&
        !unresolvedPending &&
        currentOutcome.status !== 'running' && (
          <p
            role='status'
            className='text-muted-foreground flex flex-wrap items-center gap-2 text-xs'
          >
            <span className='min-w-0 break-all'>{currentOutcome.model}</span>
            <Badge variant='outline'>
              {t(`intelligence.status.${currentOutcome.status}`)}
            </Badge>
          </p>
        )}
      {activeRuns.map((run) => (
        <Alert
          key={run.id}
          role='status'
          className='border-border/70 bg-muted/30'
        >
          <AlertDescription className='flex flex-wrap items-center justify-between gap-3'>
            <span className='min-w-0 break-all'>
              {run.group} · {run.model} · {t('intelligence.status.running')}
            </span>
            <Button
              variant='outline'
              size='sm'
              disabled={test.cancel.isPending || run.cancel_requested}
              onClick={() => test.cancel.mutate(run.id)}
            >
              {run.cancel_requested
                ? t('intelligence.cancelRequested')
                : t('Cancel')}
            </Button>
          </AlertDescription>
        </Alert>
      ))}
      {visibleGroups.map(({ group, models }) => {
        const groupInfo = catalogue.data?.groups.find(
          (item) => item.name === group
        )
        return (
          <section
            key={group}
            className='bg-card border-border/70 min-w-0 overflow-hidden rounded-xl border'
            aria-label={group}
          >
            <header className='flex flex-wrap items-center gap-2.5 px-4 py-2.5'>
              <h2 className='text-sm font-semibold break-all'>{group}</h2>
              {groupInfo && (
                <Badge
                  variant='secondary'
                  className='rounded-full bg-sky-50 px-2 py-0.5 text-[11px] font-medium text-sky-700 dark:bg-sky-950 dark:text-sky-300'
                >
                  {t('Ratio')} {formatNumber(groupInfo.ratio, locale)}×
                </Badge>
              )}
            </header>
            {models.map((model) => (
              <ModelTestCard
                key={model}
                model={model}
                group={group}
                runs={sharedPoints.filter(
                  (run) => run.group === group && run.model === model
                )}
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
          icon={Fingerprint}
          bordered
          className='bg-card min-h-48 rounded-xl'
          title={t('fingerprint.noConfiguredModels')}
        />
      )}
      <p className='text-muted-foreground text-[11px] leading-relaxed'>
        {t('fingerprint.timelineHelp')}
      </p>
      {selfTest && test.enabled && !test.quote && (
        <SelfTestDialog
          key={`${selfTest.group}:${selfTest.model}`}
          {...selfTest}
          tokens={data?.eligible_tokens_by_group?.[selfTest.group] ?? []}
          maxOutputTokens={0}
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
      {selected && (
        <TestResultDialog run={selected} onClose={() => setSelected(null)} />
      )}
    </div>
  )
}
