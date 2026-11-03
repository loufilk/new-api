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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { submitPaymentForm } from '@/lib/payment-form'

import { useRequestId } from '../hooks/use-request-id'
import { agentRepaymentAmount, parseMinorMoney } from '../lib/money'
import { getSimplePaymentMethods, requestSimpleRecharge } from '../simple-api'
import { ActionForm, type ActionField } from './action-form'

export function SimpleRechargeForm(props: {
  kind?: 'recharge' | 'repayment'
  amountDue?: number
  onClose: () => void
}) {
  const { t } = useTranslation()
  const identify = useRequestId()
  const kind = props.kind ?? 'recharge'
  const title =
    kind === 'repayment' ? t('Repay amount due') : t('Recharge agent balance')
  const info = useQuery({
    queryKey: ['agent-platform', 'simple-recharge-info', kind],
    queryFn: () => getSimplePaymentMethods(kind),
  })

  if (info.isPending) return <LoadingState />
  if (info.isError || !info.data) {
    return (
      <Dialog
        open
        title={title}
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
      >
        <ErrorState onRetry={() => void info.refetch()} />
      </Dialog>
    )
  }

  const methods = info.data.map((method) => ({
    type: method.payment_method,
    name: method.payment_method,
  }))
  const fields: ActionField[] = [
    {
      name: 'amount',
      label:
        kind === 'repayment'
          ? 'Repayment amount (CNY)'
          : 'Agent balance to add ($)',
      kind: 'text',
      max: 24,
      validate: (value) => {
        const minor = parseMinorMoney(String(value), 2)
        if (!minor || BigInt(minor) > 9007199254740991n) {
          return t('Enter a positive amount with at most two decimal places.')
        }
        return undefined
      },
      description:
        kind === 'repayment'
          ? 'Repay the debt in CNY, rounded up to the nearest fen. Any change is added to your agent balance.'
          : 'Pay 90% of the face value. Existing debt is repaid first; the remaining payment funds agent balance.',
    },
    {
      name: 'payment_method',
      label: 'Payment method',
      kind: 'select',
      options: methods.map((method) => ({
        value: method.type,
        label: method.name,
      })),
    },
  ]

  if (!methods.length) {
    return (
      <Dialog
        open
        title={title}
        description={t('No compatible online payment method is configured.')}
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
      >
        <p className='text-muted-foreground text-sm'>
          {t('Contact the root administrator to configure a payment method.')}
        </p>
      </Dialog>
    )
  }

  return (
    <ActionForm
      title={title}
      description={t(
        'This payment is separate from your ordinary wallet and does not earn referral rewards.'
      )}
      fields={fields}
      initial={{
        payment_method: methods[0].type,
        amount:
          kind === 'repayment' && props.amountDue
            ? agentRepaymentAmount(props.amountDue)
            : '',
      }}
      onClose={props.onClose}
      onSubmit={async (values) => {
        const payload = {
          amount: String(values.amount),
          kind,
          payment_method: String(values.payment_method),
        }
        const result = await requestSimpleRecharge({
          request_id: identify(payload),
          ...payload,
        })
        submitPaymentForm(result.url, result.data)
        props.onClose()
      }}
      submitLabel={t('Continue to payment')}
    />
  )
}
