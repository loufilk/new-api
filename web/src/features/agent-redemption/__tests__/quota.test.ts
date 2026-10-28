import { describe, expect, it } from 'vitest'

import { canAllocateQuota, canReserveQuota, getBatchQuota } from '../lib/quota'

describe('integer quota budget', () => {
  it('accepts an exact available balance for a batch', () => {
    expect(canReserveQuota(500001, 3, 1500003)).toBe(true)
  })
  it('rejects a batch one integer unit above available balance', () => {
    expect(canReserveQuota(500001, 3, 1500002)).toBe(false)
  })
  it.each([0, -1, 1.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1])('rejects invalid quota %s', (quota) => {
    expect(getBatchQuota(quota, 1)).toBeNull()
  })
  it.each([0, -1, 1.5, 101])('rejects invalid batch count %s', (count) => {
    expect(getBatchQuota(1, count)).toBeNull()
  })
  it('rejects multiplication overflow before sending the batch', () => {
    expect(getBatchQuota(Number.MAX_SAFE_INTEGER, 2)).toBeNull()
  })
  it('allows reclaiming only the available quota', () => {
    expect(canAllocateQuota(-100, 100)).toBe(true)
    expect(canAllocateQuota(-101, 100)).toBe(false)
  })
  it('rejects allocation overflow and zero adjustments', () => {
    expect(canAllocateQuota(1, Number.MAX_SAFE_INTEGER)).toBe(false)
    expect(canAllocateQuota(0, 100)).toBe(false)
  })
})
