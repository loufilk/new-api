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
import { useTranslation } from 'react-i18next'

import { getPricing } from '@/features/pricing/api'
import { CatalogGroupBadge } from '@/features/pricing/components/catalog-group-badge'
import { GroupCatalogBadges } from '@/features/pricing/components/group-catalog-badges'
import { MarketGroupIcon } from '@/features/pricing/components/market-group-icon'
import { ModelPriceCell } from '@/features/pricing/components/model-price-cell'
import type { MarketGroup } from '@/features/pricing/types'
import { requireServerSuccess } from '@/lib/server-error-message'

export function GroupDisplayPreview(props: {
  group: string
  entry: MarketGroup
  scope: string
  baseRatio?: number
}) {
  const { t } = useTranslation()
  // Only the current signed-in viewer's quote is valid. No admin config,
  // inferred 1×, public projection or other user's cache can supply this price.
  const pricing = useQuery({
    queryKey: ['pricing', `user:${props.scope}`],
    queryFn: async ({ signal }) =>
      requireServerSuccess(await getPricing(signal)),
    staleTime: 0,
    retry: false,
    meta: { errorToast: false },
  })
  const data =
    !pricing.isError && !pricing.isFetching ? pricing.data : undefined
  const ratio = data?.group_ratio?.[props.group]
  const allowed =
    !data?.group_keys_are_display_only &&
    Object.hasOwn(data?.usable_group ?? {}, props.group) &&
    typeof ratio === 'number' &&
    Number.isFinite(ratio) &&
    ratio >= 0
  const model = allowed
    ? data?.data?.find((row) => row.enable_groups?.includes(props.group))
    : undefined
  const source = data?.group_rate_source?.[props.group]
  const name = props.entry.label?.trim() || props.group
  return (
    <section
      aria-label={t('Group display preview')}
      className='bg-muted/25 space-y-3 rounded-xl border p-4'
    >
      <h3 className='text-sm font-semibold'>{t('Group display preview')}</h3>
      <div className='flex flex-wrap items-center gap-1.5'>
        <span className='bg-background inline-flex max-w-full flex-wrap items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium'>
          <MarketGroupIcon meta={props.entry} />
          {props.entry.exclusive && (
            <span className='rounded bg-violet-700 px-1 text-xs text-white'>
              {t('Exclusive')}
            </span>
          )}
          <GroupCatalogBadges values={props.entry.badges} />
          <span className='break-all'>{name}</span>
          {allowed && (
            <span className='text-muted-foreground tabular-nums'>×{ratio}</span>
          )}
        </span>
      </div>
      {props.entry.description && (
        <p className='text-muted-foreground text-xs break-words'>
          {props.entry.description}
        </p>
      )}
      <div className='bg-background space-y-2 rounded-lg border p-3 text-sm'>
        <p className='text-muted-foreground text-xs'>
          {t('Quote for your signed-in account')}
        </p>
        {model && data ? (
          <div className='grid min-w-0 gap-3 sm:grid-cols-2'>
            <div className='min-w-0'>
              <p className='font-medium break-all'>{model.model_name}</p>
              <div className='mt-1 flex flex-wrap items-center gap-1.5'>
                <CatalogGroupBadge
                  group={props.group}
                  catalog={{ [props.group]: props.entry }}
                />
                <span className='text-muted-foreground text-xs'>
                  {t('Effective rate')}: {ratio}×
                </span>
              </div>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t('Price source')}:{' '}
                {source === 'personal' && t('Personal rate')}
                {source === 'group_override' && t('User group override')}
                {source === 'group' && t('Base group rate')}
                {!source && t('Unavailable')}
              </p>
            </div>
            <ModelPriceCell
              model={{ ...model, group_ratio: data.group_ratio }}
              options={{ selectedGroup: props.group }}
              showExpression={false}
            />
          </div>
        ) : (
          <p className='text-muted-foreground'>
            {pricing.isFetching
              ? t('Checking…')
              : t('No authorized quote available for this group.')}
          </p>
        )}
      </div>
      <p className='text-muted-foreground text-xs'>
        {t('Base group rate')}:{' '}
        {props.baseRatio === undefined ? '—' : `${props.baseRatio}×`}
        {' · '}
        {props.entry.exclusive
          ? t('Visible only to authorized accounts')
          : t('Visible according to existing group permissions')}
      </p>
    </section>
  )
}
