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
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { useAuthStore } from '@/stores/auth-store'

import { getPlatform, postPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import { parseMinorMoney } from '../lib/money'
import type {
  CommissionAccount,
  CommissionRecipient,
  OperationDraft,
  PlatformPage,
} from '../types'
import { ActionForm, type ActionField } from './action-form'

export function CommissionTransfer(props: {
  target: 'withdrawal' | 'wallet' | 'procurement'
  accounts: CommissionAccount[]
  onPrepared: (draft: OperationDraft) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const requestId = useRequestId()
  const recipients = useQuery({
    queryKey: ['agent-platform', userId, 'recipient-options'],
    queryFn: ({ signal }) =>
      getPlatform<PlatformPage<CommissionRecipient>>(
        '/commission-recipients',
        { p: 1, page_size: 100, current_only: 'true' },
        signal
      ),
    enabled: props.target === 'withdrawal',
  })
  if (
    props.target === 'withdrawal' &&
    (recipients.isPending || recipients.isError)
  ) {
    return (
      <Dialog
        open
        title={t('Select payout recipient')}
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
      >
        {recipients.isPending ? (
          <LoadingState />
        ) : (
          <ErrorState
            onRetry={() => {
              void recipients.refetch()
            }}
          />
        )}
      </Dialog>
    )
  }
  const currentRecipients = new Map<string, CommissionRecipient>()
  for (const recipient of recipients.data?.items ?? []) {
    if (
      (currentRecipients.get(recipient.id)?.version ?? 0) < recipient.version
    ) {
      currentRecipients.set(recipient.id, recipient)
    }
  }
  const fields: ActionField[] = [
    {
      name: 'currency',
      label: 'Original currency',
      kind: 'select',
      options: props.accounts.map((account) => ({
        value: account.currency,
        label: account.currency,
      })),
    },
    { name: 'amount', label: 'Amount in original currency' },
  ]
  if (props.target === 'withdrawal') {
    fields.push({
      name: 'recipient',
      label: 'Payout recipient version',
      kind: 'select',
      options: [...currentRecipients.values()].map((recipient) => ({
        value: `${recipient.id}:${recipient.version}`,
        label: `${recipient.label} · ${recipient.reference_masked} · v${recipient.version}`,
      })),
    })
  }
  return (
    <ActionForm
      title={
        props.target === 'withdrawal'
          ? t('Request commission withdrawal')
          : t('Convert available commission')
      }
      description={t(
        'Only available commission can move. Risk reserves, pending maturity, existing holds and recovery debt cannot be bypassed by a conversion.'
      )}
      fields={fields}
      onClose={props.onClose}
      onSubmit={async (values) => {
        const account = props.accounts.find(
          (entry) => entry.currency === values.currency
        )
        if (!account) {
          throw new Error(t('Select an available commission currency.'))
        }
        const amount = parseMinorMoney(
          String(values.amount),
          account.currency_exponent
        )
        if (!amount) {
          throw new Error(
            t('Enter an exact positive amount in the original currency.')
          )
        }
        const recipient = [...currentRecipients.values()].find(
          (entry) => `${entry.id}:${entry.version}` === values.recipient
        )
        if (props.target === 'withdrawal' && !recipient) {
          throw new Error(t('Select the current payout recipient version.'))
        }
        const payload = {
          target: props.target,
          currency: account.currency,
          amount_minor: amount,
          recipient_id: recipient?.id ?? '',
          recipient_version: recipient?.version ?? 0,
        }
        const id = requestId(payload)
        const quote = await postPlatform<{ quote_id: string }>(
          props.target === 'withdrawal'
            ? '/withdrawals/quote'
            : '/commissions/conversions/quote',
          { ...payload, request_id: id }
        )
        props.onPrepared({
          action:
            props.target === 'withdrawal'
              ? 'commission.withdraw.request'
              : `commission.convert.${props.target}`,
          request_id: id,
          payload: { quote_id: quote.quote_id },
          endpoint:
            props.target === 'withdrawal'
              ? '/withdrawals'
              : '/commissions/conversions',
          resource:
            props.target === 'withdrawal'
              ? '/withdrawals'
              : '/commissions/conversions',
        })
      }}
    />
  )
}
