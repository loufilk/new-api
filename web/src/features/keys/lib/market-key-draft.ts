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
import { getAuthSessionEpoch } from '@/lib/auth-session'

export type MarketKeyDraft = { group?: string; model?: string }

let pending:
  | {
      draft: MarketKeyDraft
      owner: number
      until: number
    }
  | undefined

// Private catalogue identities stay out of URLs, browser history and storage.
export function stagePrivateMarketKeyDraft(group: string, model?: string) {
  pending = {
    draft: { group, model },
    owner: getAuthSessionEpoch(),
    until: Date.now() + 60_000,
  }
}

export function consumePrivateMarketKeyDraft(): MarketKeyDraft | undefined {
  const previous = pending
  pending = undefined
  if (
    previous &&
    previous.owner === getAuthSessionEpoch() &&
    previous.until >= Date.now()
  ) {
    return previous.draft
  }
}
