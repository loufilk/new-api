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
import type { PricingModel } from '../types'
import { getAutomaticModelCategories } from './model-categories'
import { replaceModelInPath } from './model-helpers'

export type ModelEndpointMap = Record<
  string,
  { path?: string; method?: string }
>

type ModelEndpoint = {
  type: string
  path: string
  method: string
}

/** Resolve the same verified endpoints for card links, details and samples. */
export function getModelEndpoints(
  model: PricingModel,
  endpointMap: ModelEndpointMap
): ModelEndpoint[] {
  const supportsText = getAutomaticModelCategories(model).includes('text')
  const endpoints: ModelEndpoint[] = []
  for (const type of model.supported_endpoint_types ?? []) {
    // Channel defaults can add OpenAI to media and unknown models. The wire
    // protocol alone does not establish that chat completions are supported.
    if (type === 'openai' && !supportsText) continue
    const info = endpointMap[type]
    const path = info?.path
    if (!path?.startsWith('/') || path.startsWith('//')) continue
    endpoints.push({
      type,
      path: replaceModelInPath(path, model.model_name),
      method: info.method || 'POST',
    })
  }
  // Keep declared protocols in catalog order, ahead of the generic default.
  // A multi-capability model still retains its independently verified chat tab.
  return endpoints.sort(
    (a, b) => Number(a.type === 'openai') - Number(b.type === 'openai')
  )
}

/** Explicit protocol links, never a guessed chat fallback for an unknown protocol. */
export function getEndpointDocId(type: string): string | undefined {
  const docs: Record<string, string> = {
    openai: 'chat-completions',
    'openai-response': 'responses',
    'openai-response-compact': 'responses-compact',
    anthropic: 'claude-messages',
    gemini: 'gemini-generate-content',
    embeddings: 'embeddings',
    'jina-rerank': 'rerank',
    'image-generation': 'image-generations',
    'openai-video': 'videos-create',
    'audio-speech': 'audio-speech',
    'audio-transcription': 'audio-transcriptions',
    'audio-translation': 'audio-translations',
    realtime: 'realtime',
  }
  return docs[type]
}

export function getModelPlayground(
  model: PricingModel
): '/image' | '/playground' | undefined {
  const endpoints = model.supported_endpoint_types ?? []
  const categories = getAutomaticModelCategories(model)
  if (endpoints.includes('image-generation') && categories.includes('images')) {
    return '/image'
  }
  if (endpoints.includes('openai') && categories.includes('text')) {
    return '/playground'
  }
  return undefined
}
