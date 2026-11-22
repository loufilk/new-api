import type { UseFormRegister } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'

import type { PartnerApplicationFormValues } from './schema'
import type { PartnerPlatform } from './types'

const platforms: Array<{ value: PartnerPlatform; label: string }> = [
  { value: 'openai', label: 'OpenAI' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'google', label: 'Google Gemini' },
  { value: 'deepseek', label: 'DeepSeek' },
  { value: 'alibaba', label: 'Alibaba Cloud' },
  { value: 'bytedance', label: 'ByteDance' },
  { value: 'zhipu', label: 'Zhipu' },
  { value: 'moonshot', label: 'Moonshot' },
  { value: 'other', label: 'Other' },
]

type QuoteError = Partial<
  Record<'platform' | 'group' | 'rate' | 'models' | 'description', string>
>

export function QuoteFields(props: {
  index: number
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
    <FieldSet className='min-w-0 border-t pt-4'>
      <FieldLegend variant='label'>
        {t('Quote {{number}}', { number: props.index + 1 })}
      </FieldLegend>
      <FieldGroup className='grid min-w-0 gap-3 sm:grid-cols-2'>
        <Field data-invalid={Boolean(props.errors?.platform)}>
          <FieldLabel htmlFor={`partner-platform-${props.index}`} required>
            {t('Platform')}
          </FieldLabel>
          <NativeSelect
            id={`partner-platform-${props.index}`}
            {...props.register(platformName)}
            required
            disabled={props.disabled}
            aria-invalid={Boolean(props.errors?.platform)}
          >
            {platforms.map((platform) => (
              <NativeSelectOption key={platform.value} value={platform.value}>
                {t(platform.label)}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <FieldError>
            {props.errors?.platform ? t(props.errors.platform) : undefined}
          </FieldError>
        </Field>
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
        <Field data-invalid={Boolean(props.errors?.rate)}>
          <FieldLabel htmlFor={`partner-rate-${props.index}`} required>
            {t('Rate')}
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
        <Field data-invalid={Boolean(props.errors?.models)}>
          <FieldLabel htmlFor={`partner-models-${props.index}`}>
            {t('Main models')}
          </FieldLabel>
          <Input
            id={`partner-models-${props.index}`}
            {...props.register(modelsName)}
            maxLength={500}
            disabled={props.disabled}
            aria-invalid={Boolean(props.errors?.models)}
          />
          <FieldError>
            {props.errors?.models ? t(props.errors.models) : undefined}
          </FieldError>
        </Field>
        <Field
          className='sm:col-span-2'
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
      {props.canRemove && (
        <div className='flex justify-end'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={props.disabled}
            onClick={props.onRemove}
          >
            {t('Remove group')}
          </Button>
        </div>
      )}
    </FieldSet>
  )
}
