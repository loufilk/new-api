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
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { prepareOperation, recoverOperation, submitOperation } from '../api'
import {
  forgetOperation,
  pendingOperations,
  rememberOperation,
} from '../lib/recovery'
import type { OperationDraft } from '../types'
import { Money, Quota } from './values'

export function OperationDialog(props: {
  draft: OperationDraft
  onClose: () => void
  onComplete: (result?: unknown) => void
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const client = useQueryClient()
  const verification = useSecureVerification()
  const locked = useRef(false)
  const [uncertain, setUncertain] = useState(() =>
    Boolean(
      userId &&
      pendingOperations(userId).some(
        (entry) => entry.request_id === props.draft.request_id
      )
    )
  )
  const [notFound, setNotFound] = useState(false)
  const prepare = useQuery({
    queryKey: ['agent-operation', userId, props.draft.request_id],
    queryFn: () => prepareOperation(props.draft),
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    staleTime: Infinity,
  })
  const submit = useMutation({
    mutationFn: (proof: string) => submitOperation(props.draft, proof),
    retry: false,
    meta: { errorToast: false },
  })
  const recovery = useMutation({
    mutationFn: () => recoverOperation(props.draft),
    retry: false,
    onSuccess: (page) => {
      if (page.status === 'not_observed' || page.status === 'pending') {
        setNotFound(true)
        return
      }
      if (userId) forgetOperation(userId, props.draft.request_id)
      void client.invalidateQueries({ queryKey: ['agent-platform'] })
      if (page.status === 'failed') {
        props.onClose()
        return
      }
      toast.info(t('Operation recorded'), {
        description: [page.resource_type, page.resource_id, page.resource_state]
          .filter(Boolean)
          .join(' · '),
      })
      let result = page.resource
      if (
        /^redemption\.(?:admin\.)?create$/.test(props.draft.action) &&
        Array.isArray(result)
      ) {
        result = result.flatMap((row: unknown) =>
          row &&
          typeof row === 'object' &&
          'key' in row &&
          typeof row.key === 'string'
            ? [row.key]
            : []
        )
      }
      props.onComplete(result)
    },
  })
  const prepared = prepare.data
  const context = prepared?.verification_context
  const exponent = prepared?.summary.currency_exponent ?? 0

  const confirm = async () => {
    if (!prepared || locked.current || uncertain || !userId) return
    if (
      pendingOperations(userId).some(
        (entry) =>
          entry.action === props.draft.action &&
          entry.request_id !== props.draft.request_id
      )
    ) {
      handleServerError(
        new Error(
          t(
            'Reconcile the unresolved request before creating a replacement operation.'
          )
        )
      )
      return
    }
    locked.current = true
    try {
      const proof = await verification.requestVerification({
        scope: 'agent.transaction',
        context: prepared.verification_context,
        title: t('Authorize this transaction'),
        description: t(
          'This authorization is valid only for the amount, recipient and request shown in the transaction review.'
        ),
      })
      if (!proof) return
      // Retain recovery identifiers before sending; tab closure cannot turn uncertainty into a new payment.
      rememberOperation(userId, props.draft)
      try {
        const result = await submit.mutateAsync(proof.proof_token)
        forgetOperation(userId, props.draft.request_id)
        void client.invalidateQueries({ queryKey: ['agent-platform'] })
        void client.invalidateQueries({ queryKey: ['wallet'] })
        toast.success(t('Operation recorded'))
        props.onComplete(result)
      } catch (error) {
        setUncertain(true)
        handleServerError(error)
        return
      }
    } catch (error) {
      handleServerError(error)
    } finally {
      locked.current = false
    }
  }

  return (
    <>
      <ConfirmDialog
        open
        onOpenChange={(open) => {
          if (!open && !submit.isPending && !verification.isActive) {
            props.onClose()
          }
        }}
        title={t('Review transaction')}
        desc={t(
          'Check the original currency, amount, destination and business number before authorizing.'
        )}
        confirmText={t('Authorize transaction')}
        disabled={!prepared || uncertain || verification.isActive}
        isLoading={submit.isPending}
        handleConfirm={() => {
          void confirm()
        }}
        className='sm:max-w-2xl'
      >
        {prepare.isPending && <LoadingState />}
        {prepare.isError && (
          <ErrorState
            description={getServerErrorMessage(prepare.error)}
            onRetry={() => {
              void prepare.refetch()
            }}
          />
        )}
        {context && (
          <dl className='grid grid-cols-1 gap-3 text-sm sm:grid-cols-2'>
            <div>
              <dt className='text-muted-foreground'>{t('Operation')}</dt>
              <dd className='break-all'>{context.action}</dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Request ID')}</dt>
              <dd className='flex items-center gap-1 font-mono break-all'>
                {props.draft.request_id}
                <CopyButton
                  value={props.draft.request_id}
                  aria-label={t('Copy request ID')}
                />
              </dd>
            </div>
            {context.currency && (
              <div>
                <dt className='text-muted-foreground'>
                  {t('Original amount')}
                </dt>
                <dd>
                  <Money
                    minor={context.amount_minor}
                    currency={context.currency}
                    exponent={exponent}
                  />
                </dd>
              </div>
            )}
            <div>
              <dt className='text-muted-foreground'>{t('Quota')}</dt>
              <dd>
                <Quota value={context.quota} />
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Target')}</dt>
              <dd className='break-all'>
                {String(
                  prepared?.summary.target_label ??
                    `${context.target_type} ${context.target_id}`
                )}
              </dd>
            </div>
            <div>
              <dt className='text-muted-foreground'>{t('Policy version')}</dt>
              <dd>{context.policy_version}</dd>
            </div>
            {context.currency && (
              <div>
                <dt className='text-muted-foreground'>{t('Fee')}</dt>
                <dd>
                  <Money
                    minor={prepared?.summary.fee_minor ?? '0'}
                    currency={context.currency}
                    exponent={exponent}
                  />
                </dd>
              </div>
            )}
            {context.currency && (
              <div>
                <dt className='text-muted-foreground'>{t('Net amount')}</dt>
                <dd>
                  <Money
                    minor={prepared?.summary.net_minor ?? '0'}
                    currency={context.currency}
                    exponent={exponent}
                  />
                </dd>
              </div>
            )}
            {prepared?.summary.recipient_label && (
              <div>
                <dt className='text-muted-foreground'>{t('Recipient')}</dt>
                <dd className='break-all'>
                  {prepared.summary.recipient_label}
                </dd>
              </div>
            )}
          </dl>
        )}
        {props.draft.review && (
          <dl className='grid grid-cols-1 gap-3 text-sm sm:grid-cols-2'>
            {props.draft.review.map((entry) => (
              <div key={entry.label}>
                <dt className='text-muted-foreground'>{t(entry.label)}</dt>
                <dd className='break-words'>
                  {entry.before !== undefined && (
                    <>
                      <span>{entry.before}</span>
                      <span aria-label={t('changes to')}> → </span>
                    </>
                  )}
                  {entry.after}
                </dd>
              </div>
            ))}
          </dl>
        )}
        {uncertain && (
          <Alert variant='destructive'>
            <AlertTitle>{t('Do not submit a new request')}</AlertTitle>
            <AlertDescription className='flex flex-col gap-3'>
              <p>
                {t(
                  'The response was interrupted. The operation may have been recorded. Check this request before doing anything else; unknown payments remain held.'
                )}
              </p>
              <Button
                variant='outline'
                disabled={recovery.isPending}
                onClick={() => recovery.mutate()}
              >
                {t('Check request status')}
              </Button>
              {notFound && (
                <p>
                  {t(
                    'No matching record is visible yet. Keep this request ID and contact finance. Do not create a replacement payment.'
                  )}
                </p>
              )}
            </AlertDescription>
          </Alert>
        )}
      </ConfirmDialog>
      <SecureVerificationDialog {...verification.dialogProps} />
    </>
  )
}
