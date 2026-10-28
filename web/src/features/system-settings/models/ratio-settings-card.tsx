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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  buildPricingChanges,
  useModelPricing,
  useSaveModelPricing,
  type ModelPricingConfig,
} from '@/features/model-pricing/api'
import { pricingOptions } from '@/features/model-pricing/pricing'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorSources } from '@/lib/server-error-message'

import { renameSystemGroups } from '../api'
import { SettingsPageTitleStatusPortal } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import type { GroupRenameMapping } from '../types'
import { positiveIntegerSchema } from '../utils/numeric-field'
import { GroupRatioForm } from './group-ratio-form'
import type {
  GroupRatioSaveOutcome,
  GroupRenameEditableValues,
} from './group-rename'
import { ModelRatioForm } from './model-ratio-form'
import { ToolPriceSettings } from './tool-price-settings'
import { UpstreamRatioSync } from './upstream-ratio-sync'
import {
  formatJsonForTextarea,
  type JsonValidationError,
  normalizeJsonString,
  validateJsonString,
} from './utils'

type Translate = (key: string, options?: Record<string, unknown>) => string

function formatJsonValidationError(
  t: Translate,
  error?: JsonValidationError,
  fallback = 'Invalid JSON'
) {
  if (!error) return t(fallback)

  if (error.type === 'required') return t('Value is required')
  if (error.type === 'structure') {
    return t(
      fallback === 'Invalid JSON' ? 'JSON structure is invalid' : fallback
    )
  }

  let locationMessage: string
  if (error.line && error.column) {
    locationMessage = t(
      'JSON is invalid at line {{line}}, column {{column}}.',
      {
        line: error.line,
        column: error.column,
      }
    )
  } else if (error.position !== undefined) {
    locationMessage = t('JSON is invalid at position {{position}}.', {
      position: error.position,
    })
  } else {
    locationMessage = t('JSON is invalid. Please check the syntax.')
  }

  const parts = [locationMessage]

  if (error.missingCommaLine) {
    parts.push(
      t('Check line {{line}} for a missing comma.', {
        line: error.missingCommaLine,
      })
    )
  }

  return parts.join(' ')
}

function createJsonStringField(
  t: Translate,
  options?: Parameters<typeof validateJsonString>[1]
) {
  return z.string().superRefine((value, ctx) => {
    const result = validateJsonString(value, options)
    if (!result.valid) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: formatJsonValidationError(t, result.error, result.message),
      })
    }
  })
}

const createModelSchema = (t: Translate) =>
  z.object({
    ModelPrice: createJsonStringField(t),
    ModelRatio: createJsonStringField(t),
    CacheRatio: createJsonStringField(t),
    CreateCacheRatio: createJsonStringField(t),
    CompletionRatio: createJsonStringField(t),
    ImageRatio: createJsonStringField(t),
    AudioRatio: createJsonStringField(t),
    AudioCompletionRatio: createJsonStringField(t),
    ExposeRatioEnabled: z.boolean(),
    BillingMode: createJsonStringField(t),
    BillingExpr: createJsonStringField(t),
    PluginBillingExpr: createJsonStringField(t),
  })

const createGroupSchema = (t: Translate) =>
  z.object({
    GroupRatio: createJsonStringField(t),
    TopupGroupRatio: createJsonStringField(t),
    UserUsableGroups: createJsonStringField(t),
    GroupGroupRatio: createJsonStringField(t),
    AutoGroups: createJsonStringField(t, {
      predicate: (parsed) =>
        Array.isArray(parsed) &&
        parsed.every((item) => typeof item === 'string'),
      predicateMessage: 'Expected a JSON array of group identifiers',
    }),
    MaxTokenAutoGroups: positiveIntegerSchema(t('Enter a positive integer')),
    DefaultUseAutoGroup: z.boolean(),
    GroupSpecialUsableGroup: createJsonStringField(t),
  })

