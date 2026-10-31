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
import { zodResolver } from '@hookform/resolvers/zod'
import { useEffect, useId, useRef, useState } from 'react'
import {
  useForm,
  useFormContext,
  type ControllerRenderProps,
} from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
  FormDescription,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

export interface ActionField {
  name: string
  label: string
  kind?:
    | 'text'
    | 'integer'
    | 'minor'
    | 'checkbox'
    | 'select'
    | 'textarea'
    | 'datetime'
    | 'csv'
  description?: string
  placeholder?: string
  required?: boolean
  mustBeTrue?: boolean
  validate?: (value: string | boolean) => string | undefined
  readOnly?: boolean
  min?: number
  max?: number
  options?: { value: string; label: string; updates?: Record<string, string> }[]
}
export type ActionValues = Record<string, string | boolean>

/** Business forms share the project's Form and Dialog behavior, not their authorization. */
export function ActionForm(props: {
  title: string
  description?: string
  fields: ActionField[]
  initial?: ActionValues
  onSubmit: (values: ActionValues) => Promise<void> | void
  onClose: () => void
  submitLabel?: string
  children?: React.ReactNode
}) {
  const { t } = useTranslation()
  const formId = useId()
  const submitting = useRef(false)
  const [busy, setBusy] = useState(false)
  const schema = z
    .record(z.string(), z.union([z.string(), z.boolean()]))
    .superRefine((values, context) => {
      for (const field of props.fields) {
        const value = values[field.name]
        const text = typeof value === 'string' ? value.trim() : ''
        let invalid =
          field.required !== false && field.kind !== 'checkbox' && !text
        if (field.kind === 'integer' && text) {
          invalid ||=
            !/^\d+$/.test(text) ||
            !Number.isSafeInteger(Number(text)) ||
            Number(text) < (field.min ?? 0) ||
            Number(text) > (field.max ?? Number.MAX_SAFE_INTEGER)
        }
        if (field.kind === 'minor' && text) {
          invalid ||= !/^(0|[1-9]\d*)$/.test(text)
          if (!invalid) {
            invalid ||=
              BigInt(text) > 9223372036854775807n ||
              BigInt(text) < BigInt(field.min ?? 0)
          }
        }
        if (field.kind === 'select' && text) {
          invalid ||= !field.options?.some((option) => option.value === text)
        }
        if (field.kind === 'datetime' && text) {
          invalid ||= !Number.isFinite(Date.parse(text))
        }
        if (field.kind !== 'integer') {
          invalid ||= text.length > (field.max ?? 255)
        }
        if (field.mustBeTrue && value !== true) invalid = true
        const customError = field.validate?.(value ?? '')
        if (invalid || customError) {
          context.addIssue({
            code: 'custom',
            path: [field.name],
            message:
              customError ??
              t('Enter a valid value for {{field}}', {
                field: t(field.label),
              }),
          })
        }
      }
    })
  const defaults = Object.fromEntries(
    props.fields.map((field) => [
      field.name,
      props.initial?.[field.name] ?? (field.kind === 'checkbox' ? false : ''),
    ])
  )
  const form = useForm<ActionValues>({
    resolver: zodResolver(schema),
    defaultValues: defaults,
  })
  const submit = async (values: ActionValues) => {
    if (submitting.current) return
    submitting.current = true
    setBusy(true)
    try {
      await props.onSubmit(values)
    } catch (error) {
      handleServerError(error)
    } finally {
      submitting.current = false
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      title={props.title}
      description={props.description}
      onOpenChange={(open) => {
        if (!open && !busy) props.onClose()
      }}
      showCloseButton={!busy}
      footer={
        <>
          <Button variant='outline' disabled={busy} onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          <Button type='submit' form={formId} disabled={busy}>
            {props.submitLabel ?? t('Continue')}
          </Button>
        </>
      }
    >
      {props.children}
      <Form {...form}>
        <form
          id={formId}
          onSubmit={form.handleSubmit(submit)}
          className='flex flex-col gap-4'
        >
          {props.fields.map((definition) => (
            <FormField
              key={definition.name}
              control={form.control}
              name={definition.name}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t(definition.label)}</FormLabel>
                  <FormControl>
                    <ActionControl
                      definition={definition}
                      field={field}
                      busy={busy}
                    />
                  </FormControl>
                  {definition.description && (
                    <FormDescription>
                      {t(definition.description)}
                    </FormDescription>
                  )}
                  <FormMessage />
                </FormItem>
              )}
            />
          ))}
        </form>
      </Form>
    </Dialog>
  )
}

