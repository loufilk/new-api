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
import { CircleCheck, Hourglass, ReceiptText } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Card, CardContent } from '@/components/ui/card'

import type { InvoiceSummary } from '../types'
import { InvoiceMoney } from './application-status'

export function InvoiceSummaryCards(props: { summary: InvoiceSummary }) {
  const { t } = useTranslation()
  const cards = [
    {
      key: 'available' as const,
      title: t('Available invoice amount'),
      icon: ReceiptText,
      color: 'bg-sky-50 text-sky-600 dark:bg-sky-950 dark:text-sky-300',
    },
    {
      key: 'processing' as const,
      title: t('Processing invoice amount'),
      icon: Hourglass,
      color: 'bg-teal-50 text-teal-600 dark:bg-teal-950 dark:text-teal-300',
    },
    {
      key: 'issued' as const,
      title: t('Invoiced amount'),
      icon: CircleCheck,
      color:
        'bg-violet-50 text-violet-600 dark:bg-violet-950 dark:text-violet-300',
    },
  ]
  return (
    <div className='grid gap-3 sm:grid-cols-3'>
      {cards.map((card) => (
        <Card key={card.key} className='rounded-3xl sm:min-h-40'>
          <CardContent>
            <div className='text-muted-foreground flex items-center gap-2 text-xs'>
              <span className={`rounded-xl p-2 ${card.color}`}>
                <card.icon aria-hidden className='size-4' />
              </span>
              {card.title}
            </div>
            <p className='mt-3 text-[1.4rem] font-semibold tracking-tight'>
              <InvoiceMoney
                minor={props.summary[card.key].amount_minor}
                currency={props.summary.currency}
                exponent={props.summary.currency_exponent}
              />
            </p>
            <p className='text-muted-foreground mt-1.5 text-xs'>
              {t('{{count}} records', { count: props.summary[card.key].count })}
            </p>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
