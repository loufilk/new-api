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
import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { getTestCatalogue } from './api'
import { BudgetDialog } from './budget-dialog'
import { TestHistoryList } from './history-list'
import { ModelTestCard } from './model-card'
import { TestResultDialog } from './result-dialog'
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
  const [group, setGroup] = useState('')
  const [search, setSearch] = useState('')
  const [tokens, setTokens] = useState(2048)
  const [selected, setSelected] = useState<TestRun | null>(null)
  const test = useModelTest()
  const history = test.history
  const catalogue = useQuery({
    queryKey: ['model-tests', 'catalogue'],
    enabled: test.enabled,
    queryFn: ({ signal }) => getTestCatalogue(signal),
    retry: false,
    meta: { errorToast: false },
  })
  const data = history.data
  const pendingRun = data?.items.find(
    (run) => run.id === test.pending?.request_id
  )
  const unresolvedPending =
    !!test.pending && (!pendingRun || pendingRun.status === 'running')
  const activeGroup = group || catalogue.data?.groups[0]?.name || ''
  const groupInfo = catalogue.data?.groups.find(
    (item) => item.name === activeGroup
  )
  const eligible = data?.eligible_models_by_group?.[activeGroup] ?? []
  const models = (catalogue.data?.models_by_group[activeGroup] ?? []).filter(
    (model) =>
      eligible.includes(model) &&
      model.toLowerCase().includes(search.toLowerCase())
  )
  const running =
    test.start.isPending ||
    !!data?.items.some((run) => run.status === 'running')
  const currentSelection =
    data?.items.find((run) => run.id === selected?.id) ?? selected

  return (
    <div className='mx-auto flex max-w-7xl flex-col gap-6 py-4'>
      <header className='flex flex-wrap items-start justify-between gap-4'>
        <div className='flex min-w-0 flex-col gap-2'>
          <Badge variant='secondary'>{t('intelligence.reference')}</Badge>
          <h1 className='text-3xl font-semibold tracking-tight'>
            {t('intelligence.title')}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t('intelligence.subtitle')}
          </p>
        </div>
        <Button
          variant='outline'
          disabled={history.isFetching || catalogue.isFetching}
          onClick={() => {
            void history.refetch()
            void catalogue.refetch()
          }}
        >
          <RefreshCw aria-hidden />
          {t('monitor.refresh')}
        </Button>
      </header>
      <Alert role='note'>
        <AlertDescription>{t('intelligence.scope')}</AlertDescription>
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
      {(history.isPending || (test.enabled && catalogue.isPending)) && (
        <LoadingState />
      )}
      {(history.isError || (test.enabled && catalogue.isError)) && (
        <ErrorState
          onRetry={() => {
            void history.refetch()
            void catalogue.refetch()
          }}
        />
      )}
      {data && (
        <>
          {test.enabled && (
            <section
              className='flex flex-col gap-3'
              aria-label={t('intelligence.siteResults')}
            >
              <h2 className='text-xl font-semibold'>
                {t('intelligence.siteResults')}
              </h2>
              <EmptyState
                className='min-h-32'
                title={t('intelligence.siteUnpublished')}
                description={t('intelligence.noScheduler')}
              />
            </section>
          )}
          <section
            className='flex flex-col gap-4'
            aria-label={t('intelligence.myTests')}
          >
            {test.enabled && (
              <>
                <div className='flex flex-col gap-2'>
                  <h2 className='text-xl font-semibold'>
                    {t('intelligence.myTests')}
                  </h2>
                  <p className='text-muted-foreground text-sm'>
                    {t('intelligence.limits', {
                      count: data.limits.runs_per_hour,
                      concurrency: data.limits.concurrency_per_user,
                    })}
                  </p>
                </div>
                <Collapsible className='rounded-lg border p-3'>
                  <CollapsibleTrigger
                    render={
                      <Button
                        variant='outline'
                        className='h-auto min-h-8 max-w-full py-2 text-start whitespace-normal'
                      />
                    }
                  >
                    {t('intelligence.publicPrompt')} · {data.benchmark.id} ·{' '}
                    {data.benchmark.license}
                  </CollapsibleTrigger>
                  <CollapsibleContent>
                    <pre className='mt-3 text-sm break-words whitespace-pre-wrap'>
                      {data.benchmark.prompt}
                    </pre>
                  </CollapsibleContent>
                </Collapsible>
                <div className='grid gap-3 sm:grid-cols-3'>
                  <div className='flex min-w-0 flex-col gap-2'>
                    <Label htmlFor='test-group'>{t('pool.group')}</Label>
                    <NativeSelect
                      id='test-group'
                      className='w-full'
                      value={activeGroup}
                      onChange={(event) => setGroup(event.target.value)}
                    >
                      {catalogue.data?.groups.map((item) => (
                        <NativeSelectOption key={item.name} value={item.name}>
                          {item.name} · {formatNumber(item.ratio, locale)}×
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </div>
                  <div className='flex flex-col gap-2'>
                    <Label htmlFor='test-model-search'>
                      {t('monitor.search')}
                    </Label>
                    <Input
                      id='test-model-search'
                      value={search}
                      onChange={(event) => setSearch(event.target.value)}
                    />
                  </div>
                  <div className='flex flex-col gap-2'>
                    <Label htmlFor='test-tokens'>
                      {t('intelligence.outputLimit')}
                    </Label>
                    <NativeSelect
                      id='test-tokens'
                      className='w-full'
                      value={tokens}
                      onChange={(event) =>
                        setTokens(Number(event.target.value))
                      }
                    >
                      {[2048, 4096]
                        .filter(
                          (value) => value <= data.limits.max_output_tokens
                        )
                        .map((value) => (
                          <NativeSelectOption key={value} value={value}>
                            {formatNumber(value, locale)}
                          </NativeSelectOption>
                        ))}
                    </NativeSelect>
                  </div>
                </div>
              </>
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
                        if (test.pending) {
                          test.cancel.mutate(test.pending.request_id)
                        }
                      }}
                      disabled={test.cancel.isPending}
                    >
                      {t('Cancel')}
                    </Button>
                  </div>
                </AlertDescription>
              </Alert>
            )}
            {test.enabled && (
              <>
                <h3 className='font-semibold break-all'>
                  {activeGroup} · {formatNumber(groupInfo?.ratio ?? 1, locale)}×
                </h3>
                {models.length ? (
                  <div className='grid items-start gap-4 md:grid-cols-2 xl:grid-cols-3'>
                    {models.map((model) => (
                      <ModelTestCard
                        key={`${activeGroup}:${model}`}
                        model={model}
                        group={activeGroup}
                        runs={data.items.filter(
                          (run) =>
                            run.model === model && run.group === activeGroup
                        )}
                        disabled={
                          running ||
                          test.estimate.isPending ||
                          unresolvedPending ||
                          history.isError ||
                          catalogue.isError
                        }
                        estimating={
                          test.estimate.isPending &&
                          test.estimate.variables?.model === model
                        }
                        onEstimate={() =>
                          test.estimate.mutate({
                            model,
                            group: activeGroup,
                            max_output_tokens: tokens,
                          })
                        }
                        onSelect={setSelected}
                      />
                    ))}
                  </div>
                ) : (
                  <EmptyState title={t('intelligence.noModels')} />
                )}
              </>
            )}
          </section>
          <section className='flex flex-col gap-3'>
            <h2 className='text-xl font-semibold'>
              {t('intelligence.myHistory')}
            </h2>
            <TestHistoryList
              runs={data.items}
              onSelect={setSelected}
              onCancel={(id) => test.cancel.mutate(id)}
              cancelling={test.cancel.isPending}
            />
          </section>
        </>
      )}
      {test.quote && (
        <BudgetDialog
          key={test.quote.confirmation_token}
          quote={test.quote}
          onClose={() => test.setQuote(null)}
          onConfirm={test.confirm}
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
