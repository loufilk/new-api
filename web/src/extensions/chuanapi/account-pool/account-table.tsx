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

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Badge } from '@/components/ui/badge'
import { Progress } from '@/components/ui/progress'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { PoolAccount } from './types'

export function AccountTable(props: { accounts: PoolAccount[] }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const columns: StaticDataTableColumn<PoolAccount>[] = [
    {
      id: 'account',
      header: t('pool.account'),
      cell: (row) => (
        <div className='flex flex-col gap-1'>
          <strong>{row.anonymous_id}</strong>
          <span className='text-muted-foreground text-xs'>
            {row.provider} · {row.auth_type} / {row.plan}
          </span>
        </div>
      ),
    },
    {
      id: 'status',
      header: t('Status'),
      cell: (row) => (
        <Badge variant={row.status === 'available' ? 'secondary' : 'outline'}>
          {t(`pool.status.${row.status}`)}
        </Badge>
      ),
    },
    {
      id: 'capacity',
      header: t('pool.concurrency'),
      cell: (row) => (
        <span className='tabular-nums'>
          {row.concurrency === null
            ? '—'
            : formatNumber(row.concurrency, locale)}{' '}
          /{' '}
          {row.concurrency_limit === null
            ? '—'
            : formatNumber(row.concurrency_limit, locale)}
        </span>
      ),
    },
    {
      id: 'groups',
      header: t('pool.group'),
      cell: (row) => (
        <div className='flex max-w-64 flex-wrap gap-1'>
          {row.groups.map((group) => (
            <Badge
              key={group.name}
              variant='outline'
              className='max-w-full break-all whitespace-normal'
            >
              {group.name} · {formatNumber(group.ratio, locale)}×
            </Badge>
          ))}
        </div>
      ),
    },
    {
      id: '5h',
      header: t('pool.usage5h'),
      cell: (row) => (
        <UsageProgress value={row.usage_5h_percent} label={t('pool.usage5h')} />
      ),
    },
    {
      id: '7d',
      header: t('pool.usage7d'),
      cell: (row) => (
        <UsageProgress value={row.usage_7d_percent} label={t('pool.usage7d')} />
      ),
    },
    {
      id: 'last',
      header: t('pool.lastUsed'),
      cell: (row) =>
        row.last_used_at
          ? new Date(row.last_used_at * 1000).toLocaleString(locale)
          : '—',
    },
  ]
  return (
    <StaticDataTable
      columns={columns}
      data={props.accounts}
      getRowKey={(row) => row.anonymous_id}
      containerProps={{
        role: 'region',
        'aria-label': t('pool.accounts'),
        tabIndex: 0,
      }}
      className='max-w-full overflow-x-auto'
      tableClassName='min-w-[820px]'
    />
  )
}

function UsageProgress(props: { value: number | null; label: string }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  if (props.value === null || !Number.isFinite(props.value)) {
    return <span title={t('pool.notReported')}>—</span>
  }
  const value = Math.max(0, Math.min(100, props.value))
  return (
    <div className='flex min-w-20 flex-col gap-1'>
      <span className='text-xs tabular-nums'>
        {formatNumber(value, locale)}%
      </span>
      <Progress value={value} aria-label={props.label} />
    </div>
  )
}
