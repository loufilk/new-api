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
