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
import {
  IMAGE_REFERENCE_ALLOWED_MIME,
  IMAGE_REFERENCE_MAX_TOTAL_BYTES,
} from '../constants'
import { getModelCapability } from './model-capabilities'

export type ReferenceValidationError = {
  code:
    | 'unsupported_type'
    | 'total_size_exceeded'
    | 'too_many'
    | 'model_not_supported'
  messageKey: string
}

export function validateReferenceFiles(params: {
  files: { name: string; size: number; type: string }[]
  existingTotalBytes: number
  existingCount: number
  model: string
}): ReferenceValidationError | null {
  const capability = getModelCapability(params.model)
  if (capability.maxReferenceImages === 0) {
    return {
      code: 'model_not_supported',
      messageKey: 'This model does not support reference images',
    }
  }

  for (const file of params.files) {
    const mime = file.type.toLowerCase()
    if ((IMAGE_REFERENCE_ALLOWED_MIME as readonly string[]).includes(mime)) {
      continue
    }
    if (mime !== '') {
      return {
        code: 'unsupported_type',
        messageKey: 'Only PNG, JPEG, or WebP images are supported',
      }
    }
    if (!/\.(png|jpe?g|webp)$/i.test(file.name)) {
      return {
        code: 'unsupported_type',
        messageKey: 'Only PNG, JPEG, or WebP images are supported',
      }
    }
  }

  const incomingBytes = params.files.reduce((sum, file) => sum + file.size, 0)
  if (
    params.existingTotalBytes + incomingBytes >
    IMAGE_REFERENCE_MAX_TOTAL_BYTES
  ) {
    return {
      code: 'total_size_exceeded',
      messageKey: 'Total reference image size must not exceed 15MB',
    }
  }

  if (
    params.existingCount + params.files.length >
    capability.maxReferenceImages
  ) {
    return {
      code: 'too_many',
      messageKey: 'Too many reference images for this model',
    }
  }

  return null
}

export function getReferenceTotalBytes(files: { size: number }[]): number {
  return files.reduce((sum, file) => sum + file.size, 0)
}
