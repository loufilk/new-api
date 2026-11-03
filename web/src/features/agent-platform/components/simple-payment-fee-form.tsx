import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'

import { getPlatform, postPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import { parseMinorMoney } from '../lib/money'
import type { SimplePaymentMethod } from '../simple-api'
import { ActionForm } from './action-form'

export function SimplePaymentFeeForm(props: { onClose: () => void }) {
  const { t } = useTranslation()
  const identify = useRequestId()
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['simple-root', 'payment-fees'],
    queryFn: () => getPlatform<SimplePaymentMethod[]>('/simple/payment-fees'),
  })
  if (query.isPending) return <LoadingState />
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />
  return (
    <ActionForm
      title={t('Payment channel fees')}
      description={t(
        'Confirm the actual channel fee before enabling customer payment codes. This fee is included in the minimum sale price.'
      )}
      fields={[
        {
          name: 'method',
          label: 'Payment method',
          kind: 'select',
          options: query.data.map((method) => ({
            value: method.payment_method,
            label: method.payment_method,
          })),
        },
        {
          name: 'fee',
          label: 'Channel fee (%)',
          kind: 'text',
          validate: (value) => {
            const bps =
              String(value) === '0' ? '0' : parseMinorMoney(String(value), 2)
            return bps !== null && BigInt(bps) < 10_000n
              ? undefined
              : t('Enter a valid percentage below 100.')
          },
        },
        {
          name: 'enabled',
          label: 'Enable customer payment codes',
          kind: 'checkbox',
        },
        { name: 'reason', label: 'Reason', kind: 'textarea', max: 255 },
        {
          name: 'confirmed',
          label: 'I confirm this change and its financial impact',
          kind: 'checkbox',
          mustBeTrue: true,
        },
      ]}
      initial={{ method: query.data[0]?.payment_method ?? '', enabled: true }}
      onClose={props.onClose}
      onSubmit={async (values) => {
        const payload = {
          payment_method: String(values.method),
          fee_bps: Number(
            String(values.fee) === '0'
              ? '0'
              : parseMinorMoney(String(values.fee), 2)
          ),
          enabled: values.enabled === true,
          reason: String(values.reason),
        }
        await postPlatform('/simple/payment-fees', {
          request_id: identify(payload),
          ...payload,
        })
        await client.invalidateQueries({
          queryKey: ['simple-root', 'payment-fees'],
        })
        await client.invalidateQueries({
          queryKey: ['agent-platform', 'simple-payment-methods'],
        })
        props.onClose()
      }}
      submitLabel={t('Save')}
    />
  )
}
