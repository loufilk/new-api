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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Brain } from 'lucide-react'
import { type RefObject, useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox } from '@/components/ui/combobox'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { toIntlLocale } from '@/i18n/languages'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { formatNumber } from '@/lib/format'
import { getServerErrorStatus } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  type DiagnosticDraft,
  type DiagnosticOptions,
  type ActiveDiagnosticQuestion,
  clearDiagnosticDraft,
  diagnosticInProgress,
  diagnosticLimit,
  diagnosticQuestionLabels,
  diagnosticStatusLabels,
  isActiveDiagnosticQuestion,
  isDiagnosticConflict,
  isDiagnosticMissing,
  isDiagnosticRejected,
  loadDiagnosticDraft,
  markDiagnosticDraftRejected,
  markDiagnosticRetry,
  matchesDiagnosticRun,
  readDiagnosticHistory,
  readDiagnosticModels,
  readDiagnosticOptions,
  readDiagnosticRun,
  saveDiagnosticDraft,
  submitDiagnostic,
} from '../lib/key-diagnostic'
import { useApiKeys } from './api-keys-provider'
import { KeyDiagnosticResult } from './key-diagnostic-result'

const routingLabels = {
  auto: 'Automatic',
  success_rate: 'Success rate first',
  price: 'Price first',
  speed: 'Speed first',
} as const

function KeyRouting({
  selected,
}: {
  selected: DiagnosticOptions['keys'][number]
}) {
  const { t } = useTranslation()
  if (selected.smart_routing) {
    return (
      <p className='text-muted-foreground text-xs'>
        {t('Smart Routing')}
        {selected.routing_strategy
          ? `: ${t(routingLabels[selected.routing_strategy])}`
          : ''}
        {' · '}
        {t('The selected route determines the group price.')}
      </p>
    )
  }
  if (selected.group === 'auto') {
    return (
      <p className='text-muted-foreground text-xs'>
        {t('Auto group')}
        {selected.auto_groups?.length
          ? `: ${selected.auto_groups.join(', ')}`
          : ''}
        {' · '}
        {t('The selected route determines the group price.')}
      </p>
    )
  }
  return (
    <p className='text-muted-foreground text-xs'>
      {selected.group ? (
        <>
          {t('Group')}: {selected.group}
          {' · '}
          {t('The Key group price applies.')}
        </>
      ) : (
        t('Group not available')
      )}
    </p>
  )
}

export function KeyDiagnosticButton(props: {
  tokenId?: number
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const owner = useAuthStore((state) => state.auth.user?.id)
  const { openDiagnostic } = useApiKeys()
  const trigger = useRef<HTMLButtonElement>(null)
  return (
    <Button
      ref={trigger}
      type='button'
      variant={props.tokenId ? 'ghost' : 'outline'}
      size={props.tokenId ? 'icon-sm' : 'sm'}
      title={t('Key diagnostic')}
      aria-label={t('Key diagnostic')}
      disabled={props.disabled || !owner}
      onClick={() => openDiagnostic(trigger, props.tokenId)}
    >
      <Brain
        className='text-violet-600 dark:text-violet-400'
        aria-hidden='true'
      />
      {!props.tokenId && t('Key diagnostic')}
    </Button>
  )
}

type KeyDiagnosticLauncherProps = {
  tokenId?: number
  onClose: () => void
  finalFocus?: RefObject<HTMLButtonElement | null>
}

// The table can replace row renderers on a clock tick, refetch or breakpoint
// change. Keep both launch paths in one session-scoped host above the table.
export function KeyDiagnosticHost() {
  const { diagnostic, setOpen } = useApiKeys()
  if (!diagnostic) return null
  return (
    <KeyDiagnosticLauncher
      tokenId={diagnostic.tokenId}
      finalFocus={diagnostic.trigger}
      onClose={() => setOpen(null)}
    />
  )
}

function KeyDiagnosticLauncher(props: KeyDiagnosticLauncherProps) {
  const owner = useAuthStore((state) => state.auth.user?.id)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)

  if (!owner) return null

  return (
    <KeyDiagnosticDialog
      key={`${owner}:${sessionId ?? ''}`}
      owner={owner}
      sessionId={sessionId}
      {...props}
    />
  )
}

