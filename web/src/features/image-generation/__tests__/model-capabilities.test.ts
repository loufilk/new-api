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
import { describe, expect, test } from 'vitest'

import {
  getImageOrientation,
  getModelCapability,
  parseSizeDimensions,
  resolveSettingsForModel,
} from '../lib/model-capabilities'

describe('getModelCapability resolves image model families', () => {
  test('the dedicated Console model enables ratios, low/medium quality and three references without changing Web', () => {
    const consoleModel = getModelCapability('grok-imagine-image-2.0-console')
    expect(consoleModel.sizes).toContain('16:9')
    expect(consoleModel.qualities).toEqual(['low', 'medium'])
    expect(consoleModel.maxReferenceImages).toBe(3)
    expect(getModelCapability('grok-imagine-image-2.0').qualities).toEqual([])
    expect(
      getModelCapability('grok-imagine-image-2.0').maxReferenceImages
    ).toBe(0)
  })
  test('dall-e-3 exposes three sizes, hd quality, two styles, single image, no references', () => {
    const capability = getModelCapability('dall-e-3')
    expect(capability.sizes).toEqual(['1024x1024', '1792x1024', '1024x1792'])
    expect(capability.qualities).toEqual(['standard', 'hd'])
    expect(capability.styles).toEqual(['vivid', 'natural'])
    expect(capability.maxN).toBe(1)
    expect(capability.maxReferenceImages).toBe(0)
  })

  test('other dall-e exposes 256/512/1024 sizes, up to 4 images, one reference', () => {
    const capability = getModelCapability('dall-e-2')
    expect(capability.sizes).toEqual(['256x256', '512x512', '1024x1024'])
    expect(capability.maxN).toBe(4)
    expect(capability.maxReferenceImages).toBe(1)
  })

  test('gpt-image variants expose auto sizes, four quality levels, four references', () => {
    expect(getModelCapability('gpt-image-2-4k').sizes[0]).toBe('auto')
    expect(getModelCapability('gpt-image-2-2k').sizes).toContain('2048x2048')
    expect(getModelCapability('gpt-image-2').sizes).toContain('1536x864')
    expect(getModelCapability('gpt-image-1').qualities).toEqual([
      'auto',
      'low',
      'medium',
      'high',
    ])
    expect(getModelCapability('gpt-image-1').maxReferenceImages).toBe(4)
  })

  test('grok enforces no size/quality/style options with four images and no references', () => {
    const capability = getModelCapability('grok-2-image')
    expect(capability.sizes).toEqual([])
    expect(capability.qualities).toEqual([])
    expect(capability.styles).toEqual([])
    expect(capability.maxN).toBe(4)
    expect(capability.maxReferenceImages).toBe(0)
  })

  test('generic models fall back to auto sizes with four images and no references', () => {
    const capability = getModelCapability('some-unknown-model')
    expect(capability.sizes[0]).toBe('auto')
    expect(capability.maxN).toBe(4)
    expect(capability.maxReferenceImages).toBe(0)
  })
})

describe('resolveSettingsForModel corrects stale form values on model switch', () => {
  test('dall-e-3 forces count to 1 and resets an unsupported size', () => {
    const resolved = resolveSettingsForModel('dall-e-3', {
      size: '256x256',
      quality: 'hd',
      style: 'natural',
      count: 4,
    })
    expect(resolved.count).toBe(1)
    expect(resolved.size).toBe('1024x1024')
    expect(resolved.quality).toBe('hd')
  })

  test('switching to grok clears size/quality/style selections', () => {
    const resolved = resolveSettingsForModel('grok-beta', {
      size: '1024x1024',
      quality: 'high',
      style: 'vivid',
      count: 3,
    })
    expect(resolved.size).toBe('')
    expect(resolved.quality).toBe('')
    expect(resolved.style).toBe('')
    expect(resolved.count).toBe(3)
  })

  test('valid selections survive a compatible model switch', () => {
    const resolved = resolveSettingsForModel('gpt-image-2', {
      size: '1024x1024',
      quality: 'medium',
      style: '',
      count: 2,
    })
    expect(resolved).toEqual({
      size: '1024x1024',
      quality: 'medium',
      style: '',
      count: 2,
    })
  })
})

describe('orientation and size helpers', () => {
  test('square/landscape/portrait/unknown map from dimensions', () => {
    expect(getImageOrientation(1024, 1024)).toBe('square')
    expect(getImageOrientation(1792, 1024)).toBe('landscape')
    expect(getImageOrientation(1024, 1792)).toBe('portrait')
    expect(getImageOrientation(undefined, 1024)).toBe('unknown')
  })

  test('parseSizeDimensions reads WxH strings and rejects auto', () => {
    expect(parseSizeDimensions('1536x1024')).toEqual({
      width: 1536,
      height: 1024,
    })
    expect(parseSizeDimensions('auto')).toEqual({})
  })
})
