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
import { describe, expect, test, vi } from 'vitest'

import '@/extensions/bootstrap'
import { extensionWalletPage } from '@/extensions'
import { api } from '@/lib/api'

describe('wallet extension composition', () => {
  test('publishes the wallet page through the generic bridge', () => {
    const wallet = extensionWalletPage()

    expect(wallet.id).toBe('chuanapi/wallet')
    expect(typeof wallet.page).toBe('function')
  })

  test('preserves the complete wallet cache response while exposing the paid balance capability', async () => {
    const summary = { paid: 15, bonus: 20, reward: 30, total: 65 }
    const get = vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: summary },
    })
    expect(await extensionWalletPage().loadSummary()).toEqual(summary)
    expect(get).toHaveBeenCalledWith('/api/user/wallet')
  })

  test('failed wallet responses cannot provide a spendable cached balance', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: false,
        message: 'Wallet unavailable',
        data: { paid: 15 },
      },
    })
    await expect(extensionWalletPage().loadSummary()).rejects.toThrow(
      'Wallet unavailable'
    )
  })
})
