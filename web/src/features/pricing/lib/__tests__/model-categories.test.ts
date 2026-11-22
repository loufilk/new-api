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
import { describe, expect, it } from 'vitest'

import type { PricingModel } from '../../types'
import { getModelCategories, filterByCategory } from '../model-categories'
import { getModelEndpoints, getModelPlayground } from '../model-endpoints'

const model = (values: Partial<PricingModel> = {}): PricingModel => ({
  id: 1,
  model_name: 'opaque-model-id',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 1,
  enable_groups: ['default'],
  ...values,
})

describe('model capability categories', () => {
  it('keeps missing metadata and default OpenAI protocol in all only without guessing names', () => {
    const unknown = model({
      model_name: 'gpt-image-video-embedding',
      supported_endpoint_types: ['openai'],
    })
    expect(getModelCategories(unknown)).toEqual([])
    expect(filterByCategory([unknown], 'all')).toEqual([unknown])
    expect(filterByCategory([unknown], 'text')).toEqual([])
  })
  it('does not confuse vision input with generated images', () => {
    expect(
      getModelCategories(
        model({
          input_modalities: ['text', 'image'],
          output_modalities: ['text'],
          capabilities: ['vision'],
        })
      )
    ).toEqual(['text'])
  })
  it('keeps pure audio transcription out of text despite text output and a default chat protocol', () => {
    expect(
      getModelCategories(
        model({
          input_modalities: ['audio'],
          output_modalities: ['text'],
          supported_endpoint_types: ['openai'],
        })
      )
    ).toEqual(['audio'])
  })
  it('includes multimodal output in every matching category without duplicates', () => {
    expect(
      getModelCategories(
        model({
          output_modalities: ['text', 'image', 'video', 'audio'],
          supported_endpoint_types: ['image-generation', 'openai-video'],
        })
      )
    ).toEqual(['text', 'images', 'video', 'audio'])
  })
  it('groups embedding and rerank protocols under vectors', () => {
    expect(
      getModelCategories(
        model({
          supported_endpoint_types: ['embeddings', 'jina-rerank'],
          capabilities: ['embeddings'],
        })
      )
    ).toEqual(['vectors'])
  })
  it('uses explicit capabilities when modalities and endpoints are absent', () => {
    expect(
      getModelCategories(
        model({ capabilities: ['image_generation', 'speech_recognition'] })
      )
    ).toEqual(['images', 'audio'])
  })
})

describe('manual display categories', () => {
  it('accepts decision only as a manual display category, without guessing names or changing chat capability', () => {
    const automatic = model({
      model_name: 'decision-reasoning-model',
      output_modalities: ['text'],
      capabilities: ['reasoning'],
      supported_endpoint_types: ['openai'],
    })
    expect(getModelCategories(automatic)).toEqual(['text'])
    expect(filterByCategory([automatic], 'decision')).toEqual([])
    const manual = {
      ...automatic,
      display_categories: [
        'decision',
        'text',
        'decision',
      ] as PricingModel['display_categories'],
    }
    expect(getModelCategories(manual)).toEqual(['text', 'decision'])
    expect(filterByCategory([manual], 'decision')).toEqual([manual])
    expect(
      getModelEndpoints(manual, { openai: { path: '/v1/chat/completions' } })
    ).toEqual(
      getModelEndpoints(automatic, {
        openai: { path: '/v1/chat/completions' },
      })
    )
    expect(getModelPlayground(manual)).toBe('/playground')
    expect(getModelCategories({ ...manual, display_categories: [] })).toEqual([
      'text',
    ])
  })

  it('replaces automatic categories, supports multiple values and counts each once', () => {
    const item = model({
      output_modalities: ['text'],
      display_categories: ['video', 'images', 'images'],
    })
    expect(getModelCategories(item)).toEqual(['images', 'video'])
    expect(filterByCategory([item], 'text')).toEqual([])
    expect(filterByCategory([item], 'images')).toEqual([item])
    expect(filterByCategory([item], 'video')).toEqual([item])
    expect(filterByCategory([item], 'all')).toEqual([item])
  })
  it.each([undefined, null, [], ['all'], ['images', 'unknown'], 'text'])(
    'falls back to automatic classification for absent or invalid overrides: %j',
    (override) => {
      const item = model({
        output_modalities: ['text'],
        display_categories: override as PricingModel['display_categories'],
      })
      expect(getModelCategories(item)).toEqual(['text'])
    }
  )
  it('does not change real endpoints or playground when text is displayed as images', () => {
    const item = model({
      output_modalities: ['text'],
      supported_endpoint_types: ['openai'],
      display_categories: ['images'],
    })
    expect(
      getModelEndpoints(item, { openai: { path: '/v1/chat/completions' } })
    ).toEqual([
      { type: 'openai', path: '/v1/chat/completions', method: 'POST' },
    ])
    expect(getModelPlayground(item)).toBe('/playground')
  })
  it('does not invent text capabilities when an image model is displayed as text', () => {
    const item = model({
      supported_endpoint_types: ['openai', 'image-generation'],
      display_categories: ['text'],
    })
    expect(
      getModelEndpoints(item, {
        openai: { path: '/v1/chat/completions' },
        'image-generation': { path: '/v1/images/generations' },
      })
    ).toEqual([
      {
        type: 'image-generation',
        path: '/v1/images/generations',
        method: 'POST',
      },
    ])
    expect(getModelPlayground(item)).toBe('/image')
  })
})
