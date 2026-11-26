import { describe, expect, test } from 'vitest'
import { validateGeminiVersionOverrides } from '../utils'

describe('Gemini version overrides', () => {
  test('allows a model-only map and each supported version', () => {
    for (const value of [
      '{}',
      '{"other":"v1alpha"}',
      '{"default":"v1beta","model":"v1"}',
    ]) {
      expect(validateGeminiVersionOverrides(value).valid).toBe(true)
    }
  })
  test('rejects empty input, non-maps and unsupported values', () => {
    for (const value of [
      '',
      'null',
      '[]',
      '{"default":""}',
      '{"model":null}',
      '{"model":3}',
      '{"model":"v9"}',
      '{"model":"v1/models"}',
    ]) {
      expect(validateGeminiVersionOverrides(value).valid).toBe(false)
    }
  })
})
