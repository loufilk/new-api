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
import { ImageIcon } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'

import { downloadImage } from '../api'
import type { ImageHistoryEntry } from '../types'
import { ImageResolutionPreview } from './ImageResolutionPreview'

type HistoryPanelProps = {
  onReuse?: (entry: ImageHistoryEntry) => void
  isGenerating?: boolean
  pendingCount?: number
  entries: ImageHistoryEntry[]
  totalCount: number
  query: string
  onQueryChange: (value: string) => void
  orientation: 'all' | 'square' | 'landscape' | 'portrait'
  onOrientationChange: (
    value: 'all' | 'square' | 'landscape' | 'portrait'
  ) => void
  onDimensions?: (
    entryId: string,
    resultId: string,
    source: string,
    width: number,
    height: number
  ) => void
  onRemove: (id: string) => void
  onClear: () => void
  onKeepLocally: (entryId: string, resultId: string) => Promise<void>
  resolveUrl: (resultId: string, fallback: string) => string
}

export function HistoryPanel(props: HistoryPanelProps) {
  const { t } = useTranslation()
  const [confirmation, setConfirmation] = useState<string | null>(null)
  const [preview, setPreview] = useState<{
    entryId: string
    resultId: string
  } | null>(null)
  const previewEntry = props.entries.find(
    (entry) => entry.id === preview?.entryId
  )
  const previewResult = previewEntry?.results.find(
    (result) => result.id === preview?.resultId
  )
  const orientationLabels = {
    all: t('All orientations'),
    square: t('Square'),
    landscape: t('Landscape'),
    portrait: t('Portrait'),
  }

  const handleDownload = async (url: string, id: string) => {
    try {
      await downloadImage(url, `history-${id}.png`)
    } catch (error) {
      if (error instanceof Error && error.message === 'Image not available') {
        toast.error(t('Image not available'))
        return
      }
      toast.error(t('Download failed'))
    }
  }

  const handleKeep = async (entryId: string, resultId: string) => {
    try {
      await props.onKeepLocally(entryId, resultId)
      toast.success(t('Kept locally'))
    } catch {
      toast.error(t('Failed to keep image locally'))
    }
  }

  return (
    <section className='grid min-w-0 gap-4' aria-label={t('Artwork')}>
      <div className='flex min-w-0 flex-wrap items-center gap-2'>
        <h2 className='mr-auto text-sm font-semibold'>
          {t('Generation results')}
        </h2>
        <div className='grid w-full min-w-0 gap-1.5 sm:w-64'>
          <Label className='sr-only' htmlFor='image-history-search'>
            {t('Search history')}
          </Label>
          <Input
            id='image-history-search'
            value={props.query}
            placeholder={t('Search by prompt or model')}
            onChange={(event) => props.onQueryChange(event.target.value)}
          />
        </div>
        <div className='grid w-full min-w-0 gap-1.5 sm:w-auto'>
          <Label className='sr-only' htmlFor='image-history-orientation'>
            {t('Orientation')}
          </Label>
          <Select
            value={props.orientation}
            onValueChange={(next) => {
              if (next !== null) props.onOrientationChange(next)
            }}
          >
            <SelectTrigger id='image-history-orientation' className='w-full'>
              <SelectValue>{orientationLabels[props.orientation]}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectItem value='all'>{t('All orientations')}</SelectItem>
                <SelectItem value='square'>{t('Square')}</SelectItem>
                <SelectItem value='landscape'>{t('Landscape')}</SelectItem>
                <SelectItem value='portrait'>{t('Portrait')}</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Unkept images expire after one hour. Keep locally saves in this browser, not as a downloaded file. Up to 120 batches.'
        )}
      </p>
      <div className='grid min-w-0 gap-3'>
        {props.totalCount === 0 && !props.isGenerating ? (
          <EmptyState
            icon={ImageIcon}
            bordered
            className='min-h-[300px] border-dashed'
            title={t('No history yet.')}
            description={t('Generated images will appear here.')}
          />
        ) : null}
        {props.totalCount > 0 ? (
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => setConfirmation('all')}
            className='justify-self-end'
          >
            {t('Clear history')}
          </Button>
        ) : null}

        {props.totalCount > 0 && props.entries.length === 0 ? (
          <EmptyState title={t('No matching history.')} />
        ) : null}

        <ul className='grid min-w-0 grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4'>
          {props.isGenerating
            ? Array.from({ length: props.pendingCount ?? 1 }, (_, index) => (
                <li
                  key={`pending-${index}`}
                  aria-label={t('Generating images...')}
                  className='rounded-xl border p-3'
                >
                  <Skeleton className='aspect-square w-full' />
                  <p
                    role='status'
                    className='text-muted-foreground mt-3 text-sm'
                  >
                    {t('Generating images...')}
                  </p>
                </li>
              ))
            : null}
          {props.entries.flatMap((entry) =>
            entry.results.map((result) => {
              const url = props.resolveUrl(result.id, result.source)
              return (
                <li
                  key={result.id}
                  className='bg-card min-w-0 overflow-hidden rounded-xl border'
                >
                  <Button
                    type='button'
                    variant='ghost'
                    className='h-auto w-full rounded-none p-0'
                    onClick={() =>
                      setPreview({ entryId: entry.id, resultId: result.id })
                    }
                    aria-label={t('Preview image for {{prompt}}', {
                      prompt: entry.prompt,
                    })}
                  >
                    <img
                      src={url}
                      alt={entry.prompt}
                      loading='lazy'
                      onLoad={(event) => {
                        const image = event.currentTarget
                        if (image.naturalWidth > 0 && image.naturalHeight > 0) {
                          props.onDimensions?.(
                            entry.id,
                            result.id,
                            url,
                            image.naturalWidth,
                            image.naturalHeight
                          )
                        }
                      }}
                      className='bg-muted/30 aspect-square w-full object-contain'
                    />
                  </Button>
                  <div className='grid gap-2 p-3'>
                    <p
                      className='truncate text-sm font-medium'
                      title={entry.prompt}
                    >
                      {entry.prompt}
                    </p>
                    <p
                      className='text-muted-foreground truncate text-xs'
                      title={entry.model}
                    >
                      {entry.model} · {t('Requested image size')}:{' '}
                      {entry.settings.size || t('Default size')}
                    </p>
                    <p className='text-muted-foreground text-xs'>
                      {t('Actual image size')}:{' '}
                      {result.dimensionsVerified
                        ? `${result.width}×${result.height}`
                        : t('Not yet verified')}
                    </p>
                    <div className='flex flex-wrap gap-1'>
                      <Button
                        type='button'
                        variant='outline'
                        size='xs'
                        onClick={() => void handleDownload(url, result.id)}
                      >
                        {t('Download')}
                      </Button>
                      {props.onReuse ? (
                        <Button
                          type='button'
                          variant='outline'
                          size='xs'
                          disabled={props.isGenerating}
                          onClick={() => props.onReuse?.(entry)}
                        >
                          {t('Reuse')}
                        </Button>
                      ) : null}
                      {!result.keptLocally ? (
                        <Button
                          type='button'
                          variant='ghost'
                          size='xs'
                          onClick={() => void handleKeep(entry.id, result.id)}
                        >
                          {t('Keep locally')}
                        </Button>
                      ) : (
                        <span className='text-muted-foreground text-xs'>
                          {t('Kept locally')}
                        </span>
                      )}
                      <Button
                        type='button'
                        variant='ghost'
                        size='xs'
                        onClick={() => setConfirmation(entry.id)}
                        aria-label={t('Delete history {{prompt}}', {
                          prompt: entry.prompt,
                        })}
                      >
                        {t('Delete batch')}
                      </Button>
                    </div>
                  </div>
                </li>
              )
            })
          )}
        </ul>
      </div>
      <ConfirmDialog
        open={confirmation !== null}
        onOpenChange={(open) => {
          if (!open) setConfirmation(null)
        }}
        title={confirmation === 'all' ? t('Clear history') : t('Delete batch')}
        desc={t(
          'This removes the selected batches and their locally kept images from this browser.'
        )}
        destructive
        confirmText={t('Delete')}
        handleConfirm={() => {
          if (confirmation === 'all') props.onClear()
          else if (confirmation) props.onRemove(confirmation)
          setConfirmation(null)
        }}
      />
      <Dialog
        open={!!previewResult}
        onOpenChange={(open) => {
          if (!open) setPreview(null)
        }}
        title={t('Image preview')}
        contentClassName='sm:max-w-4xl'
      >
        {previewResult && previewEntry ? (
          <div className='grid gap-4'>
            <ImageResolutionPreview
              key={previewResult.id}
              url={props.resolveUrl(previewResult.id, previewResult.source)}
              prompt={previewEntry.prompt}
              requestedSize={previewEntry.settings.size}
              onDimensions={(width, height) =>
                props.onDimensions?.(
                  previewEntry.id,
                  previewResult.id,
                  props.resolveUrl(previewResult.id, previewResult.source),
                  width,
                  height
                )
              }
            />
            <p className='text-sm break-words whitespace-pre-wrap'>
              {previewEntry.prompt}
            </p>
            <dl className='grid grid-cols-2 gap-2 text-sm break-all'>
              <dt>{t('Model')}</dt>
              <dd>{previewEntry.model}</dd>
              <dt>{t('Requested image size')}</dt>
              <dd>{previewEntry.settings.size || t('Default size')}</dd>
              <dt>{t('Quality')}</dt>
              <dd>{previewEntry.settings.quality || '—'}</dd>
              <dt>{t('Style')}</dt>
              <dd>{previewEntry.settings.style || '—'}</dd>
              <dt>{t('Count')}</dt>
              <dd>{previewEntry.settings.count}</dd>
            </dl>
            <div className='flex flex-wrap gap-2'>
              <CopyButton value={previewEntry.prompt} size='sm'>
                {t('Copy')}
              </CopyButton>
              <Button
                variant='outline'
                onClick={() =>
                  void handleDownload(
                    props.resolveUrl(previewResult.id, previewResult.source),
                    previewResult.id
                  )
                }
              >
                {t('Download')}
              </Button>
              {props.onReuse ? (
                <Button
                  disabled={props.isGenerating}
                  onClick={() => {
                    props.onReuse?.(previewEntry)
                    setPreview(null)
                  }}
                >
                  {t('Reuse')}
                </Button>
              ) : null}
            </div>
          </div>
        ) : null}
      </Dialog>
    </section>
  )
}
