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
import type { FunctionComponent } from 'react'

export type MonitorExtension = {
  id: string
  page: FunctionComponent
  settingsPage: FunctionComponent
}

let monitorExtension: MonitorExtension | null = null

export function setMonitorExtension(extension: MonitorExtension) {
  if (monitorExtension !== null) {
    throw new Error('frontend monitor extension is already configured')
  }
  monitorExtension = Object.freeze({ ...extension })
}

export function extensionMonitorPage(): MonitorExtension {
  if (monitorExtension === null) {
    throw new Error('frontend monitor extension is not configured')
  }
  return monitorExtension
}
