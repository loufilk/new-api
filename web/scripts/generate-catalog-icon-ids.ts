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
// Run with Bun from web/ after an @lobehub/icons registry change.
import { writeFileSync } from 'node:fs'

import { catalogIconVariants } from '../src/lib/catalog-icon-registry'

const entries = [...catalogIconVariants].sort(([a], [b]) =>
  a < b ? -1 : Number(a > b)
)
const lines = entries.map(
  ([id, variants]) => `  ${JSON.stringify(id)}: ${JSON.stringify(variants)}`
)
writeFileSync(
  new URL('../../model/catalog_icon_ids.json', import.meta.url),
  `{\n${lines.join(',\n')}\n}\n`
)