type ModelFormValues = z.infer<ReturnType<typeof createModelSchema>>
type GroupFormValues = z.infer<ReturnType<typeof createGroupSchema>>

// Canonical string form of the group settings the editor was loaded from. It is
// the comparison base for stale-edit detection and is refreshed after a
// successful save so later saves do not depend on a refetch completing first.
type GroupRenameBaseline = {
  GroupRatio: string
  TopupGroupRatio: string
  UserUsableGroups: string
  GroupGroupRatio: string
  AutoGroups: string
  GroupSpecialUsableGroup: string
  MaxTokenAutoGroups: string
  DefaultUseAutoGroup: string
}

const GROUP_RENAME_STALE_CODE = 'GROUP_RENAME_STALE'

function groupRenameBaselineFromDefaults(
  defaults: GroupFormValues
): GroupRenameBaseline {
  return {
    GroupRatio: defaults.GroupRatio,
    TopupGroupRatio: defaults.TopupGroupRatio,
    UserUsableGroups: defaults.UserUsableGroups,
    GroupGroupRatio: defaults.GroupGroupRatio,
    AutoGroups: defaults.AutoGroups,
    GroupSpecialUsableGroup: defaults.GroupSpecialUsableGroup,
    MaxTokenAutoGroups: String(defaults.MaxTokenAutoGroups),
    DefaultUseAutoGroup: defaults.DefaultUseAutoGroup ? 'true' : 'false',
  }
}

function isGroupRenameStale(value: unknown): boolean {
  return getServerErrorSources(value).some(
    (source) => source.code === GROUP_RENAME_STALE_CODE
  )
}

type RatioTabId =
  | 'models'
  | 'unset-models'
  | 'groups'
  | 'tool-prices'
  | 'upstream-sync'

type RatioSettingsCardProps = {
  modelDefaults: ModelFormValues
  groupDefaults: GroupFormValues
  toolPricesDefault: string
  titleKey?: string
  visibleTabs?: RatioTabId[]
}

