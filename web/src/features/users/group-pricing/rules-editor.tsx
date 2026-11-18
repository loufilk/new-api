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
import { useId } from 'react'
import {
  Controller,
  useFieldArray,
  useWatch,
  type UseFormReturn,
} from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { GroupPricingFormValues } from './lib/schema'
import type { UserGroupPricingGroup } from './types'

export function GroupPricingRulesEditor(props: {
  form: UseFormReturn<GroupPricingFormValues>
  groups: UserGroupPricingGroup[]
  disabled: boolean
  canUpdate: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  // Multipliers may be much smaller than 0.01; preserve their precision.
  const ratioFormat = new Intl.NumberFormat(locale, {
    maximumSignificantDigits: 12,
  })
  const inputId = useId()
  const fields = useFieldArray({ control: props.form.control, name: 'rules' })
  const rules = useWatch({ control: props.form.control, name: 'rules' })
  const groups = props.groups.filter((group) => group.name !== 'auto')

  return (
    <div className='flex min-w-0 flex-col gap-4'>
      {fields.fields.length === 0 && (
        <EmptyState
          title={t('Uses default group pricing')}
          description={t(
            'Add a rule to set a personal multiplier for one billing group.'
          )}
          className='min-h-28'
        />
      )}
      {fields.fields.map((field, index) => {
        const rule = rules[index]
        const group = groups.find((entry) => entry.name === rule?.group)
        const ratio = Number(rule?.ratio)
        const validRatio =
          rule?.ratio.trim() !== '' && Number.isFinite(ratio) && ratio >= 0
        const relative =
          group && group.base_ratio > 0 && validRatio
            ? (ratio / group.base_ratio) * 100
            : null
        const errors = props.form.formState.errors.rules?.[index]
        const groupId = `${inputId}-${field.id}-group`
        const ratioId = `${inputId}-${field.id}-ratio`
        return (
          <FieldSet
            key={field.id}
            aria-label={t('Pricing rule {{number}}', { number: index + 1 })}
            className='border-border/60 min-w-0 rounded-lg border p-3'
            disabled={props.disabled}
          >
            <FieldLegend variant='label'>
              {t('Pricing rule {{number}}', { number: index + 1 })}
            </FieldLegend>
            <div className='grid min-w-0 gap-3 sm:grid-cols-2'>
              <Field
                data-invalid={!!errors?.group}
                data-disabled={props.disabled}
                className='min-w-0'
              >
                <FieldLabel htmlFor={groupId}>{t('Billing group')}</FieldLabel>
                <Controller
                  control={props.form.control}
                  name={`rules.${index}.group`}
                  render={({ field: groupField }) => (
                    <Combobox
                      id={groupId}
                      options={groups
                        .filter(
                          (entry) =>
                            entry.name === groupField.value ||
                            !rules.some((item) => item.group === entry.name)
                        )
                        .map((entry) => ({
                          value: entry.name,
                          label: entry.name,
                          description: entry.description,
                        }))}
                      value={groupField.value}
                      onValueChange={(value) =>
                        groupField.onChange(value ?? '')
                      }
                      onBlur={groupField.onBlur}
                      ref={groupField.ref}
                      disabled={props.disabled}
                      placeholder={t('Select a billing group')}
                      aria-invalid={!!errors?.group}
                      aria-describedby={`${groupId}-error`}
                      className='w-full'
                    />
                  )}
                />
                <FieldError id={`${groupId}-error`}>
                  {errors?.group?.message && t(errors.group.message)}
                </FieldError>
              </Field>
              <Field
                data-invalid={!!errors?.ratio}
                data-disabled={props.disabled}
                className='min-w-0'
              >
                <FieldLabel htmlFor={ratioId}>
                  {t('Personal multiplier')}
                </FieldLabel>
                <Input
                  id={ratioId}
                  {...props.form.register(`rules.${index}.ratio`)}
                  type='number'
                  inputMode='decimal'
                  step='any'
                  min={0}
                  disabled={props.disabled}
                  placeholder={group ? String(group.base_ratio) : '0.4'}
                  aria-invalid={!!errors?.ratio}
                  aria-describedby={`${ratioId}-error`}
                />
                <FieldError id={`${ratioId}-error`}>
                  {errors?.ratio?.message && t(errors.ratio.message)}
                </FieldError>
              </Field>
            </div>
            {group && (
              <>
                <dl className='bg-muted/40 grid grid-cols-2 gap-2 rounded-md p-3 text-xs'>
                  <div>
                    <dt className='text-muted-foreground'>
                      {t('Default group multiplier')}
                    </dt>
                    <dd className='mt-1 font-medium'>
                      {ratioFormat.format(group.base_ratio)}×
                    </dd>
                  </div>
                  <div>
                    <dt className='text-muted-foreground'>
                      {t('Effective multiplier')}
                    </dt>
                    <dd className='mt-1 font-medium'>
                      {validRatio ? `${ratioFormat.format(ratio)}×` : '—'}
                      {validRatio && ratio === 0 && (
                        <Badge variant='secondary' className='ml-2'>
                          {t('Free')}
                        </Badge>
                      )}
                    </dd>
                  </div>
                  {relative !== null && Number.isFinite(relative) && (
                    <div className='col-span-2'>
                      <dt className='sr-only'>
                        {t('Relative to default pricing')}
                      </dt>
                      <dd>
                        {t('{{percent}}% of the default charge', {
                          percent: formatNumber(relative, locale),
                        })}
                      </dd>
                    </div>
                  )}
                </dl>
                {!group.available && (
                  <FieldDescription>
                    {t(
                      'This user currently cannot call this group. Pricing does not grant group access.'
                    )}
                  </FieldDescription>
                )}
              </>
            )}
            {props.canUpdate && (
              <Button
                type='button'
                size='sm'
                variant='ghost'
                className='self-start'
                disabled={props.disabled}
                onClick={() => fields.remove(index)}
              >
                {t('Restore default pricing')}
              </Button>
            )}
          </FieldSet>
        )
      })}
      {props.canUpdate && (
        <Button
          type='button'
          variant='outline'
          className='self-start'
          disabled={
            props.disabled ||
            groups.length === 0 ||
            rules.length >= groups.length
          }
          onClick={() => fields.append({ group: '', ratio: '' })}
        >
          {t('Add group pricing')}
        </Button>
      )}
    </div>
  )
}
