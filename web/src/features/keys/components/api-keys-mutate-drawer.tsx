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
import { isAxiosError } from 'axios'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { groupPlatform } from '@/features/pricing/lib/market-groups'
import { getUserGroups } from '@/lib/api'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { getCurrencyDisplay } from '@/lib/currency'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import {
  createApiKey,
  getApiKey,
  getApiKeyScopeModels,
  getSavedApiKeyModels,
  updateApiKey,
} from '../api'
import { ERROR_MESSAGES, SUCCESS_MESSAGES } from '../constants'
import type { MarketKeyDraft } from '../lib/market-key-draft'
import {
  createFixedKeyPayload,
  expirySeconds,
  initialKeyValues,
  isLegacyApiKey,
  parseKeyIpAllowlist,
  quotaUnits,
  type SingleGroupKeyValues,
  updateFixedKeyPayload,
} from '../lib/single-group-form'
import type { ApiKey } from '../types'
import { useApiKeys } from './api-keys-provider'
import { KeyGroupCards, type KeyGroupCard } from './key-group-cards'

type ApiKeyMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: ApiKey
  onCreated?: () => void
  draft?: MarketKeyDraft
}

const nowSeconds = () => Math.floor(Date.now() / 1000)

export function ApiKeysMutateDrawer({
  open,
  onOpenChange,
  currentRow,
  onCreated,
  draft,
}: ApiKeyMutateDrawerProps) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id ?? null)
  const groups = useQuery({
    queryKey: ['user-groups', userId],
    queryFn: async () => requireServerSuccess(await getUserGroups()),
    enabled: open,
    staleTime: 0,
    refetchOnWindowFocus: false,
  })
  const currentId = currentRow?.id
  const detail = useQuery({
    queryKey: ['api-key', userId, currentId],
    queryFn: async () => requireServerSuccess(await getApiKey(currentId!)),
    enabled: open && currentId !== undefined,
    staleTime: 0,
    refetchOnWindowFocus: false,
  })
  const [generation, setGeneration] = useState(0)
  const groupCards = useMemo(
    () =>
      Object.entries(groups.data?.data ?? {})
        .filter(([id]) => id && id !== 'auto')
        .map(([id, info]): KeyGroupCard => {
          const { catalog } = info
          return {
            id,
            label: catalog?.label || id,
            description: catalog?.description || info.desc || '',
            platform: groupPlatform(catalog),
            sortOrder: catalog?.sort_order ?? 0,
            ratio: info.ratio,
            badges: catalog?.badges,
          }
        }),
    [groups.data]
  )
  const error = groups.isError || (currentRow && detail.isError)
  const ready =
    groups.isSuccess &&
    groups.isFetchedAfterMount &&
    (!currentRow || (detail.isFetchedAfterMount && !!detail.data?.data))
  const reload = async () => {
    const result = await detail.refetch()
    if (result.isSuccess) setGeneration((previous) => previous + 1)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>
            {currentRow ? t('Update API Key') : t('Create API Key')}
          </DialogTitle>
          <DialogDescription>
            {t('Choose one group for this API key.')}
          </DialogDescription>
        </DialogHeader>
        {error ? (
          <ErrorState
            title={t('Failed to load API key settings')}
            onRetry={() => {
              void groups.refetch()
              if (currentRow) void detail.refetch()
            }}
          />
        ) : !ready ? (
          <LoadingState inline size='sm' message={t('Loading...')} />
        ) : open ? (
          <ApiKeyEditor
            key={`${userId}:${currentId ?? 'create'}:${generation}`}
            currentKey={detail.data?.data}
            groups={groupCards}
            draft={currentRow ? undefined : draft}
            onClose={() => onOpenChange(false)}
            onCreated={onCreated}
            onReload={reload}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

function ApiKeyEditor({
  currentKey: initialKey,
  groups,
  draft,
  onClose,
  onCreated,
  onReload,
}: {
  currentKey?: ApiKey
  groups: KeyGroupCard[]
  draft?: MarketKeyDraft
  onClose: () => void
  onCreated?: () => void
  onReload: () => Promise<void>
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id ?? null)
  useSystemConfigStore((state) => state.config.currency)
  const quotaPerUSD = getCurrencyDisplay().config.quotaPerUnit
  const { triggerRefresh } = useApiKeys()
  const [currentKey] = useState(initialKey)
  const [original] = useState(() =>
    initialKeyValues(currentKey, quotaPerUSD, nowSeconds())
  )
  const groupFromDraft =
    draft?.group && groups.some(({ id }) => id === draft.group)
      ? draft.group
      : undefined
  const [values, setValues] = useState<SingleGroupKeyValues>(() => ({
    ...original,
    group: groupFromDraft ?? original.group,
  }))
  const [advanced, setAdvanced] = useState(
    !!draft?.model || original.modelLimited || !!original.ips
  )
  const [draftPending, setDraftPending] = useState(
    !!groupFromDraft && !!draft?.model
  )
  const [draftRejected, setDraftRejected] = useState(
    (!!draft?.group && !groupFromDraft) || (!!draft?.model && !groupFromDraft)
  )
  const [busy, setBusy] = useState(false)
  const [conflict, setConflict] = useState(false)
  const [error, setError] = useState('')
  const inFlight = useRef(false)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])
  const set = <K extends keyof SingleGroupKeyValues>(
    field: K,
    next: SingleGroupKeyValues[K]
  ) => setValues((previous) => ({ ...previous, [field]: next }))

  const legacy = !!currentKey && isLegacyApiKey(currentKey)
  const authorized = groups.some(({ id }) => id === values.group)
  const changedGroup = values.group !== original.group
  const validGroup = currentKey && !changedGroup ? true : authorized
  const validVersion =
    !legacy || !changedGroup || !!currentKey?.settings_version
  const showModelQuery = advanced || draftPending
  const savedId = legacy && !changedGroup ? currentKey?.id : undefined
  const modelsAvailable = authorized || savedId !== undefined
  const models = useQuery({
    queryKey: ['api-key-fixed-scope-models', userId, values.group, savedId],
    queryFn: async () =>
      requireServerSuccess(
        savedId !== undefined
          ? await getSavedApiKeyModels(savedId)
          : await getApiKeyScopeModels({ group: values.group })
      ),
    enabled: showModelQuery && modelsAvailable,
    staleTime: 0,
    refetchOnWindowFocus: false,
  })
  useEffect(() => {
    if (!draftPending || !draft?.model || models.isFetching) return
    if (models.isSuccess && models.isFetchedAfterMount) {
      setDraftPending(false)
      if (models.data?.data?.includes(draft.model)) {
        setValues((previous) => ({
          ...previous,
          modelLimited: true,
          models: [draft.model!],
        }))
      } else {
        setDraftRejected(true)
      }
    } else if (models.isError) {
      setDraftPending(false)
      setDraftRejected(true)
    }
  }, [
    draft?.model,
    draftPending,
    models.data,
    models.isError,
    models.isFetchedAfterMount,
    models.isFetching,
    models.isSuccess,
  ])
  const modelOptions = useMemo(() => {
    const available = models.data?.data ?? []
    const names = new Set(available)
    return [
      ...available.map((model) => ({ label: model, value: model })),
      ...values.models
        .filter((model) => !names.has(model))
        .map((model) => ({
          label: model,
          value: model,
          hint: t('Unavailable for current routing scope'),
        })),
    ]
  }, [models.data, t, values.models])
  const quotaValid =
    (currentKey && values.quota === original.quota) ||
    Number.isFinite(quotaUnits(values.quota, quotaPerUSD))
  const expiryValid =
    (currentKey && values.days === original.days) ||
    Number.isFinite(expirySeconds(values.days, nowSeconds()))
  const ipValid =
    (currentKey && values.ips === original.ips) ||
    parseKeyIpAllowlist(values.ips) !== undefined
  const nameTooLong = [...values.name.trim()].length > 50
  const canSave =
    !!values.name.trim() &&
    !nameTooLong &&
    validGroup &&
    validVersion &&
    quotaValid &&
    expiryValid &&
    ipValid &&
    !busy &&
    !conflict &&
    !draftPending &&
    !draftRejected

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSave || inFlight.current) return
    inFlight.current = true
    setBusy(true)
    setError('')
    const epoch = getAuthSessionEpoch()
    const active = () => mounted.current && epoch === getAuthSessionEpoch()
    if (currentKey) {
      const payload = updateFixedKeyPayload(
        currentKey,
        values,
        original,
        quotaPerUSD,
        nowSeconds()
      )
      if (Object.keys(payload).length === 2) {
        inFlight.current = false
        onClose()
        return
      }
      try {
        requireServerSuccess(await updateApiKey(payload))
        if (!active()) return
        triggerRefresh()
        toast.success(t(SUCCESS_MESSAGES.API_KEY_UPDATED))
        onClose()
      } catch (err) {
        if (!active()) return
        if (isAxiosError(err) && err.response?.status === 409) {
          setConflict(true)
          triggerRefresh()
        } else {
          setError(t(ERROR_MESSAGES.UPDATE_FAILED))
          handleServerError(err, t(ERROR_MESSAGES.UPDATE_FAILED))
        }
      } finally {
        if (active()) {
          inFlight.current = false
          setBusy(false)
        }
      }
      return
    }
    try {
      requireServerSuccess(
        await createApiKey(
          createFixedKeyPayload(values, quotaPerUSD, nowSeconds())
        )
      )
      if (!active()) return
      triggerRefresh()
      toast.success(t(SUCCESS_MESSAGES.API_KEY_CREATED))
      onCreated?.()
      onClose()
    } catch (err) {
      if (!active()) return
      // A lost response can be a successful write. Refresh; never auto-replay.
      triggerRefresh()
      setError(
        t('Could not confirm key creation. Check the list before trying again.')
      )
      handleServerError(err, t(ERROR_MESSAGES.CREATE_FAILED))
    } finally {
      if (active()) {
        inFlight.current = false
        setBusy(false)
      }
    }
  }

  return (
    <form className='space-y-4' onSubmit={submit} noValidate aria-busy={busy}>
      <div className='grid gap-1.5'>
        <Label htmlFor='fixed-key-name'>{t('Token Name')}</Label>
        <Input
          id='fixed-key-name'
          autoComplete='off'
          placeholder={t('Enter token name')}
          value={values.name}
          onChange={(event) => set('name', event.target.value)}
          required
          aria-invalid={nameTooLong}
        />
        {nameTooLong && (
          <p className='text-destructive text-xs'>
            {t('Token name is too long')}
          </p>
        )}
      </div>
      <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
        <div className='grid gap-1.5'>
          <Label htmlFor='fixed-key-quota'>
            {currentKey ? t('Remaining quota (USD)') : t('Maximum quota (USD)')}
          </Label>
          <Input
            id='fixed-key-quota'
            inputMode='decimal'
            placeholder={t('Unlimited')}
            value={values.quota}
            onChange={(event) => set('quota', event.target.value)}
            aria-invalid={!quotaValid}
          />
          {!quotaValid && (
            <p className='text-destructive text-xs'>
              {t('Quota must be zero or greater')}
            </p>
          )}
        </div>
        <div className='grid gap-1.5'>
          <Label htmlFor='fixed-key-expiry'>{t('Validity (days)')}</Label>
          <Input
            id='fixed-key-expiry'
            inputMode='numeric'
            placeholder={t('Never expires')}
            value={values.days}
            onChange={(event) => set('days', event.target.value)}
            aria-invalid={!expiryValid}
          />
          {!expiryValid && (
            <p className='text-destructive text-xs'>
              {t('Enter a positive whole number of days or leave it blank.')}
            </p>
          )}
          {currentKey &&
            currentKey.expired_time !== -1 &&
            values.days === original.days && (
              <p className='text-muted-foreground text-xs'>
                {t('Current expiry: {{date}}', {
                  date: new Date(
                    currentKey.expired_time * 1000
                  ).toLocaleString(),
                })}
              </p>
            )}
        </div>
      </div>
      {legacy && (
        <div className='rounded-md border p-3 text-xs' role='note'>
          {t(
            'This key uses legacy routing. Leave the group unchanged to keep its current routing. Selecting a group switches the key to that group only.'
          )}
          {changedGroup && (
            <Button
              type='button'
              variant='ghost'
              size='xs'
              onClick={() => set('group', original.group)}
            >
              {t('Keep current routing')}
            </Button>
          )}
          {!validVersion && (
            <p className='text-destructive'>
              {t('Reload the key settings before changing its routing.')}
            </p>
          )}
        </div>
      )}
      {!!original.group &&
        !groups.some(({ id }) => id === original.group) &&
        !changedGroup && (
          <p className='text-muted-foreground text-xs' role='note'>
            {t(
              'The current group is unavailable. Select an authorized group to change it.'
            )}
          </p>
        )}
      {draftRejected && (
        <div
          className='flex flex-wrap items-center justify-between gap-2 rounded-md border p-2 text-xs'
          role='status'
        >
          <p>
            {t(
              'The group or model from the catalogue is unavailable. Select an authorized option.'
            )}
          </p>
          <Button
            type='button'
            variant='outline'
            size='xs'
            onClick={() => setDraftRejected(false)}
          >
            {t('Discard catalogue draft')}
          </Button>
        </div>
      )}
      <KeyGroupCards
        groups={groups}
        value={values.group}
        disabled={busy || draftPending}
        onChange={(group) => set('group', group)}
      />
      <div className='space-y-3 rounded-md border px-3 py-2.5'>
        <Button
          type='button'
          variant='ghost'
          className='w-full justify-start px-0'
          aria-expanded={advanced}
          onClick={() => setAdvanced(!advanced)}
        >
          {t('Advanced Settings')}
        </Button>
        {advanced && (
          <div className='space-y-3'>
            <div className='flex items-center justify-between gap-2'>
              <Label htmlFor='fixed-key-model-limit'>
                {t('Model limits enabled')}
              </Label>
              <Switch
                id='fixed-key-model-limit'
                checked={values.modelLimited}
                disabled={busy || draftPending}
                onCheckedChange={(checked) => set('modelLimited', checked)}
              />
            </div>
            {values.modelLimited && (
              <div className='grid gap-1.5'>
                <Label>{t('Model allowlist')}</Label>
                <MultiSelect
                  options={modelOptions}
                  selected={values.models}
                  onChange={(choices) => set('models', choices)}
                  placeholder={t('Select models')}
                  disabled={
                    busy ||
                    draftPending ||
                    !modelsAvailable ||
                    models.isFetching ||
                    models.isError
                  }
                />
                {models.isError && (
                  <Button
                    type='button'
                    size='xs'
                    variant='outline'
                    onClick={() => void models.refetch()}
                  >
                    {t('Retry')}
                  </Button>
                )}
                {!values.models.length && (
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Model restrictions are enabled with an empty allowlist. This key cannot use any model.'
                    )}
                  </p>
                )}
              </div>
            )}
            <div className='grid gap-1.5'>
              <Label htmlFor='fixed-key-ip-list'>
                {t('IP Whitelist (supports CIDR)')}
              </Label>
              <Textarea
                id='fixed-key-ip-list'
                value={values.ips}
                aria-invalid={!ipValid}
                placeholder={'192.0.2.1\n2001:db8::/32'}
                onChange={(event) => set('ips', event.target.value)}
              />
              {!ipValid && (
                <p className='text-destructive text-xs'>
                  {t('Enter valid IP addresses or networks, one per line.')}
                </p>
              )}
            </div>
          </div>
        )}
      </div>
      {error && (
        <p role='alert' className='text-destructive text-sm'>
          {error}
        </p>
      )}
      {conflict && (
        <div
          role='alert'
          className='flex items-center justify-between gap-2 rounded-md border p-2 text-sm'
        >
          <p>{t('This key changed elsewhere. Reload before saving.')}</p>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void onReload()}
          >
            {t('Reload')}
          </Button>
        </div>
      )}
      <DialogFooter>
        <Button
          type='button'
          variant='outline'
          onClick={onClose}
          disabled={busy}
        >
          {t('Cancel')}
        </Button>
        <Button type='submit' disabled={!canSave}>
          {busy ? t('Saving...') : currentKey ? t('Save changes') : t('Add')}
        </Button>
      </DialogFooter>
    </form>
  )
}
