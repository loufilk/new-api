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
// ============================================================================
// Affiliate Functions
// ============================================================================

/**
 * Generate affiliate registration link
 */
export function generateAffiliateLink(affCode: string): string {
  if (typeof window === 'undefined') return ''
  return `${window.location.origin}/sign-up?aff=${affCode}`
}

/**
 * Render a reward ratio as a percentage string driven by the backend rule.
 * Whole percentages keep one decimal (0.05 -> "5.0%") so the UI matches the
 * configured 5.0% rate.
 */
export function formatReferralRate(rate: number): string {
  if (!Number.isFinite(rate) || rate <= 0) return '0%'
  const percent = rate * 100
  return Number.isInteger(percent)
    ? `${percent.toFixed(1)}%`
    : `${percent.toFixed(2).replace(/0+$/, '').replace(/\.$/, '')}%`
}
