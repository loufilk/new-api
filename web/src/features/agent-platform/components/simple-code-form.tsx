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
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { useRequestId } from '@/features/agent-platform/hooks/use-request-id'
import type { Redemption } from '@/features/redemption-codes/types'

import {
  formatAgentCash,
  formatAgentQuota as formatQuota,
  parseAgentQuota,
} from '../lib/money'
import { createSimpleCode, type SimpleAgentBalance } from '../simple-api'
import { ActionForm, type ActionField } from './action-form'

export function SimpleCodeForm(props: {
  balance: SimpleAgentBalance
  onClose: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const requestId = useRequestId()
  const [created, setCreated] = useState<Redemption | null>(null)
  const fields: ActionField[] = [
    { name: 'name', label: 'Name', kind: 'text', max: 80 },
    {
      name: 'quota',
      label: 'Quota',
      kind: 'text',
      validate: (value) =>
        (parseAgentQuota(String(value)) ?? 0) > 0
          ? undefined
          : t('Enter a valid amount'),
    },
  ]
  if (created) {
    return (
      <Dialog
        open
        title={t('Redemption code created')}
        description={t('Copy the code and deliver it to your customer.')}
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
        footer={<Button onClick={props.onClose}>{t('Done')}</Button>}
      >
        <div className='flex min-w-0 items-center gap-2 rounded-md border p-3'>
          <code className='min-w-0 flex-1 text-sm break-all'>
            {created.key}
          </code>
          <CopyButton
            value={created.key}
            aria-label={t('Copy redemption code')}
          />
        </div>
        <p className='text-muted-foreground text-xs'>
          {t(
            'The code is visible in your redemption code records until it is redeemed.'
          )}
        </p>
      </Dialog>
    )
  }
  return (
    <ActionForm
      title={t('Create redemption code')}
      description={t(
        'Agent balance is used first. Any shortfall automatically reserves available credit; repayment is due only after redemption.'
      )}
      fields={fields}
      onClose={props.onClose}
      onSubmit={async (values) => {
        const quota = parseAgentQuota(String(values.quota)) ?? 0
        if (!Number.isSafeInteger(quota) || quota <= 0) {
          throw new Error(t('Invalid redemption code request'))
        }
        const creditFace = Math.max(0, quota - props.balance.available_quota)
        const cost = Number((BigInt(creditFace) * 9n + 9n) / 10n)
        if (cost > props.balance.available_credit_quota) {
          throw new Error(t('Insufficient available credit'))
        }
        const payload = {
          name: String(values.name).trim(),
          quota,
          mode: 'direct' as const,
        }
        const result = await createSimpleCode({
          request_id: requestId(payload),
          ...payload,
          expires_at: 0,
        })
        toast.success(t('Redemption code created'))
        await client.invalidateQueries({ queryKey: ['agent-platform'] })
        await client.invalidateQueries({ queryKey: ['redemptions'] })
        setCreated(result)
      }}
      submitLabel={t('Create code')}
    >
      <div className='bg-muted/50 rounded-md border p-3 text-sm'>
        <div className='font-medium'>{t('Available agent balance')}</div>
        <div className='mt-1 tabular-nums'>
          {formatQuota(props.balance.available_quota)}
        </div>
        <div className='mt-3 font-medium'>{t('Available credit')}</div>
        <div className='mt-1 tabular-nums'>
          {formatAgentCash(props.balance.available_credit_quota)}
        </div>
      </div>
    </ActionForm>
  )
}
