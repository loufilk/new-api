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

import { Award, Building2, FlaskConical, Gift, Shield } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

const badges = {
  self_operated: {
    label: 'Self-operated',
    icon: Building2,
    color: 'bg-slate-700 text-white',
  },
  authentic: {
    label: 'Authentic',
    icon: Shield,
    color: 'bg-emerald-700 text-white',
  },
  benefit: { label: 'Benefit', icon: Gift, color: 'bg-rose-700 text-white' },
  stable: { label: 'Stable', icon: Award, color: 'bg-violet-700 text-white' },
  distill: {
    label: 'Distill',
    icon: FlaskConical,
    color: 'bg-indigo-700 text-white',
  },
} as const

export function GroupCatalogBadges({ values }: { values?: readonly string[] }) {
  const { t } = useTranslation()
  return [...new Set(values)].map((key) => {
    if (!Object.hasOwn(badges, key)) return null
    const meta = badges[key as keyof typeof badges]
    return (
      <span
        key={key}
        className={cn(
          'inline-flex shrink-0 items-center gap-0.5 rounded px-1 py-px text-xs',
          meta.color
        )}
      >
        <meta.icon className='size-3' aria-hidden />
        {t(meta.label)}
      </span>
    )
  })
}
