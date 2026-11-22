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
import { Controller, useFormContext, useWatch } from 'react-hook-form'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { SettingsSwitchField } from '@/features/system-settings/components/settings-form-layout'

import { useFinanceLabels } from './i18n'
import { parseRate, type SettingsFormValues } from './settings-form'

export function InvoiceTypeFields(props: {
  kind: 'ordinary' | 'special'
  disabled: boolean
  paymentAvailable: boolean
}) {
  const { label } = useFinanceLabels()
  const form = useFormContext<SettingsFormValues>()
  const kind = props.kind
  const policy = useWatch({ control: form.control, name: kind })
  const activeFee =
    policy.enabled && policy.fee_enabled && (parseRate(policy.fee) ?? 0) > 0
  return (
    <fieldset
      className='min-w-0 space-y-2 rounded-lg border px-4 pt-1 pb-4'
      disabled={props.disabled}
    >
      <legend className='px-1 text-sm font-semibold'>
        {kind === 'ordinary'
          ? label('Ordinary invoice', '普通发票')
          : label('Special VAT invoice', '增值税专用发票')}
      </legend>
      {(['enabled', 'fee_enabled'] as const).map((key) => (
        <Controller
          key={key}
          control={form.control}
          name={`${kind}.${key}`}
          render={({ field }) => (
            <SettingsSwitchField
              controlId={`invoice-${kind}-${key}`}
              label={
                key === 'enabled'
                  ? label('Accept applications', '接受开票申请')
                  : label('Charge an invoice fee', '收取开票手续费')
              }
              checked={field.value}
              disabled={props.disabled}
              onCheckedChange={field.onChange}
            />
          )}
        />
      ))}
      <div className='grid grid-cols-1 gap-3 sm:grid-cols-2'>
        {(['fee', 'tax'] as const).map((key) => {
          const id = `invoice-${kind}-${key}`
          const error = form.formState.errors[kind]?.[key]
          return (
            <Field key={key} data-invalid={!!error}>
              <FieldLabel htmlFor={id}>
                {key === 'fee'
                  ? label('Fee rate (%)', '手续费率（%）')
                  : label('Configured tax rate (%)', '配置税率（%）')}
              </FieldLabel>
              <Input
                id={id}
                inputMode='decimal'
                autoComplete='off'
                aria-invalid={!!error}
                aria-describedby={error ? `${id}-error` : undefined}
                {...form.register(`${kind}.${key}`)}
              />
              <FieldError id={`${id}-error`} errors={[error]} />
            </Field>
          )
        })}
      </div>
      {activeFee && !props.paymentAvailable && (
        <Alert variant='destructive'>
          <AlertDescription>
            {label(
              'Fee payment is not available. With these settings, new applications for this invoice type will be blocked. No funds will be deducted.',
              '暂不支持手续费支付。使用此设置后，该票种将暂停接受新申请，不会扣款。'
            )}
          </AlertDescription>
        </Alert>
      )}
    </fieldset>
  )
}
