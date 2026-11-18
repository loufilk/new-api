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

import { StaticDataTable } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { FieldDescription, FieldGroup } from '@/components/ui/field'
import { toIntlLocale } from '@/i18n/languages'

import type { GroupPricingFormValues } from './lib/schema'
import type { UserGroupPricing } from './types'

export function GroupPricingConflictComparison(props: {
  latest: UserGroupPricing
  draft: GroupPricingFormValues['rules']
  disabled: boolean
  onUseLatest: (keepDraft: boolean) => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const ratioFormat = new Intl.NumberFormat(locale, {
    maximumSignificantDigits: 12,
  })
  const groups = [
    ...new Set([
      ...Object.keys(props.latest.ratios),
      ...props.draft.map((rule) => rule.group),
    ]),
  ].filter(Boolean)
  return (
    <FieldGroup className='border-border/60 rounded-lg border p-3'>
      <h4 className='text-sm font-medium'>{t('Compare pricing changes')}</h4>
      <FieldDescription>
        {t(
          'Your draft is preserved. Choose the latest saved pricing or keep your draft for another review before saving.'
        )}
      </FieldDescription>
      <StaticDataTable
        data={groups}
        getRowKey={(group) => group}
        className='max-h-64 overflow-auto'
        columns={[
          { id: 'group', header: t('Billing group'), cell: (group) => group },
          {
            id: 'draft',
            header: t('Your draft'),
            cell: (group) => {
              const rule = props.draft.find((entry) => entry.group === group)
              if (!rule) return t('Default pricing')
              const ratio = Number(rule.ratio)
              if (!rule.ratio.trim() || !Number.isFinite(ratio)) {
                return t('Invalid multiplier')
              }
              return `${ratioFormat.format(ratio)}×`
            },
          },
          {
            id: 'saved',
            header: t('Latest saved pricing'),
            cell: (group) =>
              Object.hasOwn(props.latest.ratios, group)
                ? `${ratioFormat.format(props.latest.ratios[group])}×`
                : t('Default pricing'),
          },
        ]}
      />
      <div className='flex flex-wrap gap-2'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={props.disabled}
          onClick={() => props.onUseLatest(false)}
        >
          {t('Use latest saved pricing')}
        </Button>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={props.disabled}
          onClick={() => props.onUseLatest(true)}
        >
          {t('Keep my draft for review')}
        </Button>
      </div>
    </FieldGroup>
  )
}
