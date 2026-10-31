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
export interface PendingOperation {
  request_id: string
  action: string
  resource: string
}
const key = (userId: number) => `agent-platform:pending:${userId}`

/** Only non-secret identifiers are retained. Never store payloads, recipients or proofs. */
export function pendingOperations(userId: number): PendingOperation[] {
  try {
    const value: unknown = JSON.parse(
      sessionStorage.getItem(key(userId)) ?? '[]'
    )
    if (!Array.isArray(value)) return []
    return value
      .filter((entry): entry is PendingOperation =>
        Boolean(
          entry &&
          typeof entry === 'object' &&
          typeof entry.request_id === 'string' &&
          /^[a-zA-Z0-9-]{1,100}$/.test(entry.request_id) &&
          typeof entry.action === 'string' &&
          typeof entry.resource === 'string' &&
          /^\/(?:[a-z-]+\/)*[a-z-]+$/.test(entry.resource)
        )
      )
      .map((entry) => ({
        request_id: entry.request_id,
        action: entry.action,
        resource: entry.resource,
      }))
  } catch {
    return []
  }
}

export function rememberOperation(
  userId: number,
  operation: PendingOperation
): void {
  try {
    const pending = pendingOperations(userId).filter(
      (entry) => entry.request_id !== operation.request_id
    )
    sessionStorage.setItem(
      key(userId),
      JSON.stringify([
        ...pending,
        {
          request_id: operation.request_id,
          action: operation.action,
          resource: operation.resource,
        },
      ])
    )
    window.dispatchEvent(new Event('agent-platform-pending-changed'))
  } catch {
    /* The visible business number still supports manual recovery. */
  }
}

export function forgetOperation(userId: number, requestId: string): void {
  try {
    sessionStorage.setItem(
      key(userId),
      JSON.stringify(
        pendingOperations(userId).filter(
          (entry) => entry.request_id !== requestId
        )
      )
    )
    window.dispatchEvent(new Event('agent-platform-pending-changed'))
  } catch {
    /* No secrets are retained. */
  }
}
