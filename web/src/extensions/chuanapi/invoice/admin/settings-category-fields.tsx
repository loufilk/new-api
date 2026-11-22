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
import { useRef } from 'react'
import { useController, useFormContext } from 'react-hook-form'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { SettingsSwitchField } from '@/features/system-settings/components/settings-form-layout'

import type { InvoiceCategory } from '../contracts'
import { useFinanceLabels } from './i18n'
import type { SettingsFormValues } from './settings-form'

export function InvoiceCategoryFields(props: {
  disabled: boolean
  savedCategories?: InvoiceCategory[]
}) {
  const { label } = useFinanceLabels()
  const {
    control,
    formState: { errors },
  } = useFormContext<SettingsFormValues>()
  const { field } = useController({ name: 'categories', control })
  const categories = field.value
  const disabled = props.disabled || props.savedCategories === undefined
  const focusNew = useRef<string | null>(null)
  function update(
    index: number,
    change: Partial<Pick<InvoiceCategory, 'enabled' | 'name'>>
  ) {
    field.onChange(
      categories.map((item, i) => (i === index ? { ...item, ...change } : item))
    )
  }
  function add() {
    if (disabled || categories.length >= 30) return
    const bytes = crypto.getRandomValues(new Uint8Array(8))
    const id = `cat_${Array.from(bytes, (byte) =>
      byte.toString(16).padStart(2, '0')
    ).join('')}`
    focusNew.current = id
    field.onChange([...categories, { id, name: '', enabled: true }])
  }
  return (
    <fieldset
      className='min-w-0 space-y-3 rounded-lg border px-4 pt-1 pb-4'
      disabled={disabled}
    >
      <legend className='px-1 text-sm font-semibold'>
        {label('Invoice categories', '开票项目')}
      </legend>
      {props.savedCategories === undefined && (
        <p className='text-muted-foreground text-sm'>
          {label(
            'Update the server to edit invoice categories.',
            '更新服务端后即可编辑开票项目。'
          )}
        </p>
      )}
      {categories.map((item, index) => {
        const id = `invoice-category-${item.id}`
        const error = errors.categories?.[index]?.name
        const message =
          error?.message === 'Invoice category names must be unique.'
            ? label(
                'Invoice category names must be unique.',
                '开票项目名称不能重复。'
              )
            : label(
                'Use 1–60 characters without control characters.',
                '请输入 1 至 60 个字符，不能含控制字符。'
              )
        return (
          <div
            key={item.id}
            className='grid min-w-0 gap-x-4 gap-y-1 border-b pb-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end'
          >
            <Field data-invalid={!!error}>
              <FieldLabel htmlFor={id}>
                {label('Category Name', '项目名称')} {index + 1}
              </FieldLabel>
              <Input
                id={id}
                value={item.name}
                onBlur={field.onBlur}
                autoComplete='off'
                aria-invalid={!!error}
                aria-describedby={error ? `${id}-error` : undefined}
                ref={(input) => {
                  if (input && focusNew.current === item.id) {
                    focusNew.current = null
                    input.focus()
                  }
                }}
                onChange={(event) =>
                  update(index, { name: event.target.value })
                }
              />
              {error && <FieldError id={`${id}-error`}>{message}</FieldError>}
            </Field>
            <div className='flex items-center gap-3'>
              <SettingsSwitchField
                disabled={disabled}
                controlId={`${id}-enabled`}
                checked={item.enabled}
                label={`${label('Enabled', '启用')} ${index + 1}`}
                onCheckedChange={(enabled) => update(index, { enabled })}
              />
              {props.savedCategories &&
                !props.savedCategories.some(
                  (saved) => saved.id === item.id
                ) && (
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    aria-label={`${label('Cancel', '取消')} ${index + 1}`}
                    onClick={() =>
                      field.onChange(
                        categories.filter((row) => row.id !== item.id)
                      )
                    }
                  >
                    {label('Cancel', '取消')}
                  </Button>
                )}
            </div>
          </div>
        )
      })}
      {!categories.some((item) => item.enabled) && (
        <Alert>
          <AlertDescription>
            {label(
              'If no category is enabled, customers cannot apply for an invoice.',
              '未启用任何开票项目时，客户无法申请开票。'
            )}
          </AlertDescription>
        </Alert>
      )}
      <div className='flex flex-wrap items-center gap-2'>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={disabled || categories.length >= 30}
          onClick={add}
        >
          {label('Add invoice category', '添加开票项目')}
        </Button>
        <span className='text-muted-foreground text-xs'>
          {label(
            'Up to 30 invoice categories are allowed.',
            '最多可设置 30 个开票项目。'
          )}
        </span>
      </div>
    </fieldset>
  )
}
