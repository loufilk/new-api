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
import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'

import type { TestRun } from './types'

export function TestHistoryList(props: {
  runs: TestRun[]
  onSelect: (run: TestRun) => void
  onCancel: (id: string) => void
  cancelling: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  if (!props.runs.length) {
    return (
      <EmptyState title={t('intelligence.noHistory')} className='min-h-32' />
    )
  }
  const columns: StaticDataTableColumn<TestRun>[] = [
    {
      id: 'model',
      header: t('Model'),
      cell: (run) => (
        <span className='break-all'>
          {run.model} / {run.group}
        </span>
      ),
    },
    {
      id: 'status',
      header: t('Status'),
      cell: (run) => (
        <Badge variant='outline'>
          {t(`intelligence.status.${run.status}`)}
        </Badge>
      ),
    },
    {
      id: 'time',
      header: t('Time'),
      cell: (run) => new Date(run.created_at * 1000).toLocaleString(locale),
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: (run) => (
        <div className='flex gap-2'>
          <Button variant='outline' onClick={() => props.onSelect(run)}>
            {t('Details')}
          </Button>
          {run.status === 'running' && (
            <Button
              variant='outline'
              disabled={props.cancelling || run.cancel_requested}
              onClick={() => props.onCancel(run.id)}
            >
              {run.cancel_requested
                ? t('intelligence.cancelRequested')
                : t('Cancel')}
            </Button>
          )}
        </div>
      ),
    },
  ]
  return (
    <StaticDataTable
      columns={columns}
      data={props.runs}
      getRowKey={(run) => run.id}
      containerProps={{
        role: 'region',
        'aria-label': t('intelligence.myHistory'),
        tabIndex: 0,
      }}
      className='max-w-full overflow-x-auto'
    />
  )
}
