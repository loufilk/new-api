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
import { z } from 'zod'

const snapshotSchema = z.object({
  agreement_id: z.number().int().nonnegative(),
  agreement_version: z.number().int().nonnegative(),
  agreement_state_version: z.number().int().nonnegative(),
  tier_key: z.string(),
  tier_version: z.number().int().nonnegative(),
  policy_version: z.number().int().nonnegative(),
  profile_version: z.number().int().nonnegative(),
  currency: z.string().regex(/^[A-Z]{3}$/),
  currency_exponent: z.number().int().min(0).max(6),
  quota: z.number().int().positive(),
  quota_numerator: z.string().regex(/^[1-9]\d*$/),
  quota_denominator: z.string().regex(/^[1-9]\d*$/),
  rate_bps: z.number().int().min(1).max(10000),
  face_minor: z.string().regex(/^[1-9]\d*$/),
  base_paid_minor: z.string().regex(/^[1-9]\d*$/),
  paid_minor: z.string().regex(/^[1-9]\d*$/),
  hit_reason: z.string(),
})
export function parsePriceSnapshot(
  raw: string
): z.infer<typeof snapshotSchema> | null {
  try {
    const result = snapshotSchema.safeParse(JSON.parse(raw))
    return result.success ? result.data : null
  } catch {
    return null
  }
}
