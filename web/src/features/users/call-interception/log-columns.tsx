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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'

import type { InterceptionLog, InterceptionLogKind } from './types'

export function useInterceptionLogColumns(
  kind: InterceptionLogKind
): ColumnDef<InterceptionLog>[] {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return useMemo(
    () => [
      {
        id: 'time',
        header: t('Time'),
        cell: ({ row }) => formatTimestampToDate(row.original.time),
      },
      {
        id: 'identity',
        header: kind === 'hits' ? t('Key ID') : t('Administrator ID'),
        cell: ({ row }) => {
          const record = row.original
          if ('token_id' in record) {
            return record.token_id === 0
              ? '—'
              : formatNumber(record.token_id, locale)
          }
          return formatNumber(record.actor_id, locale)
        },
      },
      ...(kind === 'hits'
        ? [
            {
              id: 'identity_kind',
              header: t('Request identity'),
              cell: ({ row }: { row: { original: InterceptionLog } }) => {
                const record = row.original
                if (!('identity_kind' in record)) return '—'
                let label = t('Unknown identity')
                switch (record.identity_kind) {
                  case 'api_key':
                    label = t('API key')
                    break
                  case 'dashboard_session':
                    label = t('Dashboard session')
                    break
                  case 'artifact_capability':
                    label = t('Artifact access capability')
                    break
                  case 'public_artifact':
                    label = t('Public artifact access')
                    break
                }
                const isResourceAccess =
                  record.identity_kind === 'artifact_capability' ||
                  record.identity_kind === 'public_artifact'
                return (
                  <div className='max-w-48 whitespace-normal'>
                    <p>{label}</p>
                    {record.actor_user_id > 0 && (
                      <p className='text-muted-foreground text-xs'>
                        {t('Caller user ID: {{id}}', {
                          id: formatNumber(record.actor_user_id, locale),
                        })}
                      </p>
                    )}
                    {isResourceAccess && (
                      <p className='text-muted-foreground text-xs'>
                        {t(
                          'Resource access does not prove a call by this user.'
                        )}
                      </p>
                    )}
                  </div>
                )
              },
            },
          ]
        : []),
      {
        id: 'request',
        header: t('Request ID'),
        cell: ({ row }) => (
          <div className='max-w-64 break-all whitespace-normal'>
            <code className='text-xs'>{row.original.request_id}</code>
            {'interface' in row.original && (
              <p className='text-muted-foreground text-xs'>
                {row.original.interface}
              </p>
            )}
          </div>
        ),
      },
      {
        id: 'result',
        header:
          kind === 'hits' ? t('Protocol / status') : t('Configuration change'),
        cell: ({ row }) => {
          const record = row.original
          if ('protocol' in record) {
            return (
              <div className='flex flex-col items-start gap-1'>
                <span>{record.protocol}</span>
                <Badge variant='outline'>{record.status}</Badge>
              </div>
            )
          }
          return (
            <div className='whitespace-normal'>
              <p>
                {record.previous_enabled ? t('Enabled') : t('Disabled')} →{' '}
                {record.enabled ? t('Enabled') : t('Disabled')}
              </p>
              <p className='text-muted-foreground text-xs'>
                {record.reply_changed
                  ? t('Reply changed')
                  : t('Reply unchanged')}
              </p>
            </div>
          )
        },
      },
      {
        id: 'revision',
        header: t('Revision'),
        cell: ({ row }) => formatNumber(row.original.revision, locale),
      },
    ],
    [t, locale, kind]
  )
}
