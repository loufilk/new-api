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

export const withdrawalConfigSchema = z.object({
  revision: z.number().int().nonnegative(),
  enabled: z.boolean(),
  min_consumed_quota: z.number().int().min(0).max(Number.MAX_SAFE_INTEGER),
  min_withdrawal_quota: z.number().int().min(1).max(Number.MAX_SAFE_INTEGER),
  quota_per_usdt: z.number().int().min(1).max(1_000_000_000_000),
  bsc_fee_bps: z.number().int().min(0).max(9999),
  polygon_fee_bps: z.number().int().min(0).max(9999),
  trc20_fee_bps: z.number().int().min(0).max(9999),
  instructions: z.string().max(3000),
  updated_at: z.number(),
  updated_by: z.number(),
})
