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
import { Lexer } from 'marked'

export interface DocHeading {
  id: string
  label: string
  depth: 2 | 3
}

export function getDocHeadings(markdown: string): readonly DocHeading[] {
  const used = new Set<string>()
  return Lexer.lex(markdown).flatMap((token) => {
    if (token.type !== 'heading' || (token.depth !== 2 && token.depth !== 3)) {
      return []
    }
    const label = token.text
      .replaceAll(/\[([^\]]+)\]\([^)]+\)/gu, '$1')
      .replaceAll(/[*_`~]/gu, '')
    const stem = label
      .normalize('NFKC')
      .toLowerCase()
      .replaceAll(/[^\p{L}\p{N}]+/gu, '-')
      .replaceAll(/^-|-$/gu, '')
    const base = `section-${stem || 'heading'}`
    let id = base
    let sequence = 2
    while (used.has(id)) {
      id = `${base}-${sequence}`
      sequence += 1
    }
    used.add(id)
    return [{ id, label, depth: token.depth }]
  })
}
