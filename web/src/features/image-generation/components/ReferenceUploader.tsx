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
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import type { ReferenceImageItem } from '../types'

type ReferenceUploaderProps = {
  items: ReferenceImageItem[]
  maxAllowed: number
  disabled: boolean
  errorKey: string | null
  onAddFiles: (files: File[]) => void
  onRemove: (id: string) => void
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`
}

function collectFiles(
  dataTransfer: DataTransfer | null,
  input: HTMLInputElement | null
): File[] {
  if (dataTransfer && dataTransfer.files.length > 0) {
    return [...dataTransfer.files]
  }
  if (input?.files) return [...input.files]
  return []
}

export function ReferenceUploader(props: ReferenceUploaderProps) {
  const { t } = useTranslation()
  const inputRef = useRef<HTMLInputElement | null>(null)

  const openPicker = () => {
    if (props.disabled) return
    inputRef.current?.click()
  }

  const handleInputChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    const files = collectFiles(null, event.target)
    if (files.length > 0) props.onAddFiles(files)
    event.target.value = ''
  }

  const handleDrop = (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault()
    if (props.disabled) return
    const files = collectFiles(event.dataTransfer, null)
    if (files.length > 0) props.onAddFiles(files)
  }

  const handlePaste = (event: React.ClipboardEvent<HTMLDivElement>) => {
    if (props.disabled) return
    const files = [...event.clipboardData.files]
    if (files.length > 0) props.onAddFiles(files)
  }

  if (props.maxAllowed === 0) return null

  return (
    <div className='grid gap-2'>
      <div
        role='button'
        tabIndex={props.disabled ? -1 : 0}
        aria-label={t('Add reference images')}
        aria-disabled={props.disabled}
        onClick={openPicker}
        onDrop={handleDrop}
        onDragOver={(event) => event.preventDefault()}
        onPaste={handlePaste}
        onKeyDown={(event) => {
          if (event.key !== 'Enter' && event.key !== ' ') return
          event.preventDefault()
          openPicker()
        }}
        className='border-border rounded-lg border border-dashed p-3 text-sm'
      >
        <p className='text-muted-foreground text-xs leading-5'>
          {t(
            'Drag images here, paste from clipboard, or choose files. Only PNG, JPEG, or WebP. Total size up to 15MB.'
          )}
        </p>
        <div className='mt-2 flex items-center gap-2'>
          <span
            aria-hidden='true'
            className='border-input bg-background inline-flex h-8 items-center rounded-md border px-3 text-xs font-medium'
          >
            {t('Choose images')}
          </span>
          <span className='text-muted-foreground text-xs'>
            {t('{{count}} of {{max}} images', {
              count: props.items.length,
              max: props.maxAllowed,
            })}
          </span>
        </div>
        <input
          ref={inputRef}
          type='file'
          accept='image/png,image/jpeg,image/webp'
          multiple
          className='sr-only'
          disabled={props.disabled}
          aria-hidden='true'
          tabIndex={-1}
          onClick={(event) => event.stopPropagation()}
          onChange={handleInputChange}
        />
      </div>
      {props.errorKey ? (
        <p role='alert' className='text-destructive text-xs'>
          {t(props.errorKey)}
        </p>
      ) : null}
      {props.items.length > 0 ? (
        <ul className='flex gap-2 overflow-x-auto pb-1'>
          {props.items.map((item) => (
            <li
              key={item.id}
              className='w-32 shrink-0 overflow-hidden rounded-lg border'
            >
              <img
                src={item.previewUrl}
                alt={t('Reference image {{name}}', { name: item.name })}
                className='h-16 w-full object-contain'
              />
              <div className='flex items-center justify-between gap-1 p-1.5'>
                <span
                  className='min-w-0 flex-1 truncate text-xs'
                  title={item.name}
                >
                  {item.name} · {formatBytes(item.size)}
                </span>
                <Button
                  type='button'
                  variant='ghost'
                  size='xs'
                  disabled={props.disabled}
                  onClick={() => props.onRemove(item.id)}
                  aria-label={t('Remove {{name}}', { name: item.name })}
                >
                  {t('Remove')}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}
