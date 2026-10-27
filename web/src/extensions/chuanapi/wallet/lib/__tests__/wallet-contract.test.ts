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
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, test } from 'vitest'

const walletDir = resolve(dirname(fileURLToPath(import.meta.url)), '../..')

function source(relativePath: string): string {
  return readFileSync(resolve(walletDir, relativePath), 'utf8')
}

describe('ChuanAPI wallet surface contract', () => {
  test('wallet page composes the supported funding surfaces and the native transfer dialog', () => {
    const wallet = source('index.tsx')
    const required = [
      'WalletStatsCard',
      'RechargeFormCard',
      'AffiliateRewardsCard',
      'LedgerTable',
      'TransferDialog',
    ]
    for (const component of required) {
      expect(wallet).toMatch(new RegExp(`<${component}`))
    }
    expect(wallet).not.toMatch(
      /WalletAccountsCard|RedemptionCard|SubscriptionPlansCard|BillingHistoryDialog|ReferralRewardsCard|ReferralHistoryCard/
    )
  })

  test('recharge form gates redemption behind explicit backend opt-in', () => {
    const form = source('components/recharge-form-card.tsx')
    expect(form).toMatch(/topupInfo\?\.enable_redemption === true/)
    expect(form).not.toMatch(/enable_redemption !== false/)
  })

  test('wallet summary combines bonus and reward with a scope hint', () => {
    const stats = source('components/wallet-stats-card.tsx')
    for (const label of [
      'Paid Balance',
      'Bonus Balance',
      'Total Usage',
      'API Requests',
    ]) {
      expect(stats).toContain(`t('${label}')`)
    }
    expect(stats).toMatch(/summary\?\.reward/)
    expect(stats).toContain(
      "'Signup bonus applies to eligible models; invitation rewards apply to all models'"
    )
    expect(stats).not.toMatch(/t\('(Current Balance|Total Balance)'\)/)
  })

  test('ledger filters expose paid, bonus, and reward but never affiliate', () => {
    const ledger = source('components/ledger-table.tsx')
    expect(ledger).toMatch(/value: 'paid'/)
    expect(ledger).toMatch(/value: 'bonus'/)
    expect(ledger).not.toMatch(/value: 'affiliate'/)
  })
})
