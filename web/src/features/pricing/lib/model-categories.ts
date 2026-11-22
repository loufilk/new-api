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
  MODEL_DISPLAY_CATEGORIES,
  modelDisplayCategoriesSchema,
} from '@/lib/model-display-categories'

import type { PricingModel } from '../types'

export const MODEL_CATEGORIES = [...MODEL_DISPLAY_CATEGORIES, 'all'] as const
export type ModelCategory = (typeof MODEL_CATEGORIES)[number]

/** Catalog evidence only: names, tags, prices and default wire protocols are not capabilities. */
export function getAutomaticModelCategories(
  model: PricingModel
): Exclude<ModelCategory, 'all'>[] {
  const outputs = new Set(model.output_modalities ?? [])
  const inputs = new Set(model.input_modalities ?? [])
  const capabilities = new Set(model.capabilities ?? [])
  const endpoints = new Set(model.supported_endpoint_types ?? [])
  const hasCapability = (...values: string[]) =>
    values.some((value) => capabilities.has(value))
  const hasEndpoint = (...values: string[]) =>
    values.some((value) => endpoints.has(value))
  const speechRecognition =
    hasCapability(
      'speech_recognition',
      'transcription',
      'audio_transcription',
      'asr'
    ) ||
    hasEndpoint('audio-transcription', 'audio-translation') ||
    (inputs.size === 1 && inputs.has('audio') && outputs.has('text'))
  const explicitText = hasCapability(
    'text_generation',
    'chat',
    'reasoning',
    'function_calling',
    'tools'
  )
  const categories: Exclude<ModelCategory, 'all'>[] = []
  if (explicitText || (outputs.has('text') && !speechRecognition)) {
    categories.push('text')
  }
  if (
    outputs.has('image') ||
    hasCapability('image_generation', 'image_editing') ||
    hasEndpoint('image-generation')
  ) {
    categories.push('images')
  }
  if (
    outputs.has('video') ||
    hasCapability('video_generation') ||
    hasEndpoint('openai-video')
  ) {
    categories.push('video')
  }
  if (
    outputs.has('audio') ||
    speechRecognition ||
    hasCapability(
      'audio_generation',
      'speech_synthesis',
      'text_to_speech',
      'realtime'
    ) ||
    hasEndpoint('audio-speech', 'realtime')
  ) {
    categories.push('audio')
  }
  if (
    hasCapability('embeddings', 'rerank', 'reranking') ||
    hasEndpoint('embeddings', 'jina-rerank')
  ) {
    categories.push('vectors')
  }
  return categories
}

/** Display-only override. Never use it as evidence for protocols or playgrounds. */
export function getModelCategories(
  model: PricingModel
): Exclude<ModelCategory, 'all'>[] {
  const override = modelDisplayCategoriesSchema.safeParse(
    model.display_categories
  )
  if (override.success && override.data.length > 0) {
    return MODEL_DISPLAY_CATEGORIES.filter((category) =>
      override.data.includes(category)
    )
  }
  return getAutomaticModelCategories(model)
}

export function filterByCategory(
  models: PricingModel[],
  category: ModelCategory
): PricingModel[] {
  if (category === 'all') return models
  return models.filter((model) => getModelCategories(model).includes(category))
}
