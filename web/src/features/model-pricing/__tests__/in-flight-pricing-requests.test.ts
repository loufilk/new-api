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
import { expect, test } from 'vitest'

import { api } from '@/lib/http-client'

import { getModelPricing } from '../api'

test('overlapping full, list and individual pricing reads receive only their own entries', async () => {
  const original = api.defaults.adapter
  const dispatched: string[][] = []
  let release!: () => void
  const gate = new Promise<void>((resolve) => (release = resolve))
  api.defaults.adapter = async (config) => {
    const names = new URLSearchParams(api.getUri(config).split('?')[1]).getAll(
      'model'
    )
    dispatched.push(names)
    await gate
    return {
      config,
      status: 200,
      statusText: 'OK',
      headers: {},
      data: {
        success: true,
        data: {
          entries: (names.length ? names : ['full-catalog']).map(
            (model_name) => ({
              model_name,
            })
          ),
        },
      },
    }
  }
  try {
    const subset = ['vendor/a+v2', 'vendor/b&x']
    const requests = [
      getModelPricing(),
      getModelPricing(subset),
      getModelPricing([subset[1]]),
      getModelPricing([...subset]), // distinct URLSearchParams, same wire URL
    ]
    const noParams = api.get('/api/option/model_pricing')
    release()
    await noParams
    const results = await Promise.all(requests)
    expect(
      results.map(({ entries }) => entries.map((e) => e.model_name))
    ).toEqual([['full-catalog'], subset, [subset[1]], subset])
    expect(dispatched).toEqual([[], subset, [subset[1]]])
  } finally {
    release()
    api.defaults.adapter = original
  }
})
