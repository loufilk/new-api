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
import { ReceiptText } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { toIntlLocale } from '@/i18n/languages'

import { invoiceOrderKey } from '../lib/invoice-orders'
import type { CommerceConfig, InvoiceOrder } from '../types'
import { InvoiceMoney } from './application-status'

export function InvoiceOrderSelector(props: {
  config: CommerceConfig
  orders: InvoiceOrder[]
  selected: string[]
  disabled: boolean
  onChange: (keys: string[]) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const selectionLimit = props.config.invoice_max_orders
  const anchor = props.orders.find((order) =>
    props.selected.includes(invoiceOrderKey(order))
  )
  const selectAllAnchor = anchor ?? props.orders[0]
  const compatibleOrders = props.orders.filter(
    (order) =>
      order.currency === selectAllAnchor?.currency &&
      order.currency_exponent === selectAllAnchor?.currency_exponent
  )
  const allSelected =
    compatibleOrders.length > 0 &&
    compatibleOrders.every((order) =>
      props.selected.includes(invoiceOrderKey(order))
    )
  return (
    <Card className='rounded-3xl'>
      <CardHeader className='gap-2'>
        <div className='flex items-start justify-between gap-3'>
          <div className='min-w-0'>
            <CardTitle>{t('Eligible orders')}</CardTitle>
            <p className='text-muted-foreground mt-1 text-sm'>
              {t(
                'Paid orders from the last {{days}} days. Select up to {{count}} orders per application.',
                {
                  days: props.config.invoice_window_days,
                  count: selectionLimit,
                }
              )}
            </p>
          </div>
          <label className='flex shrink-0 cursor-pointer items-center gap-2 text-sm'>
            <Checkbox
              className='rounded-full'
              aria-label={t('Select all eligible orders')}
              checked={allSelected}
              indeterminate={!allSelected && props.selected.length > 0}
              disabled={props.disabled || !props.orders.length}
              onCheckedChange={() =>
                props.onChange(
                  allSelected || props.selected.length >= selectionLimit
                    ? []
                    : compatibleOrders
                        .slice(0, selectionLimit)
                        .map(invoiceOrderKey)
                )
              }
            />
            {t('Select all')}
          </label>
        </div>
      </CardHeader>
      <CardContent>
        {props.orders.length === 0 ? (
          <EmptyState
            icon={ReceiptText}
            className='min-h-40'
            title={t('No eligible orders')}
            description={t(
              'Paid orders with verified payment amounts will appear here.'
            )}
          />
        ) : (
          <ul className='max-h-[30rem] space-y-2 overflow-y-auto'>
            {props.orders.map((order) => {
              const key = invoiceOrderKey(order)
              const checked = props.selected.includes(key)
              return (
                <li key={key}>
                  <label className='hover:bg-muted/40 flex cursor-pointer items-center gap-3 rounded-[1.25rem] border p-3 transition-colors sm:p-4'>
                    <Checkbox
                      className='rounded-full'
                      aria-label={t('Select order {{number}}', {
                        number: order.trade_no,
                      })}
                      checked={checked}
                      disabled={
                        props.disabled ||
                        (!checked &&
                          (props.selected.length >= selectionLimit ||
                            (!!anchor &&
                              (order.currency !== anchor.currency ||
                                order.currency_exponent !==
                                  anchor.currency_exponent))))
                      }
                      onCheckedChange={(value) =>
                        props.onChange(
                          value
                            ? [...props.selected, key]
                            : props.selected.filter((item) => item !== key)
                        )
                      }
                    />
                    <div className='min-w-0 flex-1'>
                      <div className='flex flex-wrap items-center gap-x-2 gap-y-1'>
                        <span className='text-muted-foreground text-xs'>
                          {order.source_type === 'redemption'
                            ? t('Redemption code')
                            : t('Recharge')}
                        </span>
                        <span className='font-mono text-xs break-all'>
                          {order.trade_no}
                        </span>
                      </div>
                      <time
                        className='text-muted-foreground mt-1 block text-xs'
                        dateTime={new Date(
                          order.completed_at * 1000
                        ).toISOString()}
                      >
                        {new Date(order.completed_at * 1000).toLocaleString(
                          locale
                        )}
                      </time>
                    </div>
                    <span className='shrink-0 text-sm font-semibold'>
                      <InvoiceMoney
                        minor={order.amount_minor}
                        currency={order.currency}
                        exponent={order.currency_exponent}
                      />
                    </span>
                  </label>
                </li>
              )
            })}
          </ul>
        )}
        {props.selected.length >= selectionLimit &&
          props.orders.length > selectionLimit && (
            <p role='status' className='text-muted-foreground mt-3 text-xs'>
              {t(
                'The selection limit has been reached. Submit these orders before selecting more.'
              )}
            </p>
          )}
        {anchor && compatibleOrders.length < props.orders.length && (
          <p className='text-muted-foreground mt-3 text-xs'>
            {t(
              'Orders in a different currency or precision require a separate application.'
            )}
          </p>
        )}
      </CardContent>
    </Card>
  )
}
