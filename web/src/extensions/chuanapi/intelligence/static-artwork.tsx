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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import {
  WebPreview,
  WebPreviewBody,
} from '@/components/ai-elements/web-preview'

import { buildStaticArtworkDocument } from './static-artwork-document'

export function StaticArtwork(props: { html: string; thumbnail?: boolean }) {
  const { t } = useTranslation()
  const source = useMemo(
    () => buildStaticArtworkDocument(props.html),
    [props.html]
  )
  return (
    <WebPreview
      className={
        props.thumbnail
          ? 'h-auto overflow-hidden'
          : 'h-[60vh] min-h-64 overflow-hidden'
      }
    >
      <WebPreviewBody
        title={t('intelligence.artwork')}
        srcDoc={source}
        sandbox=''
        referrerPolicy='no-referrer'
        tabIndex={props.thumbnail ? -1 : 0}
        className={
          props.thumbnail
            ? 'pointer-events-none aspect-[4/3] h-auto w-full'
            : undefined
        }
      />
    </WebPreview>
  )
}
