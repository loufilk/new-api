import { useTranslation } from 'react-i18next'

import { Card, CardContent } from '@/components/ui/card'
import { formatQuotaWithCurrency } from '@/lib/currency'

import type { AgentAccount } from '../types'

export function QuotaSummary(props: { account: AgentAccount }) {
  const { t } = useTranslation()
  const values = [
    { label: t('Available quota'), value: props.account.available_quota },
    { label: t('Reserved quota'), value: props.account.reserved_quota },
    { label: t('Redeemed quota'), value: props.account.spent_quota },
    { label: t('Allocated quota'), value: props.account.allocated_quota },
  ]
  return (
    <div className='grid grid-cols-2 gap-3 lg:grid-cols-4' aria-label={t('Agent quota budget')}>
      {values.map((item) => (
        <Card key={item.label} className='py-3'>
          <CardContent className='space-y-1 px-4'>
            <p className='text-muted-foreground text-sm'>{item.label}</p>
            <p className='break-all text-lg font-semibold tabular-nums'>{formatQuotaWithCurrency(item.value, { abbreviate: false })}</p>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
