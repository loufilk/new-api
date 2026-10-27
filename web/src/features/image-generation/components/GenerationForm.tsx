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
import { ImagePlus, SlidersHorizontal, Sparkles } from 'lucide-react'
import { useLayoutEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
  PopoverTitle,
} from '@/components/ui/popover'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import {
  IMAGE_COUNT_MAX,
  IMAGE_COUNT_MIN,
  IMAGE_PROMPT_EXAMPLE_KEYS,
} from '../constants'
import type { ImageApiKeyOption, ReferenceImageItem } from '../types'
import { ImageResolutionPresets } from './ImageResolutionPresets'
import { ReferenceUploader } from './ReferenceUploader'

const IMAGE_PARAMETER_LABEL_KEYS: Record<string, string> = {
  auto: 'Auto',
  low: 'Low',
  medium: 'Medium',
  high: 'High',
  standard: 'Standard',
  hd: 'HD',
  vivid: 'Vivid',
  natural: 'Natural',
}

export type GenerationFormValue = {
  prompt: string
  apiKeyId: number | null
  model: string
  size: string
  quality: string
  style: string
  count: number
}

type GenerationFormProps = {
  asyncMode?: boolean
  onAsyncModeChange?: (enabled: boolean) => void
  value: GenerationFormValue
  onChange: (patch: Partial<GenerationFormValue>) => void
  keys: ImageApiKeyOption[]
  models: string[]
  sizes: string[]
  qualities: string[]
  styles: string[]
  maxCount: number
  maxReferenceImages: number
  references: ReferenceImageItem[]
  referenceErrorKey: string | null
  onAddReferenceFiles: (files: File[]) => void
  onRemoveReference: (id: string) => void
  isGenerating: boolean
  formError: string | null
  isInitializing: boolean
  onGenerate: () => void
  onCancel: () => void
}