export function RatioSettingsCard({
  modelDefaults: initialModelDefaults,
  groupDefaults,
  toolPricesDefault,
  titleKey = 'Pricing Ratios',
  visibleTabs = ['models', 'groups', 'tool-prices', 'upstream-sync'],
}: RatioSettingsCardProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const queryClient = useQueryClient()
  const [confirmOpen, setConfirmOpen] = useState(false)

  const pricingQuery = useModelPricing()
  const savePricing = useSaveModelPricing()
  const [pricingBaseline, setPricingBaseline] =
    useState<ModelPricingConfig | null>(null)
  useEffect(() => {
    if (!pricingBaseline && pricingQuery.data) {
      setPricingBaseline(pricingQuery.data)
    }
  }, [pricingBaseline, pricingQuery.data])
  const modelDefaults = useMemo(
    () =>
      pricingBaseline
        ? {
            ...initialModelDefaults,
            ...pricingBaseline.options,
            BillingMode:
              pricingBaseline.options['billing_setting.billing_mode'],
            BillingExpr:
              pricingBaseline.options['billing_setting.billing_expr'],
            PluginBillingExpr:
              pricingBaseline.options['billing_setting.plugin_billing_expr'],
          }
        : initialModelDefaults,
    [initialModelDefaults, pricingBaseline]
  )
  const resetMutation = useMutation({
    mutationFn: async () => {
      if (!pricingBaseline) return
      await savePricing.mutateAsync(
        pricingBaseline.entries.map((entry) => ({
          model_name: entry.model_name,
          expected_version: entry.version,
          pricing: {},
          reset: true,
        }))
      )
      const refreshed = await pricingQuery.refetch()
      setPricingBaseline(refreshed.data ?? null)
    },
    onSuccess: () => {
      toast.success(t('Model prices reset successfully'))
      setConfirmOpen(false)
    },
    onError: (error) => handleServerError(error),
  })

  const modelNormalizedDefaults = useRef({
    ModelPrice: normalizeJsonString(modelDefaults.ModelPrice),
    ModelRatio: normalizeJsonString(modelDefaults.ModelRatio),
    CacheRatio: normalizeJsonString(modelDefaults.CacheRatio),
    CreateCacheRatio: normalizeJsonString(modelDefaults.CreateCacheRatio),
    CompletionRatio: normalizeJsonString(modelDefaults.CompletionRatio),
    ImageRatio: normalizeJsonString(modelDefaults.ImageRatio),
    AudioRatio: normalizeJsonString(modelDefaults.AudioRatio),
    AudioCompletionRatio: normalizeJsonString(
      modelDefaults.AudioCompletionRatio
    ),
    ExposeRatioEnabled: modelDefaults.ExposeRatioEnabled,
    BillingMode: normalizeJsonString(modelDefaults.BillingMode),
    BillingExpr: normalizeJsonString(modelDefaults.BillingExpr),
    PluginBillingExpr: normalizeJsonString(modelDefaults.PluginBillingExpr),
  })
  const [savedModelValues, setSavedModelValues] = useState(
    modelNormalizedDefaults.current
  )

  const [groupBaseline, setGroupBaseline] = useState<GroupRenameBaseline>(() =>
    groupRenameBaselineFromDefaults(groupDefaults)
  )
  const modelSchema = useMemo(() => createModelSchema(t), [t])
  const groupSchema = useMemo(() => createGroupSchema(t), [t])
  const groupRenameBaseline = useMemo<GroupRenameEditableValues>(
    () => ({
      GroupRatio: groupBaseline.GroupRatio,
      TopupGroupRatio: groupBaseline.TopupGroupRatio,
      UserUsableGroups: groupBaseline.UserUsableGroups,
      GroupGroupRatio: groupBaseline.GroupGroupRatio,
      AutoGroups: groupBaseline.AutoGroups,
      GroupSpecialUsableGroup: groupBaseline.GroupSpecialUsableGroup,
    }),
    [groupBaseline]
  )

  const modelForm = useForm<ModelFormValues>({
    resolver: zodResolver(modelSchema),
    mode: 'onChange',
    defaultValues: {
      ...modelDefaults,
      ModelPrice: formatJsonForTextarea(modelDefaults.ModelPrice),
      ModelRatio: formatJsonForTextarea(modelDefaults.ModelRatio),
      CacheRatio: formatJsonForTextarea(modelDefaults.CacheRatio),
      CreateCacheRatio: formatJsonForTextarea(modelDefaults.CreateCacheRatio),
      CompletionRatio: formatJsonForTextarea(modelDefaults.CompletionRatio),
      ImageRatio: formatJsonForTextarea(modelDefaults.ImageRatio),
      AudioRatio: formatJsonForTextarea(modelDefaults.AudioRatio),
      AudioCompletionRatio: formatJsonForTextarea(
        modelDefaults.AudioCompletionRatio
      ),
      BillingMode: formatJsonForTextarea(modelDefaults.BillingMode),
      BillingExpr: formatJsonForTextarea(modelDefaults.BillingExpr),
      PluginBillingExpr: formatJsonForTextarea(modelDefaults.PluginBillingExpr),
    },
  })

  const groupForm = useForm<GroupFormValues>({
    resolver: zodResolver(groupSchema),
    mode: 'onChange',
    defaultValues: {
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
    },
  })

  useEffect(() => {
    modelNormalizedDefaults.current = {
      ModelPrice: normalizeJsonString(modelDefaults.ModelPrice),
      ModelRatio: normalizeJsonString(modelDefaults.ModelRatio),
      CacheRatio: normalizeJsonString(modelDefaults.CacheRatio),
      CreateCacheRatio: normalizeJsonString(modelDefaults.CreateCacheRatio),
      CompletionRatio: normalizeJsonString(modelDefaults.CompletionRatio),
      ImageRatio: normalizeJsonString(modelDefaults.ImageRatio),
      AudioRatio: normalizeJsonString(modelDefaults.AudioRatio),
      AudioCompletionRatio: normalizeJsonString(
        modelDefaults.AudioCompletionRatio
      ),
      ExposeRatioEnabled: modelDefaults.ExposeRatioEnabled,
      BillingMode: normalizeJsonString(modelDefaults.BillingMode),
      BillingExpr: normalizeJsonString(modelDefaults.BillingExpr),
      PluginBillingExpr: normalizeJsonString(modelDefaults.PluginBillingExpr),
    }
    setSavedModelValues(modelNormalizedDefaults.current)

    modelForm.reset({
      ...modelDefaults,
      ModelPrice: formatJsonForTextarea(modelDefaults.ModelPrice),
      ModelRatio: formatJsonForTextarea(modelDefaults.ModelRatio),
      CacheRatio: formatJsonForTextarea(modelDefaults.CacheRatio),
      CreateCacheRatio: formatJsonForTextarea(modelDefaults.CreateCacheRatio),
      CompletionRatio: formatJsonForTextarea(modelDefaults.CompletionRatio),
      ImageRatio: formatJsonForTextarea(modelDefaults.ImageRatio),
      AudioRatio: formatJsonForTextarea(modelDefaults.AudioRatio),
      AudioCompletionRatio: formatJsonForTextarea(
        modelDefaults.AudioCompletionRatio
      ),
      BillingMode: formatJsonForTextarea(modelDefaults.BillingMode),
      BillingExpr: formatJsonForTextarea(modelDefaults.BillingExpr),
      PluginBillingExpr: formatJsonForTextarea(modelDefaults.PluginBillingExpr),
    })
  }, [modelDefaults, modelForm])

  useEffect(() => {
    setGroupBaseline(groupRenameBaselineFromDefaults(groupDefaults))

    groupForm.reset({
      ...groupDefaults,
      GroupRatio: formatJsonForTextarea(groupDefaults.GroupRatio),
      TopupGroupRatio: formatJsonForTextarea(groupDefaults.TopupGroupRatio),
      UserUsableGroups: formatJsonForTextarea(groupDefaults.UserUsableGroups),
      GroupGroupRatio: formatJsonForTextarea(groupDefaults.GroupGroupRatio),
      AutoGroups: formatJsonForTextarea(groupDefaults.AutoGroups),
      GroupSpecialUsableGroup: formatJsonForTextarea(
        groupDefaults.GroupSpecialUsableGroup
      ),
    })
  }, [groupDefaults, groupForm])

  const saveModelRatios = useCallback(
    async (values: ModelFormValues) => {
      const normalized = {
        ModelPrice: normalizeJsonString(values.ModelPrice),
        ModelRatio: normalizeJsonString(values.ModelRatio),
        CacheRatio: normalizeJsonString(values.CacheRatio),
        CreateCacheRatio: normalizeJsonString(values.CreateCacheRatio),
        CompletionRatio: normalizeJsonString(values.CompletionRatio),
        ImageRatio: normalizeJsonString(values.ImageRatio),
        AudioRatio: normalizeJsonString(values.AudioRatio),
        AudioCompletionRatio: normalizeJsonString(values.AudioCompletionRatio),
        ExposeRatioEnabled: values.ExposeRatioEnabled,
        BillingMode: normalizeJsonString(values.BillingMode),
        BillingExpr: normalizeJsonString(values.BillingExpr),
        PluginBillingExpr: normalizeJsonString(values.PluginBillingExpr),
      }

      if (!pricingBaseline) return
      try {
        const changes = buildPricingChanges(
          pricingBaseline,
          pricingOptions(modelNormalizedDefaults.current),
          pricingOptions(normalized)
        )
        const visibilityChanged =
          normalized.ExposeRatioEnabled !==
          modelNormalizedDefaults.current.ExposeRatioEnabled
        if (!changes.length && !visibilityChanged) {
          toast.info(t('No model price changes to save'))
          return
        }
        await savePricing.mutateAsync(changes)
        if (visibilityChanged) {
          await updateOption.mutateAsync({
            key: 'ExposeRatioEnabled',
            value: normalized.ExposeRatioEnabled,
          })
        }
        const refreshed = await pricingQuery.refetch()
        setPricingBaseline(refreshed.data ?? null)
        modelNormalizedDefaults.current = normalized
        setSavedModelValues(normalized)
        toast.success(t('Model pricing saved'))
      } catch (error) {
        handleServerError(error)
      }
    },
    [t, updateOption, pricingBaseline, savePricing, pricingQuery]
  )

  const saveGroupRatios = useCallback(
    async (
      values: GroupFormValues,
      renames: GroupRenameMapping[]
    ): Promise<GroupRatioSaveOutcome> => {
      const normalized: Record<string, string> = {
        GroupRatio: normalizeJsonString(values.GroupRatio),
        TopupGroupRatio: normalizeJsonString(values.TopupGroupRatio),
        UserUsableGroups: normalizeJsonString(values.UserUsableGroups),
        GroupGroupRatio: normalizeJsonString(values.GroupGroupRatio),
        AutoGroups: normalizeJsonString(values.AutoGroups),
        MaxTokenAutoGroups: String(values.MaxTokenAutoGroups),
        DefaultUseAutoGroup: values.DefaultUseAutoGroup ? 'true' : 'false',
        GroupSpecialUsableGroup: normalizeJsonString(
          values.GroupSpecialUsableGroup
        ),
      }
      const baseline: Record<string, string> = { ...groupBaseline }
      // Map form field names to API keys (most are 1:1, except the nested
      // special-usable-group option).
      const apiKeyMap: Record<string, string> = {
        GroupSpecialUsableGroup:
          'group_ratio_setting.group_special_usable_group',
      }

      // Every edited option is saved in the same transaction as the renames,
      // and Expected pins the values these edits were based on so a concurrent
      // write cannot be silently overwritten.
      const options: Record<string, string> = {}
      const expected: Record<string, string> = {}
      for (const [field, value] of Object.entries(normalized)) {
        const current = baseline[field] ?? ''
        if (value === current) continue
        const apiKey = apiKeyMap[field] ?? field
        options[apiKey] = value
        expected[apiKey] = current
      }

      if (renames.length === 0 && Object.keys(options).length === 0) {
        toast.info(t('No changes to save'))
        return 'saved'
      }

      // The stale conflict arrives as HTTP 409, which Axios rejects. Keep the
      // unsaved edits and pending mappings: do not refetch (that would reset
      // the form) and report a specific, translated conflict message.
      const reportStaleConflict = () =>
        toast.error(
          t(
            'The group settings changed on the server. Reload the page and apply your edits again.'
          )
        )

      try {
        const response = await renameSystemGroups({
          renames,
          options,
          expected,
        })
        if (!response.success) {
          if (isGroupRenameStale(response)) {
            reportStaleConflict()
            return 'failed'
          }
          handleServerError(response, t('Failed to update setting'))
          return 'failed'
        }
        const applied = response.data?.applied_options ?? {}
        const nextBaseline: GroupRenameBaseline = { ...groupBaseline }
        for (const [field, value] of Object.entries(normalized)) {
          const apiKey = apiKeyMap[field] ?? field
          nextBaseline[field as keyof GroupRenameBaseline] =
            applied[apiKey] ?? options[apiKey] ?? value
        }
        setGroupBaseline(nextBaseline)
        await queryClient.invalidateQueries({ queryKey: ['system-options'] })
        toast.success(t('Setting updated successfully'))
        return 'saved'
      } catch (error) {
        if (isGroupRenameStale(error)) {
          reportStaleConflict()
          return 'failed'
        }
        handleServerError(error, t('Failed to update setting'))
        return 'failed'
      }
    },
    [queryClient, t, groupBaseline]
  )

  const handleResetRatios = useCallback(() => {
    setConfirmOpen(true)
  }, [])

  const { mutate: resetMutate } = resetMutation
  const handleConfirmReset = useCallback(() => {
    resetMutate()
  }, [resetMutate])

  const tabLabels: Record<RatioTabId, string> = {
    models: 'Model prices',
    'unset-models': 'Unset price models',
    groups: 'Group ratios',
    'tool-prices': 'Tool prices',
    'upstream-sync': 'Upstream price sync',
  }
  const tabsGridClass =
    {
      1: 'grid-cols-1',
      2: 'grid-cols-2',
      3: 'grid-cols-3',
      4: 'grid-cols-4',
      5: 'grid-cols-5',
    }[visibleTabs.length] ?? 'grid-cols-4'
  const defaultTab = visibleTabs[0] ?? 'models'

  const renderTabContent = (tab: RatioTabId) => {
    if (tab === 'models' || tab === 'unset-models') {
      if (pricingQuery.isError) {
        return (
          <ErrorState
            description={pricingQuery.error.message}
            onRetry={() => void pricingQuery.refetch()}
          />
        )
      }
      if (!pricingBaseline) return <LoadingState />
      return (
        <>
          {savePricing.isError && (
            <Button
              variant='outline'
              size='sm'
              onClick={async () => {
                const refreshed = await pricingQuery.refetch()
                if (refreshed.data) setPricingBaseline(refreshed.data)
                savePricing.reset()
              }}
            >
              {t('Reload pricing')}
            </Button>
          )}
          <ModelRatioForm
            form={modelForm}
            savedValues={savedModelValues}
            onSave={saveModelRatios}
            onReset={handleResetRatios}
            isSaving={updateOption.isPending || savePricing.isPending}
            isResetting={resetMutation.isPending}
            variant={tab === 'unset-models' ? 'unset' : 'default'}
          />
        </>
      )
    }
    if (tab === 'groups') {
      return (
        <GroupRatioForm
          form={groupForm}
          baseline={groupRenameBaseline}
          onSave={saveGroupRatios}
          isSaving={updateOption.isPending}
        />
      )
    }
    if (tab === 'tool-prices') {
      return <ToolPriceSettings defaultValue={toolPricesDefault} />
    }
    return <UpstreamRatioSync />
  }

  const renderTabSwitcher = () => (
    <TabsList className={`grid w-fit max-w-full ${tabsGridClass}`}>
      {visibleTabs.map((tab) => (
        <TabsTrigger key={tab} value={tab}>
          {t(tabLabels[tab])}
        </TabsTrigger>
      ))}
    </TabsList>
  )

  return (
    <>
      {visibleTabs.length === 1 ? (
        <SettingsSection
          title={t(titleKey)}
          className={
            defaultTab === 'models' || defaultTab === 'unset-models'
              ? 'min-h-0 flex-1'
              : undefined
          }
        >
          {renderTabContent(defaultTab)}
        </SettingsSection>
      ) : (
        <Tabs defaultValue={defaultTab} className='h-full min-h-0 gap-6'>
          <SettingsPageTitleStatusPortal>
            {renderTabSwitcher()}
          </SettingsPageTitleStatusPortal>

          <SettingsSection title={t(titleKey)} className='min-h-0 flex-1'>
            {visibleTabs.map((tab) => (
              <TabsContent
                key={tab}
                value={tab}
                className={
                  tab === 'models' || tab === 'unset-models'
                    ? 'flex min-h-0 flex-col data-hidden:hidden'
                    : 'min-h-0'
                }
              >
                {renderTabContent(tab)}
              </TabsContent>
            ))}
          </SettingsSection>
        </Tabs>
      )}

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t('Reset all model prices?')}
        desc={t(
          'This will clear custom pricing ratios and revert to upstream defaults.'
        )}
        destructive
        isLoading={resetMutation.isPending}
        handleConfirm={handleConfirmReset}
        confirmText={t('Reset')}
      />
    </>
  )
}
