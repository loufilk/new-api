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
import { useTranslation } from 'react-i18next'

import { ReferenceUploader } from '@/features/image-generation/components/ReferenceUploader'
import type { ReferenceImageItem } from '@/features/image-generation/types'

import { SUPPORT_IMAGE_LIMIT } from '../lib/attachments'

export function SupportImagePicker(props: {
  items: ReferenceImageItem[]
  error: string | null
  disabled: boolean
  onAdd: (files: File[]) => void
  onRemove: (id: string) => void
}) {
  const { t } = useTranslation()
  return (
    <ReferenceUploader
      items={props.items}
      maxAllowed={SUPPORT_IMAGE_LIMIT}
      disabled={props.disabled}
      errorKey={props.error}
      onAddFiles={props.onAdd}
      onRemove={props.onRemove}
      pickerLabel={t('Add support images')}
      description={t(
        'Up to 4 images per message. PNG, JPEG or WebP, at most 5 MB each. Remove secrets before uploading.'
      )}
      previewLabel={(name) => t('Support image {{name}}', { name })}
    />
  )
}