export function GenerationForm(props: GenerationFormProps) {
  const { t } = useTranslation()
  const [promptTouched, setPromptTouched] = useState(false)
  const [openParameter, setOpenParameter] = useState<string | null>(null)
  const promptRef = useRef<HTMLTextAreaElement>(null)
  useLayoutEffect(() => {
    const input = promptRef.current
    if (!input) return
    input.style.height = '120px'
    input.style.height = `${Math.min(240, Math.max(120, input.scrollHeight))}px`
  }, [props.value.prompt])
  const promptInvalid = props.value.prompt.trim() === ''
  const countLimit = Math.max(
    IMAGE_COUNT_MIN,
    Math.min(IMAGE_COUNT_MAX, props.maxCount)
  )
  const selectedApiKeyName = props.keys.find(
    (key) => key.id === props.value.apiKeyId
  )?.name
  const canGenerate =
    !props.isGenerating &&
    !props.isInitializing &&
    !promptInvalid &&
    props.value.apiKeyId !== null &&
    props.value.model !== ''

  return (
    <form
      className='bg-card min-w-0 rounded-lg border p-3 sm:p-4'
      onSubmit={(event) => {
        event.preventDefault()
        if (canGenerate) props.onGenerate()
      }}
    >
      <div className='grid min-w-0 gap-4'>
        <div className='grid min-w-0 gap-2'>
          <Label className='sr-only' htmlFor='image-prompt'>
            {t('Prompt')}
          </Label>
          <Textarea
            ref={promptRef}
            className='max-h-[240px] min-h-[120px] resize-none border-0 shadow-none focus-visible:ring-1'
            onKeyDown={(event) => {
              if (
                event.key === 'Enter' &&
                (event.ctrlKey || event.metaKey) &&
                !event.nativeEvent.isComposing
              ) {
                event.preventDefault()
                if (canGenerate) props.onGenerate()
              }
            }}
            id='image-prompt'
            value={props.value.prompt}
            aria-invalid={promptInvalid && promptTouched}
            onBlur={() => setPromptTouched(true)}
            disabled={props.isGenerating}
            placeholder={t('Describe the image you want to create')}
            onChange={(event) => props.onChange({ prompt: event.target.value })}
            rows={4}
          />
          {promptInvalid ? (
            <div className='flex flex-wrap gap-2'>
              {IMAGE_PROMPT_EXAMPLE_KEYS.map((example) => (
                <Button
                  className='h-auto max-w-full py-1 text-left whitespace-normal'
                  key={example}
                  type='button'
                  variant='outline'
                  size='xs'
                  disabled={props.isGenerating}
                  onClick={() => props.onChange({ prompt: t(example) })}
                >
                  {t(example)}
                </Button>
              ))}
            </div>
          ) : null}
        </div>
        <div className='flex min-w-0 flex-wrap items-center gap-2 border-t pt-3'>
          <div className='grid w-full min-w-0 shrink-0 grid-cols-[minmax(0,1fr)] gap-1 sm:w-48'>
            <Label className='sr-only' htmlFor='image-api-key'>
              {t('Token')}
            </Label>
            <Select
              value={
                props.value.apiKeyId === null
                  ? ''
                  : String(props.value.apiKeyId)
              }
              onValueChange={(next) => {
                if (next !== null) {
                  props.onChange({
                    apiKeyId: next === '' ? null : Number(next),
                  })
                }
              }}
              disabled={props.isGenerating}
            >
              <SelectTrigger id='image-api-key' className='w-full min-w-0'>
                <SelectValue
                  className='min-w-0'
                  placeholder={t('Select an API key')}
                >
                  <span className='min-w-0 truncate' title={selectedApiKeyName}>
                    {t('Token')}: {selectedApiKeyName}
                  </span>
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {props.keys.map((key) => (
                    <SelectItem key={key.id} value={String(key.id)}>
                      {key.name}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>

          <div className='grid w-full min-w-0 shrink-0 grid-cols-[minmax(0,1fr)] gap-1 sm:w-52'>
            <Label className='sr-only' htmlFor='image-model'>
              {t('Model')}
            </Label>
            <Select
              value={props.value.model}
              onValueChange={(next) => {
                if (next !== null) props.onChange({ model: next })
              }}
              disabled={props.isGenerating || props.isInitializing}
            >
              <SelectTrigger id='image-model' className='w-full min-w-0'>
                <SelectValue
                  className='min-w-0'
                  placeholder={t('Select a model')}
                >
                  <span className='min-w-0 truncate' title={props.value.model}>
                    {t('Model')}: {props.value.model || t('Select a model')}
                  </span>
                </SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {props.models.map((model) => (
                    <SelectItem key={model} value={model}>
                      {model}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>

          <Popover>
            <PopoverTrigger
              render={
                <Button
                  type='button'
                  variant='outline'
                  disabled={
                    props.isGenerating || props.maxReferenceImages === 0
                  }
                />
              }
              aria-label={t('Add reference images')}
            >
              <ImagePlus aria-hidden='true' />
              {t('Add reference images')}
              {props.references.length ? ` · ${props.references.length}` : ''}
            </PopoverTrigger>
            <PopoverContent
              align='start'
              className='max-h-[65vh] w-80 max-w-[calc(100vw-2rem)] overflow-y-auto'
            >
              <PopoverTitle>{t('Add reference images')}</PopoverTitle>
              <ReferenceUploader
                items={props.references}
                maxAllowed={props.maxReferenceImages}
                disabled={props.isGenerating}
                errorKey={props.referenceErrorKey}
                onAddFiles={props.onAddReferenceFiles}
                onRemove={props.onRemoveReference}
              />
            </PopoverContent>
          </Popover>
          <ImageResolutionPresets
            model={props.value.model}
            size={props.value.size}
            sizes={props.sizes}
            disabled={props.isGenerating || props.isInitializing}
            onChange={(size) => props.onChange({ size })}
          />
          {(['size', 'quality'] as const).map((parameter) => {
            const options = parameter === 'size' ? props.sizes : props.qualities
            const label = parameter === 'size' ? t('Size') : t('Quality')
            const value = props.value[parameter]
            return (
              <Popover
                key={parameter}
                open={openParameter === parameter}
                onOpenChange={(open) =>
                  setOpenParameter(open ? parameter : null)
                }
              >
                <PopoverTrigger
                  render={
                    <Button
                      type='button'
                      variant='outline'
                      disabled={props.isGenerating || options.length === 0}
                    />
                  }
                  aria-label={label}
                >
                  {parameter === 'quality' ? (
                    <SlidersHorizontal aria-hidden='true' />
                  ) : null}
                  {IMAGE_PARAMETER_LABEL_KEYS[value]
                    ? t(IMAGE_PARAMETER_LABEL_KEYS[value])
                    : value || t('Auto')}
                </PopoverTrigger>
                <PopoverContent
                  align='start'
                  className='max-h-[65vh] w-80 max-w-[calc(100vw-2rem)] overflow-y-auto'
                >
                  <PopoverTitle>{label}</PopoverTitle>
                  <div
                    className='flex flex-wrap gap-2'
                    role='group'
                    aria-label={label}
                  >
                    {options.map((option) => (
                      <Button
                        key={option}
                        type='button'
                        size='sm'
                        variant={value === option ? 'default' : 'outline'}
                        aria-pressed={value === option}
                        disabled={props.isGenerating}
                        onClick={() => {
                          props.onChange({ [parameter]: option })
                          setOpenParameter(null)
                        }}
                      >
                        {IMAGE_PARAMETER_LABEL_KEYS[option]
                          ? t(IMAGE_PARAMETER_LABEL_KEYS[option])
                          : option}
                      </Button>
                    ))}
                  </div>
                  {parameter === 'size' ? (
                    <div className='grid grid-cols-3 gap-2 border-t pt-3'>
                      {options
                        .filter((option) => /^\d+[x:]\d+$/.test(option))
                        .map((option) => {
                          const [width, height] = option
                            .split(/[x:]/)
                            .map(Number)
                          let orientation = t('Square')
                          let icon = '□'
                          if (width > height) {
                            orientation = t('Landscape')
                            icon = '▭'
                          }
                          if (width < height) {
                            orientation = t('Portrait')
                            icon = '▯'
                          }
                          return (
                            <Button
                              key={option}
                              type='button'
                              variant={value === option ? 'default' : 'outline'}
                              className='h-auto flex-col gap-1 py-3'
                              aria-label={`${orientation} ${option}`}
                              aria-pressed={value === option}
                              disabled={props.isGenerating}
                              onClick={() => {
                                props.onChange({ size: option })
                                setOpenParameter(null)
                              }}
                            >
                              <span aria-hidden='true'>{icon}</span>
                              <span>{orientation}</span>
                              <span className='text-[10px]'>{option}</span>
                            </Button>
                          )
                        })}
                    </div>
                  ) : null}
                </PopoverContent>
              </Popover>
            )
          })}
          <div>
            <Label className='sr-only' htmlFor='image-count'>
              {t('Count')}
            </Label>
            <Select
              value={String(props.value.count)}
              onValueChange={(next) => {
                if (next !== null) props.onChange({ count: Number(next) })
              }}
              disabled={props.isGenerating}
            >
              <SelectTrigger id='image-count'>
                <SelectValue>×{props.value.count}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {Array.from(
                    { length: countLimit },
                    (_, index) => index + 1
                  ).map((count) => (
                    <SelectItem key={count} value={String(count)}>
                      {count}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </div>
          {props.styles.length ? (
            <Popover>
              <PopoverTrigger
                render={
                  <Button
                    type='button'
                    variant='ghost'
                    size='sm'
                    disabled={props.isGenerating}
                  />
                }
              >
                {t('Style')}
              </PopoverTrigger>
              <PopoverContent>
                <PopoverTitle>{t('Style')}</PopoverTitle>
                <div className='flex flex-wrap gap-2'>
                  {props.styles.map((style) => (
                    <Button
                      key={style}
                      type='button'
                      variant={
                        props.value.style === style ? 'default' : 'outline'
                      }
                      aria-pressed={props.value.style === style}
                      onClick={() => props.onChange({ style })}
                    >
                      {t(IMAGE_PARAMETER_LABEL_KEYS[style] || style)}
                    </Button>
                  ))}
                </div>
              </PopoverContent>
            </Popover>
          ) : null}
          <Button
            type='button'
            className='w-full sm:ml-auto sm:w-auto'
            disabled={!props.isGenerating && !canGenerate}
            variant={props.isGenerating ? 'outline' : 'default'}
            onClick={props.isGenerating ? props.onCancel : props.onGenerate}
          >
            <Sparkles aria-hidden='true' />
            {props.isGenerating ? t('Stop waiting') : t('Generate')}
          </Button>
        </div>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <div className='flex items-center gap-2'>
            <Switch
              id='image-async-mode'
              size='sm'
              checked={props.asyncMode ?? false}
              onCheckedChange={(checked) => props.onAsyncModeChange?.(checked)}
              disabled={props.isGenerating}
            />
            <Label htmlFor='image-async-mode' className='text-xs'>
              {t('Async generation (avoid gateway timeouts)')}
            </Label>
          </div>
          <p className='text-muted-foreground text-xs'>Ctrl/⌘ + Enter</p>
        </div>
        {props.asyncMode ? (
          <p className='text-muted-foreground text-xs'>
            {t(
              'Generate in the background and poll for results. Keep this page open; stopping only ends the wait.'
            )}
          </p>
        ) : null}
        {props.formError ? (
          <Alert variant='destructive'>
            <AlertDescription role='alert'>{props.formError}</AlertDescription>
          </Alert>
        ) : null}

        {props.isGenerating ? (
          <p className='text-muted-foreground text-xs'>
            {t(
              'Stopping only ends this wait. The provider may still process and charge for the request.'
            )}
          </p>
        ) : null}
      </div>
    </form>
  )
}
