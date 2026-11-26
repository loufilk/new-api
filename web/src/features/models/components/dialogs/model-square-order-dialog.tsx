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
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import { Reorder } from 'motion/react'
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { AutoGroupOrderItem } from '@/components/auto-group-order-item'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { FieldSet } from '@/components/ui/field'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { compareModelUsage } from '@/features/pricing/lib/filters'
import {
  SEGMENTED_SWITCH_CLASS,
  SEGMENTED_SWITCH_ITEM_CLASS,
} from '@/features/task-plugins/components/plugin-kind-switch'
import { toIntlLocale } from '@/i18n/languages'
import { formatCompactNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { getLobeIcon } from '@/lib/lobe-icon'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  getModelSquareOrder,
  getModelSquareUsage,
  updateModelSquareOrder,
} from '../../api'
import { modelsQueryKeys } from '../../lib'
import type {
  ModelSquareOrder,
  ModelSquareOrderPatch,
  ModelSquareUsage,
} from '../../types'

const MODE_KEY = 'sort_mode'
const METRIC_KEY = 'sort_metric'
const DAYS_KEY = 'sort_days'
const MODELS_KEY = 'sort_models'
const ORDER_KEYS = [MODE_KEY, METRIC_KEY, DAYS_KEY, MODELS_KEY] as const
const DEFAULT_ORDER: ModelSquareOrder = {
  sort_mode: 'manual',
  sort_metric: 'tokens',
  sort_days: 7,
  sort_models: [],
}

function orderMatches(
  order: ModelSquareOrder,
  expected: Partial<ModelSquareOrder>
): boolean {
  return ORDER_KEYS.every(
    (key) =>
      !(key in expected) ||
      JSON.stringify(order[key]) === JSON.stringify(expected[key])
  )
}

const PREVIEW_LIMIT = 10

function modelIcon(name: string, model?: ModelSquareUsage): ReactNode {
  return getLobeIcon(model?.icon || model?.vendor_icon || name, 18)
}

function ModelIconTile(props: { children: ReactNode }) {
  return (
    <span
      aria-hidden='true'
      className='bg-muted/50 flex size-8 shrink-0 items-center justify-center rounded-md'
    >
      {props.children}
    </span>
  )
}

type ModelSquareOrderDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

/** Lets the root admin choose the Recommended order of the model square. */
export function ModelSquareOrderDialog(props: ModelSquareOrderDialogProps) {
  return props.open ? <ModelSquareOrderEditor {...props} /> : null
}

