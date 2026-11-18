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
import { Zap, ExternalLink, Gauge } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  getLatencyColorClass,
  openExternalSpeedTest,
} from '@/features/dashboard/lib/api-info'
import type { ApiInfoItem, PingStatus } from '@/features/dashboard/types'
import { getBgColorClass } from '@/lib/colors'
import { cn } from '@/lib/utils'

interface ApiInfoItemProps {
  item: ApiInfoItem
  status: PingStatus
  onTest: (url: string) => void
}

export function ApiInfoItemComponent(props: ApiInfoItemProps) {
  const { t } = useTranslation()
  const item = props.item
  const status = props.status

  return (
    <div className='group hover:bg-muted/30 space-y-2.5 px-4 py-3 transition-colors sm:px-5'>
      <div className='flex items-start justify-between gap-3'>
        <div className='flex min-w-0 items-start gap-2'>
          <span
            className={cn(
              'mt-1.5 inline-block size-2 shrink-0 rounded-full',
              getBgColorClass(item.color)
            )}
          />
          <span className='min-w-0 text-sm leading-relaxed font-semibold break-words'>
            {item.route}
          </span>
        </div>
        <div className='flex shrink-0 items-center'>
          {status.testing && (
            <StatusBadge
              label={t('Testing...')}
              variant='warning'
              className='animate-pulse'
              copyable={false}
            />
          )}
          {status.latency !== null && !status.testing && (
            <StatusBadge
              variant='success'
              label={`${status.latency}${t('ms')}`}
              className={cn(
                'font-mono font-medium',
                getLatencyColorClass(status.latency)
              )}
              copyable={false}
            />
          )}
          {status.error && (
            <StatusBadge label={t('N/A')} variant='neutral' copyable={false} />
          )}
        </div>
      </div>

      {item.description && (
        <p className='text-muted-foreground text-xs leading-relaxed break-words'>
          {item.description}
        </p>
      )}
      <code className='bg-muted/50 text-foreground/80 block rounded-md px-2.5 py-2 text-xs leading-relaxed break-all'>
        {item.url}
      </code>

      <div className='flex flex-wrap items-center justify-end gap-1'>
        <Button
          variant='ghost'
          size='sm'
          onClick={() => props.onTest(item.url)}
          disabled={status.testing}
          className='h-7 gap-1.5 px-2'
          title={t('Test Latency')}
          aria-label={t('Test Latency')}
        >
          <Zap className={cn('size-3.5', status.testing && 'animate-pulse')} />
          <span>{t('Test Latency')}</span>
        </Button>

        <Button
          variant='ghost'
          size='sm'
          onClick={() => openExternalSpeedTest(item.url)}
          className='size-7 p-0'
          title={t('External Speed Test')}
          aria-label={t('External Speed Test')}
        >
          <Gauge className='size-3.5' />
        </Button>

        <CopyButton
          value={item.url}
          variant='ghost'
          size='sm'
          className='size-7 p-0'
          iconClassName='size-3.5'
          tooltip={t('Copy URL')}
          aria-label={t('Copy URL')}
        />

        <Button
          variant='ghost'
          size='sm'
          className='size-7 p-0'
          title={t('Open in New Tab')}
          aria-label={t('Open in New Tab')}
          render={<a href={item.url} target='_blank' rel='noreferrer' />}
        >
          <ExternalLink className='size-3.5' />
        </Button>
      </div>
    </div>
  )
}
