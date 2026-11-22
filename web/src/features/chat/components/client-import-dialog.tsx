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
  useInfiniteQuery,
  useMutation,
  useQueryClient,
} from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Label } from '@/components/ui/label'
import { useApiInfo } from '@/features/dashboard/hooks/use-status-data'
import { fetchTokenKey, getApiKey, getApiKeys } from '@/features/keys/api'
import { ApiKeysMutateDrawer } from '@/features/keys/components/api-keys-mutate-drawer'
import { ApiKeysProvider } from '@/features/keys/components/api-keys-provider'
import { CCSwitchDialog } from '@/features/keys/components/dialogs/cc-switch-dialog'
import type { ApiKey } from '@/features/keys/types'
import { handleServerError } from '@/lib/handle-server-error'
import { requireServerSuccess } from '@/lib/server-error-message'

import { resolveChatUrl, type ChatPreset } from '../lib/chat-links'
import {
  clientApiBase,
  isCCSwitchPreset,
  isImportableKey,
  isSafeClientLink,
} from '../lib/client-import'

type Props = {
  preset: ChatPreset
  serverAddress: string
  userId: number
  onClose: () => void
}

/** Mounted only for one import attempt; closing drops all plaintext key state. */
export function ClientImportDialog(props: Props) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { items: apiInfo } = useApiInfo()
  const [step, setStep] = useState<'key' | 'endpoint' | 'cc-switch'>('key')
  const [creating, setCreating] = useState(false)
  const [selectedId, setSelectedId] = useState('')
  const [selectedToken, setSelectedToken] = useState<ApiKey | null>(null)
  const [secret, setSecret] = useState('')
  const [endpoint, setEndpoint] = useState(
    clientApiBase(props.serverAddress) ?? ''
  )
  const pending = useRef<AbortController | null>(null)
  useEffect(() => () => pending.current?.abort(), [])

  const queryKey = ['client-import-keys', props.userId] as const
  const keys = useInfiniteQuery({
    queryKey,
    queryFn: async ({ pageParam }) =>
      requireServerSuccess(await getApiKeys({ p: pageParam, size: 50 })),
    initialPageParam: 1,
    getNextPageParam: (last, pages) =>
      pages.length * 50 < (last.data?.total ?? 0)
        ? pages.length + 1
        : undefined,
    staleTime: 0,
    gcTime: 0,
    meta: { errorToast: false },
  })
  const availableKeys = (
    keys.data?.pages.flatMap((page) => page.data?.items ?? []) ?? []
  ).filter(isImportableKey)
  const keyOptions = availableKeys.map((token) => ({
    value: String(token.id),
    label: `${token.name} (#${token.id})`,
  }))
  const endpoints = useMemo(() => {
    const candidates = [
      { url: props.serverAddress, route: t('Server address') },
      ...apiInfo,
    ]
    const unique = new Map<string, { value: string; label: string }>()
    for (const candidate of candidates) {
      const base = clientApiBase(candidate.url)
      if (base && !unique.has(base)) {
        unique.set(base, {
          value: base,
          label: `${candidate.route || base} · ${base}`,
        })
      }
    }
    return [...unique.values()]
  }, [apiInfo, props.serverAddress, t])
  const selectedEndpoint =
    endpoints.find((item) => item.value === endpoint)?.value ??
    endpoints[0]?.value ??
    ''

  const prepare = useMutation({
    // The mutation returns no secret, so Query Devtools/cache cannot retain it.
    mutationFn: async () => {
      pending.current?.abort()
      const request = new AbortController()
      pending.current = request
      const result = requireServerSuccess(await getApiKey(Number(selectedId)))
      if (request.signal.aborted) return
      if (!result.data || !isImportableKey(result.data)) {
        throw new Error(
          t('This API Key is no longer available. Please select another one.')
        )
      }
      const revealed = requireServerSuccess(
        await fetchTokenKey(result.data.id, { signal: request.signal })
      )
      if (request.signal.aborted) return
      if (!revealed.data?.key) {
        throw new Error(t('Unable to retrieve the API Key'))
      }
      setSelectedToken(result.data)
      setSecret(revealed.data.key)
      setStep('endpoint')
    },
    onError: (error) => {
      if (!pending.current?.signal.aborted) handleServerError(error)
    },
    retry: false,
    gcTime: 0,
  })

  const close = () => {
    pending.current?.abort()
    setSecret('')
    props.onClose()
  }
  const launch = () => {
    if (!secret || !selectedEndpoint) return
    if (isCCSwitchPreset(props.preset.url)) {
      setStep('cc-switch')
      return
    }
    const url = resolveChatUrl({
      template: props.preset.url,
      apiKey: secret,
      serverAddress: selectedEndpoint,
    })
    if (!isSafeClientLink(url)) {
      toast.error(t('Invalid chat link. Please contact the administrator.'))
      return
    }
    // This click has no await, preserving the browser's user activation.
    window.open(url, '_blank', 'noopener,noreferrer')
    close()
  }

  if (step === 'cc-switch') {
    return (
      <CCSwitchDialog
        open
        onOpenChange={(open) => {
          if (!open) close()
        }}
        tokenKey={secret}
        token={selectedToken}
        serverAddress={selectedEndpoint}
      />
    )
  }

  return (
    <>
      <Dialog
        open={!creating}
        onOpenChange={(open) => {
          if (!open) close()
        }}
        title={
          step === 'key'
            ? t('Select an API Key for {{client}}', {
                client: props.preset.name,
              })
            : t('API Endpoint')
        }
        description={
          step === 'key'
            ? t('Choose the API Key to import. You can also create a new one.')
            : t(
                'Import the selected API Key to {{client}} using this endpoint.',
                { client: props.preset.name }
              )
        }
        contentClassName='sm:max-w-md'
        bodyClassName='space-y-3'
        footer={
          <>
            <Button variant='outline' onClick={close}>
              {t('Cancel')}
            </Button>
            {step === 'key' ? (
              <>
                <Button
                  variant='outline'
                  disabled={prepare.isPending}
                  onClick={() => setCreating(true)}
                >
                  <Plus aria-hidden='true' />
                  {t('Create new Key')}
                </Button>
                <Button
                  disabled={
                    !availableKeys.some(
                      (token) => String(token.id) === selectedId
                    ) || prepare.isPending
                  }
                  onClick={() => prepare.mutate()}
                >
                  {prepare.isPending ? t('Loading...') : t('Continue')}
                </Button>
              </>
            ) : (
              <>
                <Button
                  variant='outline'
                  onClick={() => {
                    setSecret('')
                    setStep('key')
                  }}
                >
                  {t('Back')}
                </Button>
                <Button disabled={!selectedEndpoint} onClick={launch}>
                  {t('Import to {{client}}', { client: props.preset.name })}
                </Button>
              </>
            )}
          </>
        }
      >
        {step === 'key' ? (
          <>
            {keys.isPending && <LoadingState className='min-h-20' />}
            {keys.isError && (
              <ErrorState
                className='min-h-20'
                onRetry={() => {
                  void keys.refetch()
                }}
              />
            )}
            {!keys.isPending && !keys.isError && (
              <>
                <Label htmlFor='client-import-key'>{t('API Key')}</Label>
                <Combobox
                  id='client-import-key'
                  aria-label={t('API Key')}
                  value={selectedId}
                  onValueChange={(value) => setSelectedId(value ?? '')}
                  options={keyOptions}
                  disabled={prepare.isPending}
                  placeholder={t('Select an API Key')}
                />
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Only enabled, unexpired API Keys with available quota can be imported.'
                  )}
                </p>
                {availableKeys.length === 0 && (
                  <EmptyState
                    className='min-h-20'
                    title={t('No available API Keys')}
                  />
                )}
                {keys.hasNextPage && (
                  <Button
                    variant='outline'
                    disabled={keys.isFetchingNextPage}
                    onClick={() => {
                      void keys.fetchNextPage()
                    }}
                  >
                    {t('Load more')}
                  </Button>
                )}
              </>
            )}
          </>
        ) : (
          <>
            <Label htmlFor='client-import-endpoint'>{t('API Endpoint')}</Label>
            <Combobox
              id='client-import-endpoint'
              aria-label={t('API Endpoint')}
              value={selectedEndpoint}
              onValueChange={(value) => setEndpoint(value ?? '')}
              options={endpoints}
            />
            <p className='text-muted-foreground text-sm break-all'>
              {selectedEndpoint}
            </p>
            {!selectedEndpoint && (
              <EmptyState
                className='min-h-20'
                title={t('No available API endpoints')}
              />
            )}
          </>
        )}
      </Dialog>
      {creating && (
        <ApiKeysProvider>
          <ApiKeysMutateDrawer
            open
            onOpenChange={setCreating}
            onCreated={() => {
              setSelectedId('')
              void queryClient.resetQueries({ queryKey })
            }}
          />
        </ApiKeysProvider>
      )}
    </>
  )
}
