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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { getApiKeyScopeModels } from '@/features/keys/api'
import { ApiKeyGroupHealth } from '@/features/keys/components/api-key-group-health'
import { GroupRatioBadge } from '@/features/keys/components/auto-group-visuals'
import type { TokenGroupDetail } from '@/features/keys/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { requireServerSuccess } from '@/lib/server-error-message'
import { formatDateTimeObject } from '@/lib/time'

export function GroupDetailDialog(props: {
  group: TokenGroupDetail
  windowStart?: number
  windowEnd?: number
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const models = useQuery({
    queryKey: ['channel-status', 'models', props.group.name],
    queryFn: async () =>
      requireServerSuccess(
        await getApiKeyScopeModels({
          group: props.group.name,
          smart_routing: false,
          auto_groups_mode: 'custom',
          auto_groups: [],
        })
      ).data,
    staleTime: 60_000,
  })
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={props.group.name}
      description={
        props.group.description ||
        t('Group availability and actual request outcomes')
      }
      contentClassName='sm:max-w-3xl'
      bodyClassName='space-y-5'
      footer={
        <Button variant='outline' render={<Link to='/intelligence' />}>
          {t('View quality history')}
        </Button>
      }
    >
      <div className='grid grid-cols-2 gap-3'>
        <div className='bg-muted/40 space-y-2 rounded-xl p-4'>
          <p className='text-muted-foreground text-xs'>
            {t('Your effective multiplier')}
          </p>
          <GroupRatioBadge ratio={props.group.ratio} />
        </div>
        <div className='bg-muted/40 space-y-2 rounded-xl p-4'>
          <p className='text-muted-foreground text-xs'>
            {t('Recorded attempts')}
          </p>
          <p className='text-xl font-semibold tabular-nums'>
            {props.group.request_count == null
              ? '—'
              : formatNumber(props.group.request_count, locale)}
          </p>
        </div>
      </div>
      <section className='space-y-3 rounded-xl border p-4'>
        <h3 className='font-medium'>{t('Success rate by hour')}</h3>
        <ApiKeyGroupHealth detail={props.group} />
        {props.windowStart && props.windowEnd ? (
          <p className='text-muted-foreground text-xs'>
            {formatDateTimeObject(new Date(props.windowStart * 1000))} —{' '}
            {formatDateTimeObject(new Date(props.windowEnd * 1000))}
          </p>
        ) : null}
        <p className='text-muted-foreground text-xs leading-relaxed'>
          {t(
            'Each bar is one hour. Gray means no recorded sample. Results are weighted by request attempts, including retries.'
          )}
        </p>
      </section>
      <section className='space-y-3'>
        <div className='flex items-center gap-2'>
          <h3 className='font-medium'>{t('Available models')}</h3>
          {models.data && (
            <Badge variant='secondary'>
              {formatNumber(models.data.length, locale)}
            </Badge>
          )}
        </div>
        {models.isPending && <LoadingState />}
        {models.isError && <ErrorState onRetry={() => void models.refetch()} />}
        {models.data && models.data.length === 0 && (
          <EmptyState
            title={t('No models available in this group')}
            className='min-h-32'
          />
        )}
        <div className='flex max-h-64 flex-wrap gap-2 overflow-y-auto'>
          {models.data?.map((model) => (
            <Badge
              key={model}
              variant='outline'
              className='max-w-full break-all whitespace-normal'
            >
              {model}
            </Badge>
          ))}
        </div>
      </section>
    </Dialog>
  )
}
