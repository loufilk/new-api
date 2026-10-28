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
import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, test, vi } from 'vitest'

const toastError = vi.fn()
vi.mock('sonner', () => ({
  toast: {
    error: (...args: unknown[]) => toastError(...args),
  },
}))

const apiState = {
  handler: async (_target: unknown) => ({
    success: true,
    data: { rule_name: 'rule-a', hit: 3, total: 4 },
  }),
}

vi.mock('../api', () => ({
  getAffinityUsageCache: (target: unknown) => apiState.handler(target),
}))

const { CacheStatsDialog } = await import('../cache-stats-dialog')

function target(overrides = {}) {
  return {
    rule_name: 'rule-a',
    using_group: 'default',
    key_hint: 'hint',
    key_fp: 'fp-1',
    ...overrides,
  }
}

describe('cache stats dialog lifecycle', () => {
  test('shows loading then cached stats for the current target', async () => {
    apiState.handler = async () => ({
      success: true,
      data: { rule_name: 'rule-a', hit: 3, total: 4 },
    })

    render(
      <CacheStatsDialog open onOpenChange={() => undefined} target={target()} />
    )

    expect(screen.getByText('Loading...')).toBeInTheDocument()

    await waitFor(() => {
      expect(screen.getByText('3/4 (75.00%)')).toBeInTheDocument()
    })
  })

  test('surfaces request failures as a toast and clears loading', async () => {
    toastError.mockClear()
    apiState.handler = async () => {
      throw new Error('network down')
    }

    render(
      <CacheStatsDialog open onOpenChange={() => undefined} target={target()} />
    )

    await waitFor(() => {
      expect(toastError).toHaveBeenCalledTimes(1)
    })
    expect(screen.getByText('No data available')).toBeInTheDocument()
  })

  test('ignores stale responses after the target switches', async () => {
    let resolveFirst: (value: unknown) => void = () => undefined
    const first = new Promise((resolve) => {
      resolveFirst = resolve as (value: unknown) => void
    })
    apiState.handler = ((call: number) => async () => {
      call += 1
      if (call === 1) {
        await first
        return {
          success: true,
          data: { rule_name: 'rule-a', hit: 1, total: 2 },
        }
      }
      return {
        success: true,
        data: { rule_name: 'rule-b', hit: 3, total: 4 },
      }
    })(0)

    const { rerender } = render(
      <CacheStatsDialog open onOpenChange={() => undefined} target={target()} />
    )
    rerender(
      <CacheStatsDialog
        open
        onOpenChange={() => undefined}
        target={target({ rule_name: 'rule-b', key_fp: 'fp-2' })}
      />
    )

    resolveFirst(undefined)

    await waitFor(() => {
      expect(screen.getByText('rule-b')).toBeInTheDocument()
    })
    expect(screen.queryByText('rule-a')).toBeNull()
  })
})
