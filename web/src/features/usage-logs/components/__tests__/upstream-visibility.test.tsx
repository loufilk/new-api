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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, test } from 'vitest'

import { usageLogSchema } from '../../data/schema'
import { formatModelName } from '../../lib/format'
import { DetailsDialog } from '../dialogs/details-dialog'

const observation = {
  requested_model: 'client-model',
  upstream_model: 'private-upstream-model',
  returned_model: 'private-returned-model',
  mismatch: true,
}
const diagnostics = {
  upstream_model_name: 'private-upstream-model',
  response_model: observation,
  po: ['set model = private-override'],
  request_conversion: ['private-protocol-a', 'private-protocol-b'],
  stream_status: { status: 'error', end_error: 'private-stream-failure' },
}

afterEach(cleanup)

test.each([
  { scoped: false, isAdmin: false },
  { scoped: true, isAdmin: false },
  { scoped: false, isAdmin: true },
  { scoped: true, isAdmin: true },
])(
  'diagnostics scoped=$scoped are only visible in admin view=$isAdmin',
  (entry) => {
    const log = usageLogSchema.parse({
      id: 1,
      user_id: 1,
      created_at: 1,
      type: 2,
      model_name: 'client-model',
      content: '',
      upstream_request_id: 'private-upstream-request',
      other: JSON.stringify({
        is_model_mapped: true,
        ...(entry.scoped ? { admin_info: diagnostics } : diagnostics),
      }),
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    client.setQueryData(['status'], {}, { updatedAt: Date.now() + 60000 })
    render(
      <QueryClientProvider client={client}>
        <DetailsDialog
          log={log}
          isAdmin={entry.isAdmin}
          isRoot={false}
          open
          onOpenChange={() => undefined}
        />
      </QueryClientProvider>
    )
    const model = formatModelName(log, entry.isAdmin)
    expect(model.name).toBe('client-model')
    if (entry.isAdmin) {
      expect(screen.getByText('private-upstream-model')).toBeVisible()
      expect(screen.getByText('private-returned-model')).toBeVisible()
      expect(screen.getByText('model = private-override')).toBeVisible()
      expect(screen.getByText('private-stream-failure')).toBeVisible()
      expect(
        screen.getByText('private-protocol-a -> private-protocol-b')
      ).toBeVisible()
      expect(screen.getByText('private-upstream-request')).toBeVisible()
      expect(model.actualModel).toBe('private-upstream-model')
      expect(model.responseModel).toEqual(observation)
    } else {
      expect(screen.queryByText(/private-/)).not.toBeInTheDocument()
      expect(model.actualModel).toBeUndefined()
      expect(model.responseModel).toBeUndefined()
    }
    client.clear()
  }
)
