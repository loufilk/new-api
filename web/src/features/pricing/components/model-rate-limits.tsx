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

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { PricingModel } from '../types'
import { CatalogGroupBadge } from './catalog-group-badge'

export function ModelRateLimits(props: {
  model: PricingModel
  selectedGroup?: string
}) {
  const { t, i18n } = useTranslation()
  const model = props.model
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const formatLimit = (n: number) =>
    n === 0 ? t('Unlimited') : formatNumber(n, locale)
  // Pricing is display-only for visitors. Do not reveal group limits on public links.
  const groups = model.group_keys_are_display_only ? [] : model.enable_groups
  const rows = groups.flatMap((group) => {
    if (
      props.selectedGroup &&
      groups.includes(props.selectedGroup) &&
      group !== props.selectedGroup
    ) {
      return []
    }
    const limit = model.group_rate_limits?.[group]
    if (
      !limit ||
      !['group', 'shared'].includes(limit.scope) ||
      !Number.isSafeInteger(limit.window_minutes) ||
      limit.window_minutes < 1 ||
      !Number.isSafeInteger(limit.total_requests) ||
      limit.total_requests < 0 ||
      !Number.isSafeInteger(limit.successful_requests) ||
      limit.successful_requests < 0
    ) {
      return []
    }
    return [{ group, limit }]
  })
  return (
    <section className='space-y-2'>
      <h3 className='text-sm font-semibold'>{t('Rate limits')}</h3>
      {rows.length ? (
        <>
          <p className='text-muted-foreground text-sm'>
            {t('Request limits are per account and shared across models.')}
          </p>
          <div className='grid gap-2 sm:grid-cols-2'>
            {rows.map(({ group, limit }) => (
              <div className='space-y-2 rounded-lg border p-3' key={group}>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <CatalogGroupBadge
                    group={group}
                    catalog={model.group_catalog}
                  />
                  <span className='text-muted-foreground text-xs'>
                    {limit.scope === 'shared'
                      ? t('Shared across groups')
                      : t('Per group')}
                  </span>
                </div>
                <dl className='space-y-1 text-sm'>
                  <div className='flex justify-between gap-2'>
                    <dt>{t('Time window for rate limiting')}</dt>
                    <dd>
                      {formatNumber(limit.window_minutes, locale)}{' '}
                      {t('minutes')}
                    </dd>
                  </div>
                  <div className='flex justify-between gap-2'>
                    <dt>{t('All requests')}</dt>
                    <dd>{formatLimit(limit.total_requests)}</dd>
                  </div>
                  <div className='flex justify-between gap-2'>
                    <dt>{t('Successful requests')}</dt>
                    <dd>{formatLimit(limit.successful_requests)}</dd>
                  </div>
                </dl>
              </div>
            ))}
          </div>
        </>
      ) : (
        <p className='text-muted-foreground text-sm'>{t('pool.notReported')}</p>
      )}
    </section>
  )
}
