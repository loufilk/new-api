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
import { useQuery } from '@tanstack/react-query'
import { Info } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useForm, type SubmitErrorHandler } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DateTimePicker } from '@/components/datetime-picker'
import {
  SideDrawerSection,
  SideDrawerSectionHeader,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
  sideDrawerSwitchItemClassName,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { useStatus } from '@/hooks/use-status'
import { getUserGroups } from '@/lib/api'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import { handleServerError } from '@/lib/handle-server-error'
import {
  requireServerSuccess,
  translateServerText,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  createApiKey,
  updateApiKey,
  getApiKey,
  getTokenAutoGroups,
  getApiKeyScopeModels,
} from '../api'
import { ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import {
  getApiKeyFormSchema,
  type ApiKeyFormValues,
  getApiKeyFormDefaultValues,
  transformFormDataToPayload,
  transformApiKeyToFormDefaults,
} from '../lib'
import type { ApiKey } from '../types'
import type { ApiKeyGroupOption } from './api-key-group-combobox'
import { ApiKeyRoutingFields } from './api-key-routing-fields'
import { useApiKeys } from './api-keys-provider'

type ApiKeyMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: ApiKey
  onCreated?: () => void
}

export function ApiKeysMutateDrawer({
  open,
  onOpenChange,
  currentRow,
  onCreated,
}: ApiKeyMutateDrawerProps) {
  const { t } = useTranslation()
  const isUpdate = !!currentRow
  const currentRowId = currentRow?.id
  const userId = useAuthStore((state) => state.auth.user?.id ?? null)
  const { triggerRefresh } = useApiKeys()
  const { status, loading: statusLoading } = useStatus()
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [initializedTarget, setInitializedTarget] = useState<string | null>(
    null
  )
  const defaultUseAutoGroup = status?.default_use_auto_group === true

  // Fetch groups
  const {
    data: groupsData,
    isFetched: groupsFetched,
    isFetching: groupsFetching,
    isError: groupsError,
    refetch: refetchGroups,
  } = useQuery({
    queryKey: ['user-groups', userId],
    queryFn: async () => requireServerSuccess(await getUserGroups()),
    enabled: open,
    staleTime: 0,
  })

  const {
    data: apiKeyData,
    isFetched: apiKeyFetched,
    isFetching: apiKeyFetching,
    isError: apiKeyError,
    refetch: refetchApiKey,
  } = useQuery({
    queryKey: ['api-key', userId, currentRowId],
    queryFn: async () =>
      requireServerSuccess(await getApiKey(currentRowId ?? 0)),
    enabled: open && isUpdate && currentRowId !== undefined,
    staleTime: 0,
  })

  const {
    data: autoGroupsData,
    isFetched: autoGroupsFetched,
    isFetching: autoGroupsFetching,
    isError: autoGroupsError,
    refetch: refetchAutoGroups,
  } = useQuery({
    queryKey: ['token-auto-groups', userId],
    queryFn: async () => requireServerSuccess(await getTokenAutoGroups()),
    enabled: open,
    staleTime: 0,
  })

  const groups = useMemo<ApiKeyGroupOption[]>(
    () =>
      Object.entries(groupsData?.data || {}).map(([key, info]) => ({
        value: key,
        label: key,
        desc: info.desc ? translateServerText(t, info.desc) : key,
        ratio: info.ratio,
      })),
    [groupsData, t]
  )
  const backendHasAuto = groups.some((g) => g.value === 'auto')
  const availableAutoGroupNames = useMemo(
    () => groups.filter((group) => group.value !== 'auto').map((g) => g.value),
    [groups]
  )
  const globalAutoGroups = useMemo(() => {
    const available = new Set(availableAutoGroupNames)
    return (autoGroupsData?.data?.groups || []).filter((group) =>
      available.has(group)
    )
  }, [autoGroupsData, availableAutoGroupNames])
  const manualGroupOptions = useMemo(() => {
    const selectable = autoGroupsData?.data?.selectable_groups
    const details = new Map(
      (autoGroupsData?.data?.group_details ?? []).map((detail) => [
        detail.name,
        detail,
      ])
    )
    return groups
      .filter(
        (group) =>
          group.value !== 'auto' &&
          (!selectable || selectable.includes(group.value))
      )
      .map((group) => {
        const detail = details.get(group.value)
        return {
          ...group,
          ratio: detail?.ratio ?? group.ratio,
          health:
            detail && autoGroupsData?.data?.metrics_available === false
              ? { ...detail, success_rate: null, history: [] }
              : detail,
        }
      })
  }, [autoGroupsData, groups])
  const maxAutoGroups =
    Number.isInteger(autoGroupsData?.data?.max_count) &&
    Number(autoGroupsData?.data?.max_count) > 0
      ? Number(autoGroupsData?.data?.max_count)
      : 5
  const schema = useMemo(
    () => getApiKeyFormSchema(t, maxAutoGroups),
    [t, maxAutoGroups]
  )

  const form = useForm<ApiKeyFormValues>({
    resolver: zodResolver(schema),
    defaultValues: getApiKeyFormDefaultValues(defaultUseAutoGroup),
  })

  // Load existing data when updating
  useEffect(() => {
    if (!open) {
      form.reset(
        getApiKeyFormDefaultValues(defaultUseAutoGroup && backendHasAuto)
      )
      setInitializedTarget(null)
      return
    }
    if (
      !groupsFetched ||
      groupsFetching ||
      !autoGroupsFetched ||
      autoGroupsFetching ||
      !groupsData?.success ||
      !autoGroupsData?.success
    ) {
      return
    }
    if (isUpdate && (!apiKeyFetched || apiKeyFetching)) return
    if (!isUpdate && statusLoading) return

    const target =
      isUpdate && currentRow
        ? `${userId}:update:${currentRow.id}`
        : `${userId}:create`
    if (initializedTarget === target) return
    if (isUpdate && currentRow) {
      if (apiKeyData?.success && apiKeyData.data) {
        form.reset(
          transformApiKeyToFormDefaults(
            apiKeyData.data,
            availableAutoGroupNames,
            maxAutoGroups
          )
        )
        setInitializedTarget(target)
      }
    } else {
      form.reset(
        getApiKeyFormDefaultValues(defaultUseAutoGroup && backendHasAuto)
      )
      setInitializedTarget(target)
    }
  }, [
    open,
    isUpdate,
    currentRow,
    form,
    defaultUseAutoGroup,
    statusLoading,
    backendHasAuto,
    groupsData?.success,
    autoGroupsData?.success,
    groupsFetched,
    groupsFetching,
    autoGroupsFetched,
    autoGroupsFetching,
    apiKeyData,
    apiKeyFetched,
    apiKeyFetching,
    availableAutoGroupNames,
    maxAutoGroups,
    initializedTarget,
    userId,
  ])

  const formTarget =
    isUpdate && currentRow
      ? `${userId}:update:${currentRow.id}`
      : `${userId}:create`
  const isFormInitialized = initializedTarget === formTarget
  const smartRouting = form.watch('smart_routing')
  const selectedGroup = form.watch('group')
  const autoGroupsMode = form.watch('auto_groups_mode')
  const selectedAutoGroups = form.watch('auto_groups')
  const selectedModelLimits = form.watch('model_limits')
  const scopeAutoGroups = useMemo(
    () =>
      selectedGroup === 'auto' && autoGroupsMode === 'custom'
        ? selectedAutoGroups
        : [],
    [selectedGroup, autoGroupsMode, selectedAutoGroups]
  )

  const {
    data: modelsData,
    isError: modelsError,
    isFetching: modelsFetching,
    refetch: refetchModels,
  } = useQuery({
    queryKey: [
      'api-key-scope-models',
      userId,
      smartRouting,
      selectedGroup || '',
      autoGroupsMode,
      scopeAutoGroups,
    ],
    queryFn: async () =>
      requireServerSuccess(
        await getApiKeyScopeModels({
          group: selectedGroup || '',
          ...(smartRouting ? { smart_routing: true } : {}),
          auto_groups_mode: autoGroupsMode,
          auto_groups: scopeAutoGroups,
        })
      ),
    enabled: open && isFormInitialized,
    staleTime: 0,
  })

  const models = useMemo(() => modelsData?.data ?? [], [modelsData?.data])
  const availableModelSet = useMemo(() => new Set(models), [models])
  const unavailableSelectedModels = useMemo(
    () =>
      modelsData?.success
        ? selectedModelLimits.filter(
            (modelName) => !availableModelSet.has(modelName)
          )
        : [],
    [availableModelSet, modelsData?.success, selectedModelLimits]
  )
  const modelOptions = useMemo(
    () => [
      ...models.map((modelName) => ({
        label: modelName,
        value: modelName,
      })),
      ...unavailableSelectedModels.map((modelName) => ({
        label: modelName,
        value: modelName,
        hint: t('Unavailable for current routing scope'),
      })),
    ],
    [models, t, unavailableSelectedModels]
  )

  const onSubmit = async (data: ApiKeyFormValues) => {
    setIsSubmitting(true)
    try {
      const basePayload = transformFormDataToPayload(data)

      if (isUpdate && currentRow) {
        const result = await updateApiKey({
          ...basePayload,
          id: currentRow.id,
        })
        if (result.success) {
          toast.success(t(SUCCESS_MESSAGES.API_KEY_UPDATED))
          onOpenChange(false)
          triggerRefresh()
        } else {
          handleServerError(result, t(ERROR_MESSAGES.UPDATE_FAILED))
        }
      } else {
        // Create mode - handle batch creation
        const count = data.tokenCount || 1
        let successCount = 0

        for (let i = 0; i < count; i++) {
          const result = await createApiKey({
            ...basePayload,
            name:
              i === 0 && data.name
                ? data.name
                : `${data.name || 'default'}-${Math.random().toString(36).slice(2, 8)}`,
          })
          if (result.success) {
            successCount++
          } else {
            handleServerError(result, t(ERROR_MESSAGES.CREATE_FAILED))
            break
          }
        }

        if (successCount > 0) {
          onCreated?.()
          toast.success(
            t('Successfully created {{count}} API Key(s)', {
              count: successCount,
            })
          )
          onOpenChange(false)
          triggerRefresh()
        }
      }
    } catch (error) {
      handleServerError(error, t(ERROR_MESSAGES.UNEXPECTED))
    } finally {
      setIsSubmitting(false)
    }
  }

  const onInvalid: SubmitErrorHandler<ApiKeyFormValues> = () => {
    toast.error(t('Please fix the highlighted fields before saving'))
  }

  const handleSetExpiry = (months: number, days: number, hours: number) => {
    if (months === 0 && days === 0 && hours === 0) {
      form.setValue('expired_time', null)
      return
    }

    const now = new Date()
    now.setMonth(now.getMonth() + months)
    now.setDate(now.getDate() + days)
    now.setHours(now.getHours() + hours)

    form.setValue('expired_time', now)
  }

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const quotaLabel = t('Quota ({{currency}})', { currency: currencyLabel })
  const quotaPlaceholder = tokensOnly
    ? t('Enter quota in tokens')
    : t('Enter quota in {{currency}}', { currency: currencyLabel })
  const unlimitedQuota = form.watch('unlimited_quota')
  const submitLabel = isUpdate ? t('Save changes') : t('Add')

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        className={sideDrawerContentClassName(
          'min-w-0 max-w-[100dvw] sm:!max-w-[516px]'
        )}
      >
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isUpdate ? t('Update API Key') : t('Create API Key')}
          </SheetTitle>
          <SheetDescription>
            {isUpdate
              ? t('Update the API key by providing necessary info.')
              : t('Add a new API key by providing necessary info.')}
          </SheetDescription>
        </SheetHeader>
        {(groupsError || autoGroupsError || apiKeyError) && (
          <ErrorState
            title={t('Failed to load routing groups')}
            description={t(
              'Routing settings are unavailable. Retry before saving this key.'
            )}
            onRetry={() => {
              void refetchGroups()
              void refetchAutoGroups()
              if (isUpdate) void refetchApiKey()
            }}
          />
        )}
        <Form {...form}>
          <form
            id='api-key-form'
            onSubmit={form.handleSubmit(onSubmit, onInvalid)}
            aria-busy={!isFormInitialized}
            inert={!isFormInitialized || isSubmitting ? true : undefined}
            className={sideDrawerFormClassName(
              'min-w-0 gap-0 px-5 py-6 sm:px-8 sm:py-6'
            )}
          >
            <SideDrawerSection className='before:bg-border relative ml-1 gap-6 border-b-0 border-l pb-9 pl-7 before:absolute before:top-0 before:-left-[5px] before:size-2.5 before:rounded-full'>
              <SideDrawerSectionHeader
                title={t('Basic Information')}
                description={t('Set API key basic information')}
              />
              <FormField
                control={form.control}
                name='name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('Token Name')}{' '}
                      <Info className='size-3.5' aria-hidden='true' />
                    </FormLabel>
                    <FormControl>
                      <Input {...field} placeholder={t('Enter token name')} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <ApiKeyRoutingFields
                options={manualGroupOptions}
                globalGroups={globalAutoGroups}
                maxCount={maxAutoGroups}
                allowAuto={backendHasAuto}
                disabled={isSubmitting}
              />

              <FormField
                control={form.control}
                name='expired_time'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Expiration Time')}</FormLabel>
                    <div className='grid gap-2'>
                      <FormControl>
                        <DateTimePicker
                          value={field.value ?? undefined}
                          onChange={(date) => field.onChange(date ?? null)}
                          placeholder={t('Never expires')}
                          className='min-w-0 [&_input[type=time]]:w-24 sm:[&_input[type=time]]:w-32'
                        />
                      </FormControl>
                      <div className='grid grid-cols-4 gap-2 sm:flex'>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          className='px-2 text-xs sm:px-3 sm:text-sm'
                          onClick={() => handleSetExpiry(0, 0, 0)}
                        >
                          {t('Never')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          className='px-2 text-xs sm:px-3 sm:text-sm'
                          onClick={() => handleSetExpiry(1, 0, 0)}
                        >
                          {t('1 Month')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          className='px-2 text-xs sm:px-3 sm:text-sm'
                          onClick={() => handleSetExpiry(0, 1, 0)}
                        >
                          {t('1 Day')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          className='px-2 text-xs sm:px-3 sm:text-sm'
                          onClick={() => handleSetExpiry(0, 0, 1)}
                        >
                          {t('1 Hour')}
                        </Button>
                      </div>
                    </div>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {!isUpdate && (
                <FormField
                  control={form.control}
                  name='tokenCount'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Quantity')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min='1'
                          placeholder={t('Number of keys to create')}
                          onChange={(e) =>
                            field.onChange(
                              Number.parseInt(e.target.value, 10) || 1
                            )
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Create multiple API keys at once (random suffix will be added to names)'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}
            </SideDrawerSection>

            <SideDrawerSection className='before:bg-border relative ml-1 gap-6 border-b-0 border-l pb-9 pl-7 before:absolute before:top-0 before:-left-[5px] before:size-2.5 before:rounded-full'>
              <SideDrawerSectionHeader
                title={t('Quota Settings')}
                description={t('Set quota amount and limits')}
              />
              <div className='grid grid-cols-[auto_minmax(0,1fr)] items-end gap-3'>
                <FormField
                  control={form.control}
                  name='unlimited_quota'
                  render={({ field }) => (
                    <FormItem className='flex items-center gap-3 pb-2'>
                      <FormLabel className='text-xs'>
                        {t('Unlimited Quota')}
                      </FormLabel>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                          className='data-checked:bg-orange-500'
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name='remain_quota_dollars'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel className='text-xs'>{quotaLabel}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min='0'
                          step={tokensOnly ? 1 : 0.01}
                          disabled={unlimitedQuota}
                          placeholder={quotaPlaceholder}
                          onChange={(event) =>
                            field.onChange(
                              Number.parseFloat(event.target.value) || 0
                            )
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>
            </SideDrawerSection>

            <SideDrawerSection className='before:bg-border relative ml-1 gap-6 border-b-0 border-l pb-9 pl-7 before:absolute before:top-0 before:-left-[5px] before:size-2.5 before:rounded-full'>
              <SideDrawerSectionHeader
                title={t('Access Restrictions')}
                description={t('Set API key access restrictions')}
              />
              <div className='flex flex-col gap-5'>
                <FormField
                  control={form.control}
                  name='model_limits'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Model allowlist')}</FormLabel>
                      <FormControl>
                        <MultiSelect
                          options={modelOptions}
                          disabled={modelsFetching || modelsError}
                          selected={field.value}
                          onChange={field.onChange}
                          placeholder={t('Select models (empty for allow all)')}
                        />
                      </FormControl>
                      <FormDescription>
                        {unavailableSelectedModels.length > 0
                          ? t(
                              '{{count}} selected model(s) are unavailable for the current routing scope. They remain in the whitelist until you remove or replace them.',
                              {
                                count: unavailableSelectedModels.length,
                              }
                            )
                          : t('Limit which models can be used with this key')}
                      </FormDescription>
                      {modelsFetching && (
                        <FormDescription role='status'>
                          {t('Loading models...')}
                        </FormDescription>
                      )}
                      {modelsError && (
                        <div
                          role='alert'
                          className='text-destructive flex items-center justify-between gap-2 text-xs'
                        >
                          <span>{t('Failed to load models')}</span>
                          <Button
                            type='button'
                            variant='outline'
                            size='sm'
                            onClick={() => void refetchModels()}
                          >
                            {t('Retry')}
                          </Button>
                        </div>
                      )}
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='allow_ips'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('IP Whitelist (supports CIDR)')}</FormLabel>
                      <FormControl>
                        <Textarea
                          {...field}
                          className='min-h-20 resize-none'
                          placeholder={t(
                            'One IP per line (empty for no restriction)'
                          )}
                          rows={3}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Do not over-trust this feature. IP may be spoofed. Please use with nginx, CDN and other gateways.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='allow_user_quota_query'
                  render={({ field }) => (
                    <FormItem className={sideDrawerSwitchItemClassName()}>
                      <div className='flex flex-col gap-0.5'>
                        <FormLabel className='text-sm'>
                          {t('Allow querying user quota')}
                        </FormLabel>
                        <FormDescription className='text-xs'>
                          {t(
                            "When enabled, this API key can be used to check your account's remaining and used quota."
                          )}
                        </FormDescription>
                      </div>
                      <FormControl>
                        <Switch
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                    </FormItem>
                  )}
                />
              </div>
            </SideDrawerSection>
          </form>
        </Form>
        <SheetFooter
          className={sideDrawerFooterClassName('sm:flex-row-reverse')}
        >
          <SheetClose
            render={
              <Button
                variant='outline'
                className='order-2 w-full min-w-0 sm:order-none sm:w-auto'
              />
            }
          >
            {t('Close')}
          </SheetClose>
          <Button
            type='button'
            onClick={form.handleSubmit(onSubmit, onInvalid)}
            disabled={!isFormInitialized || isSubmitting}
            className='order-1 w-full min-w-0 sm:order-none sm:w-auto'
          >
            {isSubmitting ? t('Saving...') : submitLabel}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
