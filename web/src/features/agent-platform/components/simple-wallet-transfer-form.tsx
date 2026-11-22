/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { extensionWalletPage } from '@/extensions'
import { useAuthStore } from '@/stores/auth-store'

import { formatAgentQuota, parseAgentQuota } from '../lib/money'
import { isWalletTransferRejected, transferWalletToAgent } from '../simple-api'
import {
  useWalletTransferStore,
  walletTransferVersion,
  WalletTransferChanged,
} from '../stores/wallet-transfer-store'
import { ActionForm } from './action-form'

type Props = { userId: number; onClose: () => void }
export function SimpleWalletTransferForm(props: Props) {
  return <WalletTransferSession key={props.userId} {...props} />
}

function WalletTransferSession(props: Props) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const store = useWalletTransferStore()
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    const unsubscribe = useAuthStore.subscribe((state, previous) => {
      if (state.auth.user?.id !== previous.auth.user?.id) alive.current = false
    })
    return () => {
      alive.current = false
      unsubscribe()
    }
  }, [])
  const current = () =>
    alive.current && useAuthStore.getState().auth.user?.id === props.userId
  const [snapshot, setSnapshot] = useState(() => {
    try {
      return store.read(props.userId)
    } catch {
      return null
    }
  })
  const [generation, setGeneration] = useState(0)
  const pending = snapshot?.status === 'pending' ? snapshot : null
  const wallet = useQuery({
    queryKey: ['wallet', 'summary', props.userId],
    queryFn: () => extensionWalletPage().loadSummary(),
  })
  const transfer = useMutation({
    mutationFn: transferWalletToAgent,
    retry: false,
    meta: { errorToast: false },
  })
  const title = t('Transfer wallet balance to agent balance')
  if (!pending && (wallet.isPending || wallet.isError || !wallet.data)) {
    return (
      <Dialog
        open
        title={title}
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
      >
        {wallet.isPending ? (
          <LoadingState />
        ) : (
          <ErrorState onRetry={() => void wallet.refetch()} />
        )}
      </Dialog>
    )
  }
  return (
    <ActionForm
      key={generation}
      title={title}
      description={t(
        'Only paid wallet balance can be transferred, at 1:1. No extra discount or commission applies. Unused funds can only return to the original wallet, never as cash.'
      )}
      initial={{ amount: pending?.amount ?? '' }}
      fields={[
        {
          name: 'amount',
          label: 'Amount ($)',
          kind: 'text',
          max: 24,
          readOnly: !!pending,
          validate: (value) => {
            if (pending) return undefined
            const quota = parseAgentQuota(String(value))
            return quota !== null &&
              quota > 0 &&
              quota <= (wallet.data?.paid ?? 0)
              ? undefined
              : t('Enter a valid amount within your paid wallet balance.')
          },
        },
      ]}
      onClose={props.onClose}
      onSubmit={async (values) => {
        if (!current()) return
        const quota = parseAgentQuota(String(values.amount))
        if (
          !pending &&
          (quota === null || quota <= 0 || quota > (wallet.data?.paid ?? 0))
        ) {
          throw new Error(
            t('Enter a valid amount within your paid wallet balance.')
          )
        }
        try {
          const intent = await store.begin(
            props.userId,
            walletTransferVersion(snapshot),
            quota ?? 0,
            String(values.amount)
          )
          if (!current()) return
          setSnapshot(intent)
          await transfer
            .mutateAsync({
              request_id: intent.request_id,
              quota: intent.quota,
              expected_user_id: props.userId,
            })
            .catch(async (error: unknown) => {
              if (isWalletTransferRejected(error)) {
                const rejected = await store.finish(
                  props.userId,
                  intent.request_id,
                  'rejected'
                )
                if (current()) {
                  const latest = store.read(props.userId)
                  if (!rejected) throw new WalletTransferChanged(latest)
                  setSnapshot(latest)
                }
              }
              throw error
            })
          const settled = await store.finish(
            props.userId,
            intent.request_id,
            'completed'
          )
          if (!current()) return
          if (!settled) {
            throw new WalletTransferChanged(store.read(props.userId))
          }
          // Refetch: a replay response may contain an older account snapshot.
          await Promise.all([
            client.invalidateQueries({ queryKey: ['agent-platform'] }),
            client.invalidateQueries({ queryKey: ['wallet'] }),
            client.invalidateQueries({ queryKey: ['simple-root'] }),
          ])
          if (current()) props.onClose()
        } catch (error) {
          if (!current()) return
          if (error instanceof WalletTransferChanged) {
            setSnapshot(error.current)
            setGeneration((value) => value + 1)
          }
          throw error
        }
      }}
      submitLabel={pending ? t('Retry transfer') : t('Transfer')}
    >
      <p className='mb-3 text-sm'>
        {t('Paid wallet balance')}:{' '}
        {wallet.data ? formatAgentQuota(wallet.data.paid) : '—'}
      </p>
      {pending && (
        <p role='status' className='text-muted-foreground mb-3 text-sm'>
          {t(
            'A transfer is awaiting confirmation. Retry the same transfer to check its result. Closing this dialog does not cancel a submitted transfer.'
          )}
        </p>
      )}
    </ActionForm>
  )
}
