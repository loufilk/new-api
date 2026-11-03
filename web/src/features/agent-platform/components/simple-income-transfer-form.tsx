import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { postPlatform } from '../api'
import { useRequestId } from '../hooks/use-request-id'
import { formatAgentQuota, parseAgentQuota } from '../lib/money'
import { ActionForm } from './action-form'

export function SimpleIncomeTransferForm(props: {
  availableQuota: number
  onClose: () => void
}) {
  const { t } = useTranslation()
  const identify = useRequestId()
  const client = useQueryClient()
  return (
    <ActionForm
      title={t('Transfer payment income to agent balance')}
      description={t(
        'Transferred income can be used for codes or moved to your wallet during exit settlement. It cannot be refunded as cash.'
      )}
      fields={[
        {
          name: 'amount',
          label: 'Amount ($)',
          kind: 'text',
          validate: (value) => {
            const quota = parseAgentQuota(String(value))
            return quota !== null && quota > 0 && quota <= props.availableQuota
              ? undefined
              : t('Enter a valid amount')
          },
        },
      ]}
      onClose={props.onClose}
      onSubmit={async (values) => {
        const quota = parseAgentQuota(String(values.amount))
        if (quota === null || quota <= 0) {
          throw new Error(t('Enter a valid amount'))
        }
        const payload = {
          quota,
          reason: 'Transfer payment income to agent balance',
        }
        await postPlatform('/simple/payment/transfer', {
          request_id: identify(payload),
          ...payload,
        })
        await client.invalidateQueries({ queryKey: ['agent-platform'] })
        await client.invalidateQueries({ queryKey: ['wallet'] })
        props.onClose()
      }}
      submitLabel={t('Transfer')}
    >
      <p className='mb-3 text-sm'>
        {t('Payment income available')}:{' '}
        {formatAgentQuota(props.availableQuota)}
      </p>
    </ActionForm>
  )
}