function ModelSquareOrderEditor(props: ModelSquareOrderDialogProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const queryClient = useQueryClient()
  const optionsQuery = useQuery({
    queryKey: modelsQueryKeys.squareOrder(),
    queryFn: async ({ signal }) =>
      requireServerSuccess(await getModelSquareOrder(signal)).data,
    staleTime: 0,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: { errorToast: false },
  })
  const [draft, setDraft] = useState<Partial<ModelSquareOrder>>({})
  const [unconfirmed, setUnconfirmed] = useState<ModelSquareOrderPatch | null>(
    null
  )

  const saved = optionsQuery.data ?? DEFAULT_ORDER
  const order = { ...saved, ...draft }
  const changed = ORDER_KEYS.filter(
    (key) => JSON.stringify(order[key]) !== JSON.stringify(saved[key])
  )
  const canEdit =
    optionsQuery.isSuccess &&
    optionsQuery.isFetchedAfterMount &&
    optionsQuery.fetchStatus === 'idle' &&
    unconfirmed === null
  const mode = order[MODE_KEY]
  const metric = order[METRIC_KEY]
  const days = order[DAYS_KEY]
  const listed = order[MODELS_KEY]
  const usesUsage = mode !== 'manual'

  // Every square model, whichever group can see it, with its usage
  const usageQuery = useQuery({
    queryKey: modelsQueryKeys.squareUsage(days),
    queryFn: async () => requireServerSuccess(await getModelSquareUsage(days)),
    enabled: optionsQuery.isSuccess && optionsQuery.isFetchedAfterMount,
    placeholderData: keepPreviousData,
  })
  const squareModels = useMemo(
    () => usageQuery.data?.data ?? [],
    [usageQuery.data]
  )

  const modelByName = useMemo(
    () => new Map(squareModels.map((model) => [model.model_name, model])),
    [squareModels]
  )

  const ranked = useMemo(() => {
    const pinned = mode === 'mixed' ? listed : []
    return squareModels
      .filter((model) => !pinned.includes(model.model_name))
      .map((model) => ({
        model,
        name: model.model_name,
        usage: metric === 'requests' ? model.requests : model.tokens,
      }))
      .sort(compareModelUsage)
  }, [metric, mode, listed, squareModels])
  const hasUsage = ranked.some((row) => row.usage > 0)

  const update = (changes: Partial<ModelSquareOrder>) => {
    if (!canEdit || saveOrder.isPending) return
    setDraft((current) => ({ ...current, ...changes }))
  }

  const moveModel = (index: number, direction: 'up' | 'down') => {
    const target = direction === 'up' ? index - 1 : index + 1
    if (target < 0 || target >= listed.length) return
    const next = [...listed]
    ;[next[index], next[target]] = [next[target], next[index]]
    update({ [MODELS_KEY]: next })
  }

  const finishSave = () => {
    void queryClient.invalidateQueries({ queryKey: ['pricing'] })
    void queryClient.invalidateQueries({ queryKey: ['system-options'] })
    toast.success(t('Setting updated successfully'))
    props.onOpenChange(false)
  }

  const confirmSave = async (patch: ModelSquareOrderPatch, error?: unknown) => {
    // A failed HTTP response can mean the commit happened but its response
    // was lost. Read back the committed order, never replay the write.
    const result = await optionsQuery.refetch()
    if (!result.isSuccess || !result.data) {
      setUnconfirmed(patch)
      if (error) handleServerError(error, t('Failed to update setting'))
      return
    }

    setUnconfirmed(null)
    if (orderMatches(result.data, patch.changes)) {
      finishSave()
    } else if (!orderMatches(result.data, patch.expected)) {
      setDraft({})
      toast.error(
        t(
          'Could not load or save the settings. Reload them before retrying to avoid overwriting another administrator’s changes.'
        )
      )
    } else if (error) {
      handleServerError(error, t('Failed to update setting'))
    }
  }

  const saveOrder = useMutation({
    mutationFn: async (patch: ModelSquareOrderPatch) =>
      requireServerSuccess(await updateModelSquareOrder(patch)),
    retry: false,
    onSuccess: finishSave,
    onError: (error, patch) => confirmSave(patch, error),
  })

  const handleSave = () => {
    if (!canEdit || changed.length === 0 || saveOrder.isPending) return
    saveOrder.mutate({
      changes: Object.fromEntries(changed.map((key) => [key, order[key]])),
      expected: Object.fromEntries(changed.map((key) => [key, saved[key]])),
    })
  }

  const modes = [
    { value: 'manual', label: t('Manual order') },
    { value: 'usage', label: t('Most used') },
    { value: 'mixed', label: t('Pinned + most used') },
  ]
  const periods = [
    { value: '1', label: t('Last 1 day') },
    { value: '7', label: t('Last 7 days') },
    { value: '30', label: t('Last 30 days') },
  ]
  const metricLabel = metric === 'requests' ? t('Requests') : t('Tokens')
  const addLabel = mode === 'mixed' ? t('Add pinned model') : t('Add model')
  const candidates = squareModels
    .filter((model) => !listed.includes(model.model_name))
    .map((model) => ({
      value: model.model_name,
      label: model.model_name,
      description: model.vendor_name,
      icon: modelIcon(model.model_name, model),
    }))
  const previewRows = ranked.slice(0, PREVIEW_LIMIT)
  const hiddenCount = ranked.length - previewRows.length
  const rankOffset = mode === 'mixed' ? listed.length : 0

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!saveOrder.isPending) props.onOpenChange(open)
      }}
      title={t('Model square order')}
      description={t(
        'Choose the default order of the model square. Users can still pick another sort.'
      )}
      contentClassName='sm:max-w-xl'
      footer={
        <>
          <Button
            variant='outline'
            disabled={saveOrder.isPending}
            onClick={() => props.onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button
            onClick={handleSave}
            disabled={!canEdit || changed.length === 0 || saveOrder.isPending}
          >
            {t('Save')}
          </Button>
        </>
      }
    >
      {optionsQuery.isFetching && (
        <LoadingState inline size='sm' message={t('Loading settings...')} />
      )}
      {optionsQuery.isError && !optionsQuery.isFetching && (
        <ErrorState
          title={t('Failed to load settings')}
          onRetry={() => {
            if (unconfirmed) void confirmSave(unconfirmed)
            else void optionsQuery.refetch()
          }}
          className='min-h-0'
        />
      )}
      <FieldSet
        disabled={!canEdit || saveOrder.isPending}
        className='min-w-0 gap-5'
      >
        <div className='flex flex-col gap-2'>
          <div className='text-sm font-medium'>{t('Sort method')}</div>
          <ToggleGroup
            value={[mode]}
            onValueChange={(values) => {
              const next = values.find((value) => value !== mode)
              if (next) update({ [MODE_KEY]: next })
            }}
            aria-label={t('Sort method')}
            spacing={1}
            className={SEGMENTED_SWITCH_CLASS}
          >
            {modes.map((item) => (
              <ToggleGroupItem
                key={item.value}
                value={item.value}
                className={SEGMENTED_SWITCH_ITEM_CLASS}
              >
                {item.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </div>

        {usesUsage && (
          <div className='flex flex-wrap items-start gap-6'>
            <div className='flex flex-col gap-2'>
              <div className='text-sm font-medium'>{t('Rank by')}</div>
              <ToggleGroup
                value={[metric]}
                onValueChange={(values) => {
                  const next = values.find((value) => value !== metric)
                  if (next) update({ [METRIC_KEY]: next })
                }}
                variant='outline'
                size='sm'
                aria-label={t('Rank by')}
              >
                <ToggleGroupItem value='requests'>
                  {t('Requests')}
                </ToggleGroupItem>
                <ToggleGroupItem value='tokens'>{t('Tokens')}</ToggleGroupItem>
              </ToggleGroup>
            </div>
            <div className='flex flex-col gap-2'>
              <div className='text-sm font-medium'>{t('Period')}</div>
              <Select
                items={periods}
                value={String(days)}
                onValueChange={(value) => update({ [DAYS_KEY]: Number(value) })}
              >
                <SelectTrigger
                  size='sm'
                  className='w-36'
                  aria-label={t('Period')}
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent alignItemWithTrigger={false}>
                  <SelectGroup>
                    {periods.map((period) => (
                      <SelectItem key={period.value} value={period.value}>
                        {period.label}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
          </div>
        )}

        {mode !== 'usage' && (
          <div className='flex flex-col gap-2'>
            <div className='flex items-baseline justify-between gap-3'>
              <div className='text-sm font-medium'>
                {mode === 'mixed' ? t('Pinned models') : t('Model order')}
              </div>
              {listed.length > 1 && (
                <div className='text-muted-foreground text-xs'>
                  {t('Drag to reorder')}
                </div>
              )}
            </div>
            {listed.length > 0 && (
              <Reorder.Group
                axis='y'
                values={listed}
                onReorder={(next: string[]) => update({ [MODELS_KEY]: next })}
                className='flex flex-col gap-2'
              >
                {listed.map((name, index) => {
                  const model = modelByName.get(name)
                  return (
                    <AutoGroupOrderItem
                      key={name}
                      group={name}
                      index={index}
                      count={listed.length}
                      leading={
                        <ModelIconTile>{modelIcon(name, model)}</ModelIconTile>
                      }
                      onMove={moveModel}
                      onRemove={(removed) =>
                        update({
                          [MODELS_KEY]: listed.filter(
                            (item) => item !== removed
                          ),
                        })
                      }
                    >
                      {model?.vendor_name && (
                        <span className='text-muted-foreground text-xs'>
                          {model.vendor_name}
                        </span>
                      )}
                    </AutoGroupOrderItem>
                  )
                })}
              </Reorder.Group>
            )}
            <Combobox
              aria-label={addLabel}
              options={candidates}
              value=''
              onValueChange={(name) => {
                if (name && !listed.includes(name)) {
                  update({ [MODELS_KEY]: [...listed, name] })
                }
              }}
              placeholder={addLabel}
              className='sm:w-72'
            />
            {mode === 'manual' && (
              <p className='text-sm'>
                {t('Models not in the list follow by name.')}
              </p>
            )}
          </div>
        )}

        {usesUsage && usageQuery.isFetching && (
          <LoadingState inline size='sm' message={t('Loading...')} />
        )}
        {usesUsage && usageQuery.isSuccess && !usageQuery.isPlaceholderData && (
          <div className='flex flex-col gap-2'>
            <div className='flex items-baseline justify-between gap-3'>
              <div className='text-sm font-medium'>
                {mode === 'mixed' ? t('Other models') : t('Current order')}
              </div>
              {hasUsage && (
                <div className='text-muted-foreground text-xs'>
                  {metricLabel}
                </div>
              )}
            </div>
            {!hasUsage && (
              <p className='text-sm'>
                {t('No usage data, so models are ordered by name.')}
              </p>
            )}
            {hasUsage && (
              <ol
                aria-label={
                  mode === 'mixed' ? t('Other models') : t('Current order')
                }
                className='flex flex-col gap-2'
              >
                {previewRows.map((row, index) => (
                  <li
                    key={row.model.model_name}
                    className='flex min-w-0 items-center gap-2 rounded-lg border p-2'
                  >
                    <span className='text-muted-foreground w-7 shrink-0 text-center text-sm tabular-nums'>
                      {rankOffset + index + 1}
                    </span>
                    <ModelIconTile>
                      {modelIcon(row.model.model_name, row.model)}
                    </ModelIconTile>
                    <div className='flex min-w-0 flex-1 flex-wrap items-center gap-2'>
                      <span className='min-w-0 truncate text-sm font-medium'>
                        {row.model.model_name}
                      </span>
                      {row.model.vendor_name && (
                        <span className='text-muted-foreground text-xs'>
                          {row.model.vendor_name}
                        </span>
                      )}
                    </div>
                    <span className='shrink-0 pe-1 text-sm tabular-nums'>
                      {formatCompactNumber(row.usage, locale)}
                    </span>
                  </li>
                ))}
              </ol>
            )}
            {hasUsage && hiddenCount > 0 && (
              <p className='text-muted-foreground text-sm'>
                {t('And {{count}} more models', { count: hiddenCount })}
              </p>
            )}
          </div>
        )}
      </FieldSet>
    </Dialog>
  )
}
