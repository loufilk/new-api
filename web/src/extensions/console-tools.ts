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
import type { QueryClient } from '@tanstack/react-query'
import type { ReactNode } from 'react'

export type ModelTestAvailability = { enabled: boolean }

export type ConsoleToolsExtension = {
  id: string
  accountPoolPage: () => ReactNode
  accountPoolDiagnostics: () => ReactNode
  verifyAccountPoolAccess: (client: QueryClient) => Promise<boolean>
  affiliatePage: () => ReactNode
  intelligencePage: () => ReactNode
}

let consoleToolsExtension: ConsoleToolsExtension | null = null

export function setConsoleToolsExtension(extension: ConsoleToolsExtension) {
  if (consoleToolsExtension !== null) {
    throw new Error('frontend console tools extension is already configured')
  }
  consoleToolsExtension = Object.freeze({ ...extension })
}

export function extensionConsoleTools(): ConsoleToolsExtension {
  if (consoleToolsExtension === null) {
    throw new Error('frontend console tools extension is not configured')
  }
  return consoleToolsExtension
}
