import { X } from 'lucide-react'
import { useRef, useState } from 'react'
import type { ControllerRenderProps } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import {
  maxPartnerModelsLength,
  type PartnerApplicationFormValues,
} from './schema'

type ModelsField = ControllerRenderProps<
  PartnerApplicationFormValues,
  `quotes.${number}.models`
>

function combineModels(tags: readonly string[], draft: string = ''): string[] {
  const pending = draft
    .split(/[,，\n]/u)
    .map((name) => name.trim())
    .filter(Boolean)
  return [...new Set([...tags, ...pending])]
}

function serializeModels(models: string[]): string {
  const spaced = models.join(', ')
  // Keep lists that fit the original compact format valid at the boundary.
  return spaced.length <= maxPartnerModelsLength ? spaced : models.join(',')
}

// The application starts each row from one mounted useFieldArray item. Even
// text still in the input is immediately part of the existing wire value.
export function QuoteModelTags(props: {
  id: string
  field: ModelsField
  disabled: boolean
  invalid: boolean
}) {
  const { t } = useTranslation()
  const inputRef = useRef<HTMLInputElement>(null)
  const [models, setModels] = useState(() =>
    combineModels([], props.field.value)
  )
  const [draft, setDraft] = useState('')
  const describedBy = props.invalid
    ? `${props.id}-hint ${props.id}-error`
    : `${props.id}-hint`

  const editDraft = (value: string) => {
    setDraft(value)
    props.field.onChange(serializeModels(combineModels(models, value)))
  }
  const commitDraft = () => {
    const next = combineModels(models, draft)
    setModels(next)
    setDraft('')
    props.field.onChange(serializeModels(next))
  }
  const remove = (name: string) => {
    const next = models.filter((model) => model !== name)
    setModels(next)
    props.field.onChange(serializeModels(combineModels(next, draft)))
    inputRef.current?.focus()
  }

  return (
    <div className='border-input focus-within:border-ring focus-within:ring-ring/50 flex min-h-9 min-w-0 flex-wrap items-center gap-1.5 rounded-md border px-2 py-1 focus-within:ring-[3px]'>
      {models.map((name) => (
        <Badge
          key={name}
          variant='secondary'
          className='h-auto max-w-full gap-1 py-0.5 pr-0.5 whitespace-normal'
        >
          <span className='min-w-0 break-all'>{name}</span>
          <Button
            type='button'
            variant='ghost'
            size='icon-sm'
            className='size-5 shrink-0 rounded-sm p-0'
            aria-label={t('Remove {{name}}', { name })}
            disabled={props.disabled}
            onClick={() => remove(name)}
          >
            <X className='size-3' aria-hidden='true' />
          </Button>
        </Badge>
      ))}
      <Input
        id={props.id}
        ref={(element) => {
          inputRef.current = element
          props.field.ref(element)
        }}
        name={props.field.name}
        value={draft}
        disabled={props.disabled}
        enterKeyHint='done'
        aria-describedby={describedBy}
        aria-invalid={props.invalid}
        className='h-7 min-w-24 flex-1 border-0 px-1 shadow-none focus-visible:ring-0 aria-invalid:ring-0'
        onChange={(event) => editDraft(event.target.value)}
        onBlur={props.field.onBlur}
        onKeyDown={(event) => {
          if (event.nativeEvent.isComposing) return
          if (
            event.key === 'Enter' ||
            event.key === ',' ||
            event.key === '，'
          ) {
            event.preventDefault()
            commitDraft()
          }
        }}
      />
    </div>
  )
}