function KeyDiagnosticDialog(
  props: KeyDiagnosticLauncherProps & {
    owner: number
    sessionId?: string
  }
) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const prefix = useId()
  const queryClient = useQueryClient()
  const [epoch] = useState(getAuthSessionEpoch)
  const { owner, sessionId } = props
  const queryNamespace = ['key-diagnostics', owner, epoch] as const
  const [draft, setDraft] = useState(() =>
    loadDiagnosticDraft(owner, sessionId)
  )
  const [viewId, setViewId] = useState(draft?.id ?? null)
  const [tokenId, setTokenId] = useState(props.tokenId ?? 0)
  const [questionId, setQuestion] =
    useState<ActiveDiagnosticQuestion>('pelican-svg-v2')
  const [selection, setSelection] = useState<{
    model: string
    revision: number
  } | null>(null)
  const [approved, setApproved] = useState(false)
  const [localError, setLocalError] = useState<'storage' | null>(null)
  const submitting = useRef(false)
  const [lastLimit, setLastLimit] = useState(draft?.limit)

  const options = useQuery({
    queryKey: [...queryNamespace, 'options'],
    queryFn: ({ signal }) => readDiagnosticOptions(signal),
    staleTime: 0,
    gcTime: 0,
    retry: false,
    meta: { errorToast: false },
  })
  const selected = options.data?.keys.find((key) => key.id === tokenId)
  const models = useQuery({
    queryKey: [...queryNamespace, 'models', tokenId],
    enabled: !!selected && !viewId,
    queryFn: ({ signal }) => readDiagnosticModels(tokenId, signal),
    staleTime: 0,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
    meta: { errorToast: false },
  })
  const question = options.data?.questions.find(
    (item) => item.id === questionId
  )
  // A refreshed catalogue cannot resurrect a previously rejected selection.
  const model =
    models.isSuccess &&
    selection?.revision === models.dataUpdatedAt &&
    models.data.includes(selection.model)
      ? selection.model
      : ''
  const history = useQuery({
    queryKey: [...queryNamespace, 'history'],
    queryFn: ({ signal }) => readDiagnosticHistory(signal),
    retry: false,
    gcTime: 0,
    meta: { errorToast: false },
  })
  function hasConsentSession() {
    const auth = useAuthStore.getState().auth
    return (
      !!sessionId &&
      getAuthSessionEpoch() === epoch &&
      auth.user?.id === owner &&
      auth.session?.sid === sessionId
    )
  }

  const submission = useMutation({
    mutationFn: ({ attempt }: { attempt: DiagnosticDraft; retry: boolean }) => {
      if (!hasConsentSession()) throw new Error('Diagnostic login changed')
      return submitDiagnostic(attempt, epoch)
    },
    retry: false,
    gcTime: 0,
    meta: { errorToast: false },
    onError: (error, { attempt, retry }) => {
      if (!hasConsentSession()) return
      const limit = diagnosticLimit(error, Date.now())
      setLastLimit(limit)
      if (!retry && isDiagnosticRejected(error)) {
        markDiagnosticDraftRejected(owner, sessionId, attempt, limit)
        const stored = loadDiagnosticDraft(owner, sessionId)
        if (stored && matchesDiagnosticRun(stored, attempt)) setDraft(stored)
      }
    },
    onSuccess: (run, { attempt }) => {
      if (!hasConsentSession()) return
      clearDiagnosticDraft(owner, sessionId, attempt.id)
      queryClient.setQueryData([...queryNamespace, 'run', attempt.id], run)
      void queryClient.invalidateQueries({
        queryKey: [...queryNamespace, 'history'],
      })
    },
    onSettled: () => {
      submitting.current = false
    },
  })
  const detail = useQuery({
    queryKey: [...queryNamespace, 'run', viewId],
    enabled: viewId !== null && !submission.isPending,
    queryFn: ({ signal }) => readDiagnosticRun(viewId ?? '', signal),
    staleTime: 0,
    gcTime: 0,
    retry: false,
    refetchInterval: ({ state }) => {
      if (!state.error) {
        return state.data && diagnosticInProgress(state.data.status)
          ? 2500
          : false
      }
      // If an acknowledgement or detail read was lost, reconcile briefly by
      // GET only. No automatic paid POST and no retry storm on access/edge limits.
      const status = getServerErrorStatus(state.error)
      if (state.errorUpdateCount >= 4 || submission.isPending) return false
      if (draft?.state === 'rejected-before-save' && viewId === draft.id) {
        return false
      }
      if (status && status < 500 && !isDiagnosticMissing(state.error)) {
        return false
      }
      return Math.min(2500 * 2 ** (state.errorUpdateCount - 1), 10000)
    },
    meta: { errorToast: false },
  })
  const found =
    detail.data ??
    (submission.data?.id === viewId ? submission.data : undefined)
  const draftWasViewed = draft?.id === viewId
  const mismatch = !!(
    found &&
    draft &&
    draftWasViewed &&
    !matchesDiagnosticRun(found, draft)
  )
  const run = mismatch ? undefined : found
  const accepted = !!(run && draft && matchesDiagnosticRun(run, draft))
  useEffect(() => {
    if (accepted && draft) {
      clearDiagnosticDraft(owner, sessionId, draft.id)
    }
  }, [accepted, draft, owner, sessionId])

  const conflict =
    mismatch || (draftWasViewed && isDiagnosticConflict(submission.error))
  const missing = !run && isDiagnosticMissing(detail.error)
  const canChoose = !!(
    sessionId &&
    options.isSuccess &&
    !options.isFetching &&
    selected &&
    models.isSuccess &&
    !models.isFetching
  )
  const canStart = !!(
    canChoose &&
    question &&
    model &&
    approved &&
    !submission.isPending
  )
  const knownNotSaved = draft?.state === 'rejected-before-save'
  const rejected = draftWasViewed && missing && knownNotSaved
  const retiredDraft =
    draft && draftWasViewed && !isActiveDiagnosticQuestion(draft.question)
  const canStartAfterRetired = !!(
    retiredDraft &&
    missing &&
    !conflict &&
    !detail.isFetching &&
    !submission.isPending &&
    options.isSuccess &&
    !options.isFetching &&
    options.data.questions.some(({ id }) => id === 'pelican-svg-v2') &&
    options.data.questions.some(({ id }) => id === 'candy-logic-v2')
  )
  const canRetry = !!(
    sessionId &&
    draft &&
    draftWasViewed &&
    missing &&
    !conflict &&
    !rejected &&
    !retiredDraft &&
    !submission.isPending
  )
  const limit = draftWasViewed ? lastLimit : undefined
  let limitMessage = ''
  if (limit) {
    switch (limit.reason) {
      case 'hourly':
        limitMessage = t('Your hourly test limit has been reached.')
        break
      case 'user_active':
        limitMessage = t(
          'You already have a test in progress. Wait for it to finish.'
        )
        break
      case 'capacity':
        limitMessage = t('All test slots are busy. Please try again later.')
        break
      case 'request_rate':
        limitMessage = t('Test requests were made too quickly.')
        break
      default:
        limitMessage = t(
          'The test limit was reached. Wait before trying again.'
        )
    }
  }
  let recoveryMessage = t('Loading...')
  if (rejected && limit) {
    recoveryMessage = t('This test was not started or charged.')
  } else if (!run && getServerErrorStatus(detail.error) === 429) {
    recoveryMessage = t(
      'Result lookup is temporarily limited. Check this ID again later; do not submit another test.'
    )
  } else if (rejected) {
    recoveryMessage = t(
      'The server rejected this test before saving it. Review your selection.'
    )
  } else if (retiredDraft && missing) {
    recoveryMessage = t(
      'No saved result was found for the old question. The earlier outcome is not confirmed.'
    )
  } else if (canRetry) {
    recoveryMessage = t(
      'No saved result yet. Retrying keeps the same request ID.'
    )
  } else if (detail.isError || submission.isError) {
    recoveryMessage = t(
      'Submission could not be confirmed. Check this request before starting another test.'
    )
  }

  function send(attempt: DiagnosticDraft, retry = false) {
    if (
      submitting.current ||
      !hasConsentSession() ||
      !isActiveDiagnosticQuestion(attempt.question) ||
      (retry && (!canRetry || detail.isFetching))
    ) {
      return
    }
    if (retry) {
      const stored = loadDiagnosticDraft(owner, sessionId)
      if (!stored || !matchesDiagnosticRun(stored, attempt)) return
      if (stored.state === 'rejected-before-save') {
        setDraft(stored)
        setLastLimit(stored.limit)
        return
      }
      if (!markDiagnosticRetry(owner, sessionId, attempt)) {
        setLocalError('storage')
        return
      }
    }
    submitting.current = true
    setLocalError(null)
    setLastLimit(undefined)
    submission.mutate({ attempt, retry })
  }

  function start() {
    if (
      !canStart ||
      submitting.current ||
      !hasConsentSession() ||
      !selected ||
      !question
    ) {
      return
    }
    setLocalError(null)
    const existing = loadDiagnosticDraft(owner, sessionId)
    if (existing) {
      // A list/row reopen (or another tab) must not silently replace an unconfirmed request.
      setDraft(existing)
      setViewId(existing.id)
      return
    }
    if (typeof globalThis.crypto?.randomUUID !== 'function') {
      setLocalError('storage')
      return
    }
    const attempt = {
      id: crypto.randomUUID(),
      token_id: selected.id,
      model,
      question: question.id,
    }
    if (!saveDiagnosticDraft(owner, sessionId, attempt)) {
      setLocalError('storage')
      return
    }
    setDraft(attempt)
    setViewId(attempt.id)
    setApproved(false)
    send(attempt)
  }

  function setup() {
    if (!hasConsentSession()) return
    if (draft && (accepted || conflict || rejected || canStartAfterRetired)) {
      clearDiagnosticDraft(owner, sessionId, draft.id)
    }
    if (rejected) {
      // Preflight rejected the published choice. Refresh both catalogues
      // before exposing a new paid confirmation; do not replay this tuple.
      void queryClient.invalidateQueries({
        queryKey: [...queryNamespace, 'options'],
      })
      void queryClient.invalidateQueries({
        queryKey: [...queryNamespace, 'models'],
      })
    }
    setDraft(loadDiagnosticDraft(owner, sessionId))
    setSelection(null)
    setApproved(false)
    setViewId(null)
    setLastLimit(undefined)
    submission.reset()
  }

  return (
    <Dialog
      open
      disablePointerDismissal
      onOpenChange={(next) => !next && props.onClose()}
      finalFocus={props.finalFocus}
      title={t('Key diagnostic')}
      description={t(
        'This sends one paid request with your selected API key. Your normal account balance and group price apply. A single answer does not verify model identity.'
      )}
      contentClassName='sm:max-w-2xl'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button variant='outline' onClick={props.onClose}>
            {t('Close')}
          </Button>
          {!viewId && (
            <Button disabled={!canStart} onClick={start}>
              {t('Start test')}
            </Button>
          )}
          {viewId && !submission.isPending && (
            <>
              {draft && !draftWasViewed && (
                <Button variant='outline' onClick={() => setViewId(draft.id)}>
                  {t('Resume previous test')}
                </Button>
              )}
              {(!draft ||
                accepted ||
                conflict ||
                rejected ||
                canStartAfterRetired) && (
                <Button variant='outline' onClick={setup}>
                  {t('New test')}
                </Button>
              )}
              <Button
                variant='outline'
                disabled={detail.isFetching}
                onClick={() => void detail.refetch()}
              >
                {t('Check result')}
              </Button>
              {canRetry && draft && (
                <Button
                  disabled={detail.isFetching}
                  onClick={() => send(draft, true)}
                >
                  {t('Retry same request')}
                </Button>
              )}
            </>
          )}
        </>
      }
    >
      {localError && (
        <p role='alert' className='text-destructive text-sm'>
          {t(
            'This browser could not save the request for recovery. No test was sent.'
          )}
        </p>
      )}
      {!viewId && (
        <>
          {draft && (
            <Button variant='outline' onClick={() => setViewId(draft.id)}>
              {t('Resume previous test')}
            </Button>
          )}
          {options.isPending && (
            <LoadingState inline message={t('Loading...')} />
          )}
          {options.isError && (
            <p role='alert' className='text-destructive text-sm'>
              {t('Could not load test options.')}
            </p>
          )}
          {options.data && (
            <>
              <div className='space-y-2'>
                <Label htmlFor={`${prefix}key`}>{t('API Key')}</Label>
                <NativeSelect
                  id={`${prefix}key`}
                  value={tokenId || ''}
                  disabled={!!props.tokenId}
                  onChange={(event) => {
                    setTokenId(Number(event.target.value))
                    setSelection(null)
                    setApproved(false)
                  }}
                >
                  <NativeSelectOption value=''>
                    {t('Select API key')}
                  </NativeSelectOption>
                  {options.data.keys.map((key) => (
                    <NativeSelectOption key={key.id} value={key.id}>
                      {(key.name || `#${key.id}`) +
                        (key.group
                          ? ` · ${key.group === 'auto' ? t('Auto group') : key.group}`
                          : '')}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                {selected && <KeyRouting selected={selected} />}
              </div>
              {(!options.data.keys.length || (props.tokenId && !selected)) && (
                <EmptyState title={t('No eligible API key')} />
              )}
              <fieldset className='space-y-2'>
                <legend className='text-sm font-medium'>
                  {t('Test question')}
                </legend>
                <RadioGroup
                  value={questionId}
                  onValueChange={(value) => {
                    if (!isActiveDiagnosticQuestion(value)) return
                    setQuestion(value)
                    setApproved(false)
                  }}
                  className='flex flex-wrap gap-x-5 gap-y-2'
                >
                  {options.data.questions.map((item) => (
                    <div key={item.id} className='flex items-center gap-2'>
                      <RadioGroupItem id={prefix + item.id} value={item.id} />
                      <Label htmlFor={prefix + item.id}>
                        {t(diagnosticQuestionLabels[item.id])}
                      </Label>
                    </div>
                  ))}
                </RadioGroup>
              </fieldset>
              {question && (
                <div className='space-y-2'>
                  <div className='bg-muted/50 max-h-48 overflow-auto rounded-lg border p-3 text-sm whitespace-pre-wrap'>
                    {question.prompt}
                  </div>
                  <p className='text-muted-foreground text-xs'>
                    {t('Output limit')}:{' '}
                    {formatNumber(question.max_output_tokens, locale)}{' '}
                    {t('tokens')}
                  </p>
                </div>
              )}
              <div className='space-y-2'>
                <Label htmlFor={`${prefix}model`}>{t('Model')}</Label>
                <Combobox
                  id={`${prefix}model`}
                  options={(models.data ?? []).map((value) => ({
                    value,
                    label: value,
                  }))}
                  value={model}
                  searchPlaceholder={t('Search models')}
                  disabled={!canChoose}
                  onValueChange={(value) => {
                    setSelection(
                      value
                        ? { model: value, revision: models.dataUpdatedAt }
                        : null
                    )
                    setApproved(false)
                  }}
                />
                {selected && models.isPending && (
                  <LoadingState inline message={t('Loading...')} />
                )}
                {selected && models.isError && (
                  <p role='alert' className='text-destructive text-sm'>
                    {t('Could not load models for this key.')}
                  </p>
                )}
                {selected && models.data?.length === 0 && (
                  <p className='text-muted-foreground text-sm'>
                    {t('No eligible chat models for this key.')}
                  </p>
                )}
              </div>
              <div className='flex items-start gap-2'>
                <Checkbox
                  id={`${prefix}consent`}
                  checked={approved && !!model}
                  disabled={!canChoose || !model || !question}
                  onCheckedChange={(checked) => setApproved(checked === true)}
                />
                <Label htmlFor={`${prefix}consent`} className='leading-snug'>
                  {t('I agree to one paid test with this API key.')}
                </Label>
              </div>
            </>
          )}
        </>
      )}
      {viewId && (
        <>
          {submission.isPending && (
            <p role='status'>
              {t('Submitting test. You can close this window and check later.')}
            </p>
          )}
          {conflict && (
            <p role='alert' className='text-destructive text-sm'>
              {t(
                'This request ID belongs to a different selection. It was not retried.'
              )}
            </p>
          )}
          {!conflict && !run && !submission.isPending && (
            <div role='status' className='space-y-2 text-sm'>
              {limit && <p>{limitMessage}</p>}
              {limit?.retryAt && (
                <p>
                  {t('Try again after {{time}}.', {
                    time: new Date(limit.retryAt).toLocaleTimeString(locale),
                  })}
                </p>
              )}
              <p>{recoveryMessage}</p>
              <p className='text-muted-foreground text-xs break-all'>
                {t('Test ID')}: {viewId}
              </p>
            </div>
          )}
          {run && <KeyDiagnosticResult run={run} />}
        </>
      )}
      <details className='rounded-lg border p-3'>
        <summary className='cursor-pointer text-sm font-medium'>
          {t('Recent tests')}
        </summary>
        <div className='mt-2 max-h-48 space-y-1 overflow-auto'>
          {history.isPending && (
            <LoadingState inline message={t('Loading...')} />
          )}
          {history.isError && (
            <p className='text-muted-foreground text-sm'>
              {t('Could not load recent tests.')}
            </p>
          )}
          {history.data?.length === 0 && (
            <p className='text-muted-foreground text-sm'>
              {t('No previous tests')}
            </p>
          )}
          {history.data?.map((item) => (
            <Button
              key={item.id}
              variant={viewId === item.id ? 'secondary' : 'ghost'}
              size='sm'
              className='w-full justify-between gap-2'
              aria-pressed={viewId === item.id}
              disabled={submission.isPending}
              onClick={() => {
                if (accepted) setDraft(null)
                setViewId(item.id)
              }}
            >
              <span className='truncate'>{item.model ?? item.id}</span>
              <span className='text-muted-foreground text-xs'>
                {t(diagnosticStatusLabels[item.status])}
              </span>
            </Button>
          ))}
        </div>
      </details>
    </Dialog>
  )
}
