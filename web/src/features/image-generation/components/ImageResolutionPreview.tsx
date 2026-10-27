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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { parseSizeDimensions } from '../lib/model-capabilities'

type Props = {
  url: string
  prompt: string
  requestedSize: string
  onDimensions?: (width: number, height: number) => void
}

export function ImageResolutionPreview(props: Props) {
  const { t } = useTranslation()
  const [decoded, setDecoded] = useState<{
    url: string
    width: number
    height: number
  } | null>(null)
  const actual = decoded?.url === props.url ? decoded : null
  const requested = parseSizeDimensions(props.requestedSize)
  const mismatch =
    actual &&
    requested.width &&
    requested.height &&
    (actual.width !== requested.width || actual.height !== requested.height)
  return (
    <div className='grid min-w-0 gap-2'>
      <img
        key={props.url}
        src={props.url}
        alt={props.prompt}
        className='max-h-[60vh] w-full object-contain'
        onLoad={(event) => {
          const image = event.currentTarget
          if (image.naturalWidth > 0 && image.naturalHeight > 0) {
            props.onDimensions?.(image.naturalWidth, image.naturalHeight)
            setDecoded({
              url: props.url,
              width: image.naturalWidth,
              height: image.naturalHeight,
            })
          }
        }}
        onError={() => setDecoded(null)}
      />
      <p className='text-muted-foreground text-sm'>
        {t('Actual image size')}:{' '}
        {actual ? `${actual.width}×${actual.height}` : t('Not yet verified')}
      </p>
      {mismatch ? (
        <p className='text-sm' role='status'>
          {t('The returned image size differs from the requested size.')}
        </p>
      ) : null}
    </div>
  )
}
