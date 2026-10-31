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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { CURRENCY_EXPONENTS } from '../lib/currencies'
import type { OperationPayload } from '../types'
import { ResourceTable } from './resource-table'
import { BusinessDate, BusinessState, Money, Quota } from './values'
import { WorkflowOperation, type WorkflowAction } from './workflow-operation'

export type WorkflowRow = OperationPayload & { id: number }
export interface WorkflowColumn {
  key: string
  label: string
  kind?:
    | 'money'
    | 'quota'
    | 'date'
    | 'state'
    | 'boolean'
    | 'integer'
    | 'allocations'
  currencyKey?: string
  exponentKey?: string
  catalogExponent?: boolean
}
export function WorkflowValue(props: {
  row: OperationPayload
  column: WorkflowColumn
}) {
  const { t, i18n } = useTranslation()
  const value = props.column.key
    .split('.')
    .reduce<import('../types').OperationValue | undefined>(
      (parent, key) =>
        parent && typeof parent === 'object' && !Array.isArray(parent)
          ? parent[key]
          : undefined,
      props.row
    )
  if (value === null || value === undefined) return <span>—</span>
  if (props.column.kind === 'allocations' && Array.isArray(value)) {
    return (
      <ul className='space-y-1'>
        {value.map((entry) =>
          entry && typeof entry === 'object' && !Array.isArray(entry) ? (
            <li key={String(entry.receivable_id)}>
              #{String(entry.receivable_id)}:{' '}
              <Money
                minor={String(entry.amount_minor)}
                currency={String(props.row.currency ?? '')}
                exponent={Number(props.row.currency_exponent)}
              />
            </li>
          ) : null
        )}
      </ul>
    )
  }
  if (props.column.kind === 'money') {
    return (
      <Money
        minor={String(value)}
        currency={String(
          props.row[props.column.currencyKey ?? 'currency'] ?? ''
        )}
        exponent={
          props.column.catalogExponent
            ? CURRENCY_EXPONENTS[
                String(props.row[props.column.currencyKey ?? 'currency'])
              ]
            : Number(props.row[props.column.exponentKey ?? 'currency_exponent'])
        }
      />
    )
  }
  if (props.column.kind === 'quota') return <Quota value={Number(value)} />
  if (props.column.kind === 'date') {
    return <BusinessDate value={Number(value)} />
  }
  if (props.column.kind === 'state') {
    return <BusinessState value={String(value)} />
  }
  if (props.column.kind === 'boolean') {
    return <span>{value === true ? t('Yes') : t('No')}</span>
  }
  if (props.column.kind === 'integer' && typeof value === 'number') {
    return (
      <span className='tabular-nums'>
        {formatNumber(
          value,
          toIntlLocale(i18n.resolvedLanguage || i18n.language)
        )}
      </span>
    )
  }
  if (typeof value === 'object') return <span>—</span>
  return <span className='break-words'>{String(value)}</span>
}
export function WorkflowTable(props: {
  resource: string
  columns: WorkflowColumn[]
  actions?: (row: WorkflowRow) => WorkflowAction[]
  create?: WorkflowAction[]
  filters?: Record<string, string | number | undefined>
  disabled?: boolean
  rows?: WorkflowRow[]
  onInspect?: (row: WorkflowRow) => void
  renderActions?: (row: WorkflowRow) => React.ReactNode
}) {
  const { t } = useTranslation()
  const [operation, setOperation] = useState<WorkflowAction | null>(null)
  const columns: ColumnDef<WorkflowRow>[] = props.columns.map(
    (column, index) => ({
      accessorKey: column.key,
      header: t(column.label),
      meta: { mobileTitle: index === 0, mobileBadge: column.kind === 'state' },
      cell: ({ row }) => <WorkflowValue row={row.original} column={column} />,
    })
  )
  if (props.actions || props.onInspect || props.renderActions) {
    columns.push({
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <div className='flex flex-wrap gap-2'>
          {props.renderActions?.(row.original)}
          {props.onInspect && (
            <Button
              size='sm'
              variant='outline'
              onClick={() => props.onInspect?.(row.original)}
            >
              {t('View details')}
            </Button>
          )}
          {props.actions?.(row.original).map((action) => (
            <Button
              key={action.action}
              size='sm'
              variant='outline'
              onClick={() => setOperation(action)}
            >
              {t(action.label)}
            </Button>
          ))}
        </div>
      ),
    })
  }
  return (
    <div className='flex flex-col gap-4'>
      {!!props.create?.length && (
        <div className='flex flex-wrap gap-2'>
          {props.create.map((action) => (
            <Button
              key={action.action}
              disabled={props.disabled}
              onClick={() => setOperation(action)}
            >
              {t(action.label)}
            </Button>
          ))}
        </div>
      )}
      <ResourceTable
        resource={props.resource}
        columns={columns}
        filters={props.filters}
        rows={props.rows}
      />
      {operation && (
        <WorkflowOperation
          operation={operation}
          resource={props.resource}
          onClose={() => setOperation(null)}
        />
      )}
    </div>
  )
}
