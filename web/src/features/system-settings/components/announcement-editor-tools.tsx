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
import { useId, useState, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'

import { RichContent } from '@/components/rich-content'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { getAnnouncementImageUrl } from '@/lib/announcement-images'

interface AnnouncementEditorToolsProps {
  value: string
  onChange: (value: string) => void
  maxLength?: number
}

// Compose the existing textarea/RichContent flow; no upload, editor engine or proxy.
export function AnnouncementEditorTools(props: AnnouncementEditorToolsProps) {
  const { t } = useTranslation()
  const id = useId()
  const [url, setUrl] = useState('')
  const [description, setDescription] = useState('')
  const [preview, setPreview] = useState(false)
  const [error, setError] = useState('')

  const insertImage = () => {
    const normalized = getAnnouncementImageUrl(url.trim())
    if (!normalized || !description.trim()) {
      setError(t('Enter a public HTTPS image URL and an image description.'))
      return
    }
    const alt = description
      .trim()
      .replaceAll(/([\\[\]])/g, '\\$1')
      .replaceAll(/[\r\n]/g, ' ')
    const destination = normalized
      .replaceAll('&', '&amp;')
      .replaceAll('(', '%28')
      .replaceAll(')', '%29')
    const next = `${props.value}${props.value ? '\n\n' : ''}![${alt}](${destination})`
    if (props.maxLength && next.length > props.maxLength) {
      setError(
        t('Content cannot exceed {{max}} characters.', { max: props.maxLength })
      )
      return
    }
    props.onChange(next)
    setUrl('')
    setDescription('')
    setError('')
  }

  const handleImageKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') {
      event.preventDefault()
      insertImage()
    }
  }

  return (
    <div className='min-w-0 space-y-3'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Use public HTTPS image links. Images are not uploaded here; viewing them contacts the image host. Do not include secrets in URLs.'
        )}
      </p>
      <div className='grid min-w-0 gap-3 sm:grid-cols-2'>
        <div className='min-w-0 space-y-2'>
          <Label htmlFor={`${id}-url`}>{t('Image URL')}</Label>
          <Input
            id={`${id}-url`}
            type='text'
            inputMode='url'
            onKeyDown={handleImageKeyDown}
            value={url}
            maxLength={4096}
            placeholder='https://example.com/image.png'
            onChange={(event) => {
              setUrl(event.target.value)
              setError('')
            }}
            aria-describedby={error ? `${id}-error` : undefined}
          />
        </div>
        <div className='min-w-0 space-y-2'>
          <Label htmlFor={`${id}-description`}>{t('Image description')}</Label>
          <Input
            id={`${id}-description`}
            onKeyDown={handleImageKeyDown}
            value={description}
            maxLength={200}
            onChange={(event) => {
              setDescription(event.target.value)
              setError('')
            }}
          />
        </div>
      </div>
      {error ? (
        <p id={`${id}-error`} role='alert' className='text-destructive text-sm'>
          {error}
        </p>
      ) : null}
      <div className='flex flex-wrap gap-2'>
        <Button type='button' variant='outline' size='sm' onClick={insertImage}>
          {t('Insert image')}
        </Button>
        <Button
          type='button'
          variant='outline'
          size='sm'
          aria-expanded={preview}
          aria-controls={`${id}-preview`}
          onClick={() => setPreview(!preview)}
        >
          {t('Preview')}
        </Button>
      </div>
      {preview ? (
        <section
          id={`${id}-preview`}
          aria-label={t('Preview')}
          className='max-h-96 min-w-0 overflow-auto rounded-md border p-3'
        >
          <RichContent
            imagePolicy='announcement'
            breaks
            content={props.value}
          />
        </section>
      ) : null}
    </div>
  )
}
