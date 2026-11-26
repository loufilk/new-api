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
import { Address4, Address6 } from 'ip-address'

import type { ApiKey, ApiKeyFormData, ApiKeyUpdateData } from '../types'

const daySeconds = 86_400

export type SingleGroupKeyValues = {
  name: string
  group: string
  quota: string
  days: string
  modelLimited: boolean
  models: string[]
  ips: string
  allowUserQuotaQuery: boolean
}

export function isLegacyApiKey(key: ApiKey) {
  return !key.group || key.group === 'auto' || key.smart_routing === true
}

export function initialKeyValues(
  key: ApiKey | undefined,
  quotaPerUSD: number,
  nowSeconds: number
): SingleGroupKeyValues {
  return {
    name: key?.name ?? '',
    group: key && !isLegacyApiKey(key) ? (key.group ?? '') : '',
    quota:
      key && !key.unlimited_quota ? String(key.remain_quota / quotaPerUSD) : '',
    days:
      key && key.expired_time !== -1
        ? String(
            Math.max(0, Math.ceil((key.expired_time - nowSeconds) / daySeconds))
          )
        : '',
    modelLimited: key?.model_limits_enabled ?? false,
    models: key?.model_limits?.split(',').filter(Boolean) ?? [],
    ips: key?.allow_ips ?? '',
    allowUserQuotaQuery: key?.allow_user_quota_query ?? true,
  }
}

export function quotaUnits(raw: string, quotaPerUSD: number) {
  if (!raw.trim()) return 0
  if (!/^(?:\d+(?:\.\d*)?|\.\d+)$/.test(raw.trim())) return Number.NaN
  const amount = Number(raw)
  const units = Math.round(amount * quotaPerUSD)
  return amount <= 1_000_000_000 && Number.isSafeInteger(units)
    ? units
    : Number.NaN
}

export function expirySeconds(raw: string, nowSeconds: number) {
  if (!raw.trim()) return -1
  if (!/^\d+$/.test(raw.trim())) return Number.NaN
  const days = Number(raw)
  const expiry = nowSeconds + days * daySeconds
  return days > 0 && Number.isSafeInteger(expiry) && expiry <= 253_402_300_799
    ? expiry
    : Number.NaN
}

export function parseKeyIpAllowlist(raw: string): string | undefined {
  if (!raw.trim()) return ''
  const entries = raw
    .split(/[\r\n,]+/)
    .map((ip) => ip.trim())
    .filter(Boolean)
  if (
    !entries.length ||
    entries.some(
      (ip) =>
        ip.includes('%') || (!Address4.isValid(ip) && !Address6.isValid(ip))
    )
  ) {
    return undefined
  }
  // Server stores one address/network per line. Preserve unchanged legacy text
  // on sparse edits, but normalize new or changed input to that exact format.
  return [...new Set(entries)].join('\n')
}

export function createFixedKeyPayload(
  values: SingleGroupKeyValues,
  quotaPerUSD: number,
  nowSeconds: number
): ApiKeyFormData {
  return {
    name: values.name.trim(),
    group: values.group,
    remain_quota: quotaUnits(values.quota, quotaPerUSD),
    unlimited_quota: values.quota.trim() === '',
    expired_time: expirySeconds(values.days, nowSeconds),
    model_limits_enabled: values.modelLimited,
    model_limits: values.models.join(','),
    allow_ips: parseKeyIpAllowlist(values.ips) ?? values.ips.trim(),
    allow_user_quota_query: values.allowUserQuotaQuery,
  }
}

export function updateFixedKeyPayload(
  key: ApiKey,
  values: SingleGroupKeyValues,
  original: SingleGroupKeyValues,
  quotaPerUSD: number,
  nowSeconds: number
): ApiKeyUpdateData {
  const payload: ApiKeyUpdateData = {
    id: key.id,
    expected_version: key.settings_version,
  }
  if (values.name !== original.name) payload.name = values.name.trim()
  // No-op edits don't rewrite routing, recredit a balance or extend expiry.
  if (values.group !== original.group && values.group) {
    payload.group = values.group
  }
  if (values.quota !== original.quota) {
    payload.unlimited_quota = values.quota.trim() === ''
    if (!payload.unlimited_quota) {
      payload.remain_quota = quotaUnits(values.quota, quotaPerUSD)
    }
  }
  if (values.days !== original.days) {
    payload.expired_time = expirySeconds(values.days, nowSeconds)
  }
  if (values.modelLimited !== original.modelLimited) {
    payload.model_limits_enabled = values.modelLimited
  }
  if (values.models.join(',') !== original.models.join(',')) {
    payload.model_limits = values.models.join(',')
  }
  if (values.ips !== original.ips) {
    payload.allow_ips = parseKeyIpAllowlist(values.ips) ?? values.ips.trim()
  }
  if (values.allowUserQuotaQuery !== original.allowUserQuotaQuery) {
    payload.allow_user_quota_query = values.allowUserQuotaQuery
  }
  return payload
}
