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
    <div
      className='grid grid-cols-2 gap-3 lg:grid-cols-4'
      aria-label={t('Agent quota budget')}
    >
      {values.map((item) => (
        <Card key={item.label} className='py-3'>
          <CardContent className='space-y-1 px-4'>
            <p className='text-muted-foreground text-sm'>{item.label}</p>
            <p className='text-lg font-semibold break-all tabular-nums'>
              {formatQuotaWithCurrency(item.value, { abbreviate: false })}
            </p>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
