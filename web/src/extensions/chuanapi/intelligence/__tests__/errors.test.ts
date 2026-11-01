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
import { describe, expect, test } from 'vitest'

import { safeServerErrorMessage } from '@/lib/server-error-message'

import { modelTestErrorKey } from '../errors'

describe('model test errors', () => {
  test('maps structured API error codes without showing generic request-failed copy', () => {
    expect(
      modelTestErrorKey({
        response: {
          data: {
            success: false,
            code: 'rate_or_concurrency_limit',
            message: 'Model test request could not be completed',
          },
        },
      })
    ).toBe(
      'The test limit has been reached. Finish an active test or try again later.'
    )
  })
  test('does not unwrap deliberately masked authentication failures', () => {
    expect(
      modelTestErrorKey({
        [safeServerErrorMessage]: true,
        message: 'Safe auth message',
        cause: { code: 'request_id_conflict' },
      })
    ).toBeUndefined()
  })
})
