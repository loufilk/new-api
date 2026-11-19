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
import type { TOptions } from 'i18next'

export type TextTranslationOptions<
  Values extends object = Record<string, unknown>,
> = TOptions<Values> & {
  defaultValue?: string
  returnObjects?: false
  returnDetails?: false
}

/** Text-only translations used by helpers that accept a hook's or instance's t. */
export interface TranslateText<
  Values extends object = Record<string, unknown>,
> {
  (key: string): string
  (key: string, options: TextTranslationOptions<Values>): string
}
