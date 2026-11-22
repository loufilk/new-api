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
import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import { imageHistoryStorageKey } from '../storage/history-owner'

export const HISTORY_OWNER_ID = 101
export const HISTORY_OWNER_KEY = imageHistoryStorageKey(HISTORY_OWNER_ID) ?? ''
export function signInHistoryOwner(id = HISTORY_OWNER_ID) {
  const now = Math.floor(Date.now() / 1000)
  const bundle: AuthBundle = {
    access_token: `synthetic-access-${id}`,
    token_type: 'Bearer',
    access_expires_at: now + 3600,
    user: { id, username: `synthetic-user-${id}`, role: 1 },
    session: {
      sid: `synthetic-session-${id}`,
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: now,
      last_active_at: now,
      expires_at: now + 3600,
    },
  }
  useAuthStore.getState().auth.setBundle(bundle)
}
