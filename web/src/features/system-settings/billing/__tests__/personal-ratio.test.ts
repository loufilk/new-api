import { describe, expect, it } from 'vitest'

import { normalizePersonalRatio } from '../personal-ratio'

describe('personal rate input', () => {
  it('keeps explicit zero and precision separate from an empty draft', () => {
    expect(normalizePersonalRatio('0')).toBe('0')
    expect(normalizePersonalRatio(' 0.00000001 ')).toBe('0.00000001')
    expect(normalizePersonalRatio('10000')).toBe('10000')
    expect(normalizePersonalRatio('')).toBeNull()
  })

  it('rejects negative, scientific, oversized and over-precise values', () => {
    for (const value of ['-1', '1e-5', '10000.01', '0.123456789', '1.']) {
      expect(normalizePersonalRatio(value)).toBeNull()
    }
  })
})
