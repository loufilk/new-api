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
import { useMutation, useQuery } from '@tanstack/react-query'
import { FlaskConical } from 'lucide-react'
import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'
import { formatNumber } from '@/lib/format'
import {
  requireServerSuccess,
  getServerErrorMessage,
} from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

type Question = { id: string; prompt: string; max_output_tokens: number }
type Options = { keys: { id: number; name: string }[]; questions: Question[] }
type Result = {
  id: string
  status: string
  error_code: string
  request_id: string
  answer: string
}
type Response<T> = { success: boolean; data: T }

const endpoint = '/api/user/key-diagnostics'
async function read<T>(path: string, signal?: AbortSignal) {
  return requireServerSuccess(
    (await api.get<Response<T>>(endpoint + path, { signal })).data
  ).data
}

export function KeyDiagnosticButton(props: {
  tokenId?: number
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const owner = useAuthStore((s) => s.auth.user?.id)
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button
        type='button'
        variant={props.tokenId ? 'ghost' : 'outline'}
        size={props.tokenId ? 'icon-sm' : 'sm'}
        title={t('Key diagnostic')}
        aria-label={t('Key diagnostic')}
        disabled={props.disabled}
        onClick={() => setOpen(true)}
      >
        <FlaskConical aria-hidden='true' />
        {!props.tokenId && t('Key diagnostic')}
      </Button>
      {open && (
        <KeyDiagnosticDialog
          key={owner}
          tokenId={props.tokenId}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  )
}

function KeyDiagnosticDialog(props: { tokenId?: number; onClose: () => void }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const prefix = useId()
  const owner = useAuthStore((s) => s.auth.user?.id)
  const storageKey =
    'key-diagnostic:last:' + owner + ':' + (props.tokenId ?? 'picker')
  const [tokenId, setTokenId] = useState(props.tokenId ?? 0)
  const [questionId, setQuestionId] = useState('pelican-svg-v1')
  const [model, setModel] = useState('')
  const [attempt, setAttempt] = useState<string | null>(() => {
    try {
      return sessionStorage.getItem(storageKey)
    } catch {
      return null
    }
  })
  const submitted = useRef(!!attempt)
  const [result, setResult] = useState<Result>()
  const options = useQuery({
    queryKey: ['key-diagnostic-options', owner],
    queryFn: ({ signal }) => read<Options>('/options', signal),
    staleTime: 0,
    gcTime: 0,
    meta: { errorToast: false },
  })
  const selected = options.data?.keys.find((k) => k.id === tokenId)
  const models = useQuery({
    queryKey: ['key-diagnostic-models', owner, tokenId],
    enabled: !!selected,
    queryFn: ({ signal }) =>
      read<string[]>('/keys/' + tokenId + '/models', signal),
    staleTime: 0,
    gcTime: 0,
    meta: { errorToast: false },
  })
  const question = options.data?.questions.find((q) => q.id === questionId)
  const run = useMutation({
    retry: false,
    meta: { errorToast: false },
    mutationFn: async (id: string) =>
      requireServerSuccess(
        (
          await api.post<Response<Result>>(
            endpoint + '/runs',
            {
              request_id: id,
              token_id: tokenId,
              model,
              question: questionId,
              confirmed: true,
            },
            { singleUseAuthorization: true, timeout: 185000 }
          )
        ).data
      ).data,
    onSuccess: setResult,
  })
  const check = useMutation({
    retry: false,
    meta: { errorToast: false },
    mutationFn: (id: string) => read<Result>('/runs/' + encodeURIComponent(id)),
    onSuccess: setResult,
  })
  const pending = run.isPending || check.isPending
  const start = () => {
    if (
      submitted.current ||
      !selected ||
      !question ||
      !models.data?.includes(model)
    )
      return
    const id = crypto.randomUUID()
    submitted.current = true
    setAttempt(id)
    try {
      sessionStorage.setItem(storageKey, id)
    } catch {
      /* private storage unavailable */
    }
    run.mutate(id)
  }
  const reset = () => {
    submitted.current = false
    try {
      sessionStorage.removeItem(storageKey)
    } catch {
      /* private storage unavailable */
    }
    setAttempt(null)
    setResult(undefined)
    run.reset()
    check.reset()
  }
  const err = check.error ?? run.error ?? options.error ?? models.error
  return (
    <Dialog
      open
      onOpenChange={(next) => {
        if (!next && !pending) props.onClose()
      }}
      showCloseButton={!pending}
      title={t('Key diagnostic')}
      description={t(
        'This sends one paid request with your selected API key. Your normal account balance and group price apply. A single answer does not verify model identity.'
      )}
      bodyClassName='space-y-4'
      footer={
        <>
          <Button variant='outline' disabled={pending} onClick={props.onClose}>
            {t('Close')}
          </Button>
          {!attempt && (
            <Button
              disabled={
                pending ||
                !selected ||
                !question ||
                !models.data?.includes(model)
              }
              onClick={start}
            >
              {t('Start test')}
            </Button>
          )}
          {attempt && (
            <Button
              variant='outline'
              disabled={pending}
              onClick={() => check.mutate(attempt)}
            >
              {t('Check result')}
            </Button>
          )}
          {attempt && !pending && result?.status !== 'running' && (
            <Button variant='outline' onClick={reset}>
              {t('New test')}
            </Button>
          )}
        </>
      }
    >
      {options.isPending && <LoadingState inline message={t('Loading...')} />}
      {err && (
        <p role='alert' className='text-destructive'>
          {getServerErrorMessage(err)}
        </p>
      )}
      {!attempt && options.data && (
        <>
          <div className='space-y-1'>
            <Label htmlFor={prefix + 'key'}>{t('API Key')}</Label>
            <NativeSelect
              id={prefix + 'key'}
              value={tokenId || ''}
              disabled={!!props.tokenId}
              onChange={(e) => {
                setTokenId(Number(e.target.value))
                setModel('')
              }}
            >
              <NativeSelectOption value=''>
                {t('Select API key')}
              </NativeSelectOption>
              {options.data.keys.map((k) => (
                <NativeSelectOption value={k.id} key={k.id}>
                  {k.name || '#' + k.id} (#{k.id})
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          {(!options.data.keys.length || (props.tokenId && !selected)) && (
            <EmptyState title={t('No eligible API key')} />
          )}
          <div className='space-y-1'>
            <Label htmlFor={prefix + 'model'}>{t('Model')}</Label>
            <NativeSelect
              id={prefix + 'model'}
              value={model}
              disabled={!selected || models.isPending}
              onChange={(e) => setModel(e.target.value)}
            >
              <NativeSelectOption value=''>
                {t('Select model')}
              </NativeSelectOption>
              {models.data?.map((m) => (
                <NativeSelectOption key={m} value={m}>
                  {m}
                </NativeSelectOption>
              ))}
            </NativeSelect>
            {selected && models.isPending && (
              <LoadingState inline message={t('Loading...')} />
            )}
            {selected && models.data?.length === 0 && (
              <p>{t('No eligible chat models for this key.')}</p>
            )}
          </div>
          <div className='space-y-1'>
            <Label htmlFor={prefix + 'question'}>{t('Test question')}</Label>
            <NativeSelect
              id={prefix + 'question'}
              value={questionId}
              onChange={(e) => setQuestionId(e.target.value)}
            >
              {options.data.questions.map((q) => (
                <NativeSelectOption key={q.id} value={q.id}>
                  {q.id === 'pelican-svg-v1'
                    ? t('Cycling pelican (SVG animation)')
                    : t('Drawing candies (logic puzzle)')}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
          {question && (
            <section className='bg-muted rounded-md p-3 text-sm'>
              <p className='whitespace-pre-wrap'>{question.prompt}</p>
              <p className='text-muted-foreground mt-2'>
                {t('Output limit')}:{' '}
                {formatNumber(question.max_output_tokens, locale)} tokens
              </p>
            </section>
          )}
        </>
      )}
      {pending && <LoadingState inline message={t('Loading...')} />}
      {attempt && !result && !pending && (
        <p role='status'>
          {t(
            'Check the existing request before starting another test. Checking does not send a model request.'
          )}
        </p>
      )}
      {result && (
        <section aria-live='polite' className='space-y-2'>
          <p>
            {t('Status')}: {t(result.status)} {result.error_code}
          </p>
          <p className='text-muted-foreground text-xs break-all'>
            {t('Request ID')}: {result.request_id}
          </p>
          {result.answer && (
            <pre className='bg-muted max-h-80 overflow-auto rounded-md p-3 text-xs break-words whitespace-pre-wrap'>
              {result.answer}
            </pre>
          )}
        </section>
      )}
    </Dialog>
  )
}
