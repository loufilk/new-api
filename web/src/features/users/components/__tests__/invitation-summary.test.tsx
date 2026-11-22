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
import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'

import type { User } from '../../types'
import { UserInvitationCell } from '../user-invitation-cell'

const user: User = {
  id: 42,
  username: 'invitee',
  display_name: '',
  role: 1,
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'default',
  status: 1,
}

it('shows ordinary invitation counts and inviter attribution without reseller test-grant details', () => {
  render(<UserInvitationCell user={{ ...user, aff_count: 3, inviter_id: 7 }} />)
  expect(screen.getByText('Invited 3 users')).toBeInTheDocument()
  expect(screen.getByText('Inviter ID: 7')).toBeInTheDocument()
  expect(screen.queryByText(/Test quota granted/)).not.toBeInTheDocument()
})

it('shows an empty marker when a user has no ordinary invitation activity', () => {
  render(<UserInvitationCell user={user} />)
  expect(screen.getByText('—')).toBeInTheDocument()
})
