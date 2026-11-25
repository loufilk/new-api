import { Trash2 } from 'lucide-react'
import { Controller, type Control, type UseFormRegister } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
  FieldLegend,
  FieldSet,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import { QuoteModelTags } from './quote-model-tags'
import type { PartnerApplicationFormValues } from './schema'

type QuoteError = Partial<
  Record<'platform' | 'group' | 'rate' | 'models' | 'description', string>
>

export function QuoteFields(props: {
  index: number
  control: Control<PartnerApplicationFormValues>
  platforms: { value: string; label: string }[]
  register: UseFormRegister<PartnerApplicationFormValues>
  errors?: QuoteError
  disabled: boolean
  canRemove: boolean
  onRemove: () => void
}) {
  const { t } = useTranslation()
  const groupName = `quotes.${props.index}.group` as const
  const platformName = `quotes.${props.index}.platform` as const
  const rateName = `quotes.${props.index}.rate` as const
  const modelsName = `quotes.${props.index}.models` as const
  const descriptionName = `quotes.${props.index}.description` as const
  return (
    <FieldSet className='min-w-0 gap-3 rounded-lg border p-4'>
      <FieldLegend variant='label'>
        {t('Quote {{number}}', { number: props.index + 1 })}
      </FieldLegend>
      <FieldGroup className='grid min-w-0 items-start gap-3 sm:grid-cols-2 xl:grid-cols-[minmax(0,1.1fr)_minmax(0,1fr)_minmax(7.5rem,0.65fr)_auto]'>
        <Field data-invalid={Boolean(props.errors?.group)}>
          <FieldLabel htmlFor={`partner-group-${props.index}`} required>
            {t('Group name')}
          </FieldLabel>
          <Input
            id={`partner-group-${props.index}`}
            {...props.register(groupName)}
            required
            maxLength={120}
            disabled={props.disabled}
            aria-invalid={Boolean(props.errors?.group)}
          />
          <FieldError>
            {props.errors?.group ? t(props.errors.group) : undefined}
          </FieldError>
        </Field>
        <Field data-invalid={Boolean(props.errors?.platform)}>
          <FieldLabel htmlFor={`partner-platform-${props.index}`} required>
            {t('Platform')}
          </FieldLabel>
          <Controller
            control={props.control}
            name={platformName}
            render={({ field }) => (
              <Combobox
                id={`partner-platform-${props.index}`}
                options={props.platforms}
                value={field.value}
                ref={field.ref}
                onBlur={field.onBlur}
                onValueChange={field.onChange}
                disabled={props.disabled}
                aria-invalid={Boolean(props.errors?.platform)}
              />
            )}
          />
          <FieldError>
            {props.errors?.platform ? t(props.errors.platform) : undefined}
          </FieldError>
        </Field>
        <Field data-invalid={Boolean(props.errors?.rate)}>
          <FieldLabel htmlFor={`partner-rate-${props.index}`} required>
            {t('Multiplier')}
          </FieldLabel>
          <Input
            id={`partner-rate-${props.index}`}
            {...props.register(rateName)}
            type='number'
            min='0'
            max='1000'
            step='0.000001'
            required
            disabled={props.disabled}
            aria-invalid={Boolean(props.errors?.rate)}
          />
          <FieldDescription>
            {t('Use a multiplier from 0 to 1000.')}
          </FieldDescription>
          <FieldError>
            {props.errors?.rate ? t(props.errors.rate) : undefined}
          </FieldError>
        </Field>
        <div className='xl:mt-6'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='text-muted-foreground hover:text-destructive'
            disabled={props.disabled || !props.canRemove}
            onClick={props.onRemove}
          >
            <Trash2 aria-hidden='true' />
            {t('Remove group')}
          </Button>
        </div>
        <Field
          className='sm:col-span-full'
          data-invalid={Boolean(props.errors?.models)}
        >
          <FieldLabel htmlFor={`partner-models-${props.index}`}>
            {t('Main models')}
          </FieldLabel>
          <Controller
            control={props.control}
            name={modelsName}
            render={({ field }) => (
              <QuoteModelTags
                id={`partner-models-${props.index}`}
                disabled={props.disabled}
                invalid={Boolean(props.errors?.models)}
                field={field}
              />
            )}
          />
          <FieldDescription id={`partner-models-${props.index}-hint`}>
            {t('Press Enter to add a model.')}
          </FieldDescription>
          <FieldError id={`partner-models-${props.index}-error`}>
            {props.errors?.models ? t(props.errors.models) : undefined}
          </FieldError>
        </Field>
        <Field
          className='sm:col-span-full'
          data-invalid={Boolean(props.errors?.description)}
        >
          <FieldLabel htmlFor={`partner-quote-description-${props.index}`}>
            {t('Group description')}
          </FieldLabel>
          <Textarea
            id={`partner-quote-description-${props.index}`}
            {...props.register(descriptionName)}
            maxLength={500}
            disabled={props.disabled}
            rows={2}
            aria-invalid={Boolean(props.errors?.description)}
          />
          <FieldError>
            {props.errors?.description
              ? t(props.errors.description)
              : undefined}
          </FieldError>
        </Field>
      </FieldGroup>
    </FieldSet>
  )
}