function ActionControl({
  definition,
  field,
  busy,
  ...controlProps
}: {
  definition: ActionField
  field: ControllerRenderProps<ActionValues>
  busy: boolean
}) {
  const { t } = useTranslation()
  const { setValue } = useFormContext<ActionValues>()
  const fileRead = useRef(0)
  useEffect(
    () => () => {
      fileRead.current += 1
    },
    []
  )
  if (definition.kind === 'checkbox') {
    return (
      <Checkbox
        {...controlProps}
        checked={field.value === true}
        onCheckedChange={(value) => field.onChange(value === true)}
        onBlur={field.onBlur}
        name={field.name}
        ref={field.ref}
        disabled={busy}
      />
    )
  }
  if (definition.kind === 'select') {
    return (
      <NativeSelect
        {...controlProps}
        value={String(field.value)}
        onChange={(event) => {
          field.onChange(event)
          const updates = definition.options?.find(
            (option) => option.value === event.target.value
          )?.updates
          for (const [name, value] of Object.entries(updates ?? {})) {
            setValue(name, value, { shouldValidate: true })
          }
        }}
        onBlur={field.onBlur}
        name={field.name}
        ref={field.ref}
        disabled={busy}
      >
        <NativeSelectOption value=''>
          {t('Select an option')}
        </NativeSelectOption>
        {definition.options?.map((option) => (
          <NativeSelectOption key={option.value} value={option.value}>
            {t(option.label)}
          </NativeSelectOption>
        ))}
      </NativeSelect>
    )
  }
  if (definition.kind === 'csv') {
    return (
      <div className='flex flex-col gap-3'>
        <Input
          type='file'
          accept='.csv,text/csv'
          aria-label={t('Choose statement CSV file')}
          disabled={busy}
          onChange={(event) => {
            const sequence = ++fileRead.current
            const file = event.target.files?.[0]
            event.target.value = ''
            if (!file) return
            if (file.size > 49152) {
              handleServerError(
                new Error(t('Statement CSV must not exceed 48 KiB.'))
              )
              return
            }
            void file
              .text()
              .then((text) => {
                if (sequence === fileRead.current) field.onChange(text)
              })
              .catch((error: unknown) => handleServerError(error))
          }}
        />
        <Textarea
          {...controlProps}
          {...field}
          onChange={(event) => {
            fileRead.current += 1
            field.onChange(event)
          }}
          value={String(field.value)}
          maxLength={49152}
          disabled={busy}
          className='min-h-48 font-mono text-xs'
        />
      </div>
    )
  }
  if (definition.kind === 'textarea') {
    return (
      <Textarea
        {...controlProps}
        {...field}
        value={String(field.value)}
        maxLength={definition.max ?? 255}
        disabled={busy}
      />
    )
  }
  return (
    <Input
      {...controlProps}
      {...field}
      value={String(field.value)}
      placeholder={definition.placeholder}
      type={definition.kind === 'datetime' ? 'datetime-local' : 'text'}
      inputMode={
        definition.kind === 'integer' || definition.kind === 'minor'
          ? 'numeric'
          : undefined
      }
      maxLength={
        definition.max && definition.kind !== 'integer' ? definition.max : 255
      }
      disabled={busy}
      readOnly={definition.readOnly}
      autoComplete='off'
    />
  )
}
