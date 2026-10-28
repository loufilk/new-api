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
import { Link, useLocation } from '@tanstack/react-router'
import { BookOpen, Braces } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

import { docsGroupIcons, docsGroups, getGuide } from '../content/nav'

function normalizePathname(pathname: string): string {
  return pathname.endsWith('/') && pathname !== '/'
    ? pathname.slice(0, -1)
    : pathname
}

export function DocsSidebar({ onNavigate }: { onNavigate?: () => void }) {
  const { t } = useTranslation()
  const pathname = normalizePathname(
    useLocation({ select: (location) => location.pathname })
  )

  const topLinks = [
    {
      to: '/help',
      icon: BookOpen,
      label: t('Docs home'),
      active: pathname === '/help',
    },
    {
      to: '/help/api',
      icon: Braces,
      label: t('API Reference'),
      active: pathname === '/help/api',
    },
  ]

  return (
    <nav aria-label={t('Docs navigation')} className='space-y-5 text-sm'>
      <div className='space-y-0.5'>
        {topLinks.map((link) => (
          <Link
            key={link.to}
            to={link.to}
            onClick={onNavigate}
            className={cn(
              'flex items-center gap-2 rounded-md px-2 py-1.5 transition-colors',
              link.active
                ? 'bg-muted text-foreground font-medium'
                : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'
            )}
          >
            <link.icon className='size-4 shrink-0' />
            {link.label}
          </Link>
        ))}
      </div>

      {docsGroups.map((group) => {
        const GroupIcon = docsGroupIcons[group.id]
        return (
          <div key={group.id} className='space-y-0.5'>
            <div className='text-muted-foreground/70 flex items-center gap-1.5 px-2 pb-1 text-xs font-medium'>
              {GroupIcon ? <GroupIcon className='size-3.5 shrink-0' /> : null}
              {group.title}
            </div>
            {group.slugs.map((slug) => {
              const guide = getGuide(slug)
              if (!guide) {
                return null
              }
              const active = pathname === `/help/guides/${slug}`
              return (
                <Link
                  key={slug}
                  to='/help/guides/$slug'
                  params={{ slug }}
                  onClick={onNavigate}
                  className={cn(
                    'flex items-center justify-between gap-2 rounded-md px-2 py-1.5 transition-colors',
                    active
                      ? 'bg-muted text-foreground font-medium'
                      : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'
                  )}
                >
                  <span className='truncate'>{guide.title}</span>
                  {guide.recommended ? (
                    <Badge
                      variant='secondary'
                      className='h-4 shrink-0 px-1.5 text-[10px]'
                    >
                      {t('Recommended')}
                    </Badge>
                  ) : null}
                </Link>
              )
            })}
          </div>
        )
      })}
    </nav>
  )
}
