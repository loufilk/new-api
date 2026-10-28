/** All budget arithmetic runs in integer quota units, never display currency. */
export function getBatchQuota(quota: number, count: number): number | null {
  if (!Number.isSafeInteger(quota) || quota <= 0 || !Number.isSafeInteger(count) || count < 1 || count > 100) return null
  const total = quota * count
  return Number.isSafeInteger(total) ? total : null
}

export function canReserveQuota(quota: number, count: number, available: number): boolean {
  const total = getBatchQuota(quota, count)
  return total !== null && Number.isSafeInteger(available) && available >= total
}

export function canAllocateQuota(delta: number, available: number): boolean {
  return Number.isSafeInteger(delta) && delta !== 0 && Number.isSafeInteger(available) && available >= 0 && Number.isSafeInteger(available + delta) && available + delta >= 0
}
