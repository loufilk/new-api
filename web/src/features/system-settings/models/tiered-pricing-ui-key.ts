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
const uiKeys = new WeakMap<object, string>()
let uiKeySequence = 0

export function getStableUiKey(value: object): string {
  const existing = uiKeys.get(value)
  if (existing) return existing

  const key = `ui-${++uiKeySequence}`
  uiKeys.set(value, key)
  return key
}

export function carryStableUiKey<T extends object>(
  source: object,
  replacement: T
): T {
  uiKeys.set(replacement, getStableUiKey(source))
  return replacement
}
