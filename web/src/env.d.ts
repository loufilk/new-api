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
/// <reference types="@rsbuild/core/types" />

interface ImportMetaEnv {
  /** Release version stamped into the compiled frontend by CI. */
  readonly VITE_REACT_APP_VERSION?: string
  /**
   * Optional API origin for deployments that host the console separately from
   * the API. Empty or absent keeps all app-owned requests on the frontend
   * origin (same-origin dev proxy included).
   */
  readonly VITE_API_BASE_URL?: string
}

declare module '@visactor/react-vchart' {
  export const VChart: React.ComponentType<Record<string, unknown>>
}
