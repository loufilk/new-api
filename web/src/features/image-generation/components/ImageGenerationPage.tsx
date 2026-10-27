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
import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { getAuthSessionEpoch } from '@/lib/auth-session'
import { useAuthStore } from '@/stores/auth-store'

import { useImageData } from '../hooks/use-image-data'
import { useImageGeneration } from '../hooks/use-image-generation'
import { useImageHistory } from '../hooks/use-image-history'
import { useReferenceImages } from '../hooks/use-reference-images'
import {
  getModelCapability,
  resolveSettingsForModel,
} from '../lib/model-capabilities'
import type { ImageHistoryEntry } from '../types'
import { GenerationForm } from './GenerationForm'
import { HistoryPanel } from './HistoryPanel'

export function ImageGenerationPage() {
  const epoch = useSyncExternalStore(
    useAuthStore.subscribe,
    getAuthSessionEpoch
  )
  return <ImageGenerationWorkspace key={epoch} />
}

function ImageGenerationWorkspace() {
  const { t } = useTranslation()
  const data = useImageData()
  const history = useImageHistory()
  const generation = useImageGeneration()

  const [prompt, setPrompt] = useState('')
  const [size, setSize] = useState('')
  const [quality, setQuality] = useState('')
  const [style, setStyle] = useState('')
  const [count, setCount] = useState(1)
  const [asyncMode, setAsyncMode] = useState(false)
  const [reuseEntry, setReuseEntry] = useState<ImageHistoryEntry | null>(null)
  const artworkRef = useRef<HTMLDivElement>(null)
  const [completed, setCompleted] = useState(0)
  useEffect(() => {
    if (completed) {
      artworkRef.current?.scrollIntoView?.({
        block: 'start',
        behavior: 'smooth',
      })
    }
  }, [completed])

  const capability = getModelCapability(data.model)

  useEffect(() => {
    if (!data.model) return
    const resolved = resolveSettingsForModel(data.model, {
      size,
      quality,
      style,
      count,
    })
    if (resolved.size !== size) setSize(resolved.size)
    if (resolved.quality !== quality) setQuality(resolved.quality)
    if (resolved.style !== style) setStyle(resolved.style)
    if (resolved.count !== count) setCount(resolved.count)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.model])

  const references = useReferenceImages(
    data.model,
    capability.maxReferenceImages
  )

  const handleModelChange = (nextModel: string) => {
    data.setModel(nextModel)
    const resolved = resolveSettingsForModel(nextModel, {
      size,
      quality,
      style,
      count,
    })
    setSize(resolved.size)
    setQuality(resolved.quality)
    setStyle(resolved.style)
    setCount(resolved.count)
  }

  const handleGenerate = async () => {
    if (generation.isGenerating) return
    const images = await generation.generate({
      apiKeyId: data.apiKeyId,
      model: data.model,
      prompt,
      count,
      size,
      quality,
      style,
      images: references.items.map((item) => item.file),
      asyncMode,
    })
    if (images?.length === 0) {
      generation.setError(
        t('No images were returned. Try adjusting your prompt.')
      )
      return
    }
    if (images && images.length > 0) {
      history.setQuery('')
      history.setOrientation('all')
      setCompleted((value) => value + 1)
      history.pushEntry({
        prompt: prompt.trim(),
        model: data.model,
        size,
        quality,
        style,
        count,
        images,
      })
    }
  }

  const applyReuse = (entry: ImageHistoryEntry) => {
    if (generation.isGenerating) return
    const nextModel = data.availableModels.includes(entry.model)
      ? entry.model
      : data.model
    const resolved = resolveSettingsForModel(nextModel, entry.settings)
    setPrompt(entry.prompt)
    data.setModel(nextModel)
    setSize(resolved.size)
    setQuality(resolved.quality)
    setStyle(resolved.style)
    setCount(resolved.count)
    references.clear()
    generation.setError(null)
    setReuseEntry(null)
    if (
      nextModel !== entry.model ||
      Object.entries(resolved).some(
        ([key, value]) =>
          value !== entry.settings[key as keyof typeof entry.settings]
      )
    ) {
      toast.info(
        t(
          'Some historical settings are unavailable and were adjusted for the current model.'
        )
      )
    }
    toast.info(
      t(
        'Prompt and settings restored. Add reference images again if needed, then generate.'
      )
    )
    document.querySelector<HTMLTextAreaElement>('#image-prompt')?.focus()
  }

  if (data.isLoadingBootstrap) {
    return (
      <div className='grid gap-4' aria-live='polite'>
        <Skeleton className='h-8 w-48' />
        <Skeleton className='h-64 w-full' />
        <span className='sr-only'>{t('Loading image generation...')}</span>
      </div>
    )
  }

  if (data.isBootstrapError) {
    return (
      <Alert variant='destructive'>
        <AlertTitle>{t('Failed to load image generation')}</AlertTitle>
        <AlertDescription className='mt-2 flex flex-wrap items-center gap-2'>
          <span>{t('Please check your connection and try again.')}</span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void data.refetchBootstrap()}
          >
            {t('Retry')}
          </Button>
        </AlertDescription>
      </Alert>
    )
  }

  if (data.keys.length === 0) {
    return (
      <Alert>
        <AlertTitle>{t('No API keys available')}</AlertTitle>
        <AlertDescription className='mt-2 flex flex-wrap items-center gap-2'>
          <span>
            {t('Create an API key first, then return to generate images.')}
          </span>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => void data.refetchBootstrap()}
          >
            {t('Refresh')}
          </Button>
        </AlertDescription>
      </Alert>
    )
  }

  let modelNotice: {
    title: string
    description: string
    destructive: boolean
    canRetry: boolean
  } | null = null
  if (data.selectedKey && data.availableModels.length === 0) {
    const tokenLoading = data.isLoadingTokenModels
    let modelsTitle = t('No image models available')
    let modelsDescription = t(
      'No image models match this API key. Check the model image-generation endpoint configuration, the key group, and its model restrictions, then refresh.'
    )
    if (tokenLoading) {
      modelsTitle = t('Loading models...')
      modelsDescription = t('Loading available models for this API key.')
    } else if (data.isTokenModelsError) {
      modelsTitle = t('Loading failed')
      modelsDescription = t('Please check your connection and try again.')
    }
    modelNotice = {
      title: modelsTitle,
      description: modelsDescription,
      destructive: !tokenLoading && data.isTokenModelsError,
      canRetry: !tokenLoading,
    }
  }

  return (
    <div className='mx-auto grid w-full max-w-7xl min-w-0 gap-5'>
      <div>
        <h1 className='text-xl font-semibold tracking-tight'>
          {t('Image Generation')}
        </h1>
        <p className='text-muted-foreground text-sm'>
          {t('Turn text prompts into images. Results stay in this browser.')}
        </p>
      </div>
      <div aria-live='polite' className='sr-only'>
        {generation.isGenerating ? t('Generating images...') : null}
      </div>
      {modelNotice ? (
        <Alert variant={modelNotice.destructive ? 'destructive' : undefined}>
          <AlertTitle>{modelNotice.title}</AlertTitle>
          <AlertDescription className='mt-2 flex flex-wrap items-center gap-2'>
            <span>{modelNotice.description}</span>
            {modelNotice.canRetry ? (
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={() =>
                  void Promise.all([
                    data.refetchBootstrap(),
                    data.refetchTokenModels(),
                  ])
                }
              >
                {t('Refresh')}
              </Button>
            ) : null}
          </AlertDescription>
        </Alert>
      ) : null}
      <div className='grid min-w-0 gap-4'>
        <h2 className='text-center text-xl font-semibold'>
          {t('Start generating images')}
        </h2>
        <GenerationForm
          asyncMode={asyncMode}
          onAsyncModeChange={setAsyncMode}
          value={{
            prompt,
            apiKeyId: data.apiKeyId,
            model: data.model,
            size,
            quality,
            style,
            count,
          }}
          onChange={(patch) => {
            if (patch.prompt !== undefined) setPrompt(patch.prompt)
            if (patch.apiKeyId !== undefined) data.setApiKeyId(patch.apiKeyId)
            if (patch.model !== undefined) handleModelChange(patch.model)
            if (patch.size !== undefined) setSize(patch.size)
            if (patch.quality !== undefined) setQuality(patch.quality)
            if (patch.style !== undefined) setStyle(patch.style)
            if (patch.count !== undefined) setCount(patch.count)
          }}
          keys={data.keys}
          models={data.availableModels}
          sizes={capability.sizes}
          qualities={capability.qualities}
          styles={capability.styles}
          maxCount={capability.maxN}
          maxReferenceImages={capability.maxReferenceImages}
          references={references.items}
          referenceErrorKey={references.errorKey}
          onAddReferenceFiles={references.addFiles}
          onRemoveReference={references.removeItem}
          isGenerating={generation.isGenerating}
          formError={generation.error}
          isInitializing={data.isLoadingTokenModels}
          onGenerate={() => void handleGenerate()}
          onCancel={generation.cancel}
        />
        {capability.requestFormat === 'grok-console' ? (
          <p className='text-muted-foreground text-sm'>
            {t(
              'Console supports up to 3 reference images and low/medium quality. Size sets the aspect ratio; resolution is 1K.'
            )}
          </p>
        ) : null}
        <div ref={artworkRef} className='min-w-0 scroll-mt-4'>
          <HistoryPanel
            isGenerating={generation.isGenerating}
            pendingCount={count}
            onReuse={(entry) => {
              if (references.items.length) setReuseEntry(entry)
              else applyReuse(entry)
            }}
            entries={history.visibleEntries}
            totalCount={history.entries.length}
            query={history.query}
            onQueryChange={history.setQuery}
            orientation={history.orientation}
            onOrientationChange={history.setOrientation}
            onDimensions={history.recordDimensions}
            onRemove={history.removeEntry}
            onClear={history.clearAll}
            onKeepLocally={history.keepLocally}
            resolveUrl={history.resolveResultUrl}
          />
        </div>
      </div>
      <ConfirmDialog
        open={reuseEntry !== null}
        onOpenChange={(open) => {
          if (!open) setReuseEntry(null)
        }}
        title={t('Reuse')}
        desc={t(
          'Reusing this artwork clears the current reference images. Continue?'
        )}
        disabled={generation.isGenerating}
        handleConfirm={() => {
          if (reuseEntry) applyReuse(reuseEntry)
        }}
      />
    </div>
  )
}
