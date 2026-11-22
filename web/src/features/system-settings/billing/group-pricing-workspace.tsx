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
import {
  ChevronRight,
  Layers3,
  SlidersHorizontal,
  UsersRound,
} from 'lucide-react'
import {
  useId,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from 'react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

type WorkspaceSection = 'groups' | 'people' | 'advanced'
type RatioPanel = 'pricing' | 'advanced'
type CatalogPanel = 'groups' | 'people'

type WorkspaceLocation = {
  section: WorkspaceSection
  ratio: RatioPanel
  catalog: CatalogPanel
}

type GroupPricingWorkspaceProps = {
  renderRatio: (panel: RatioPanel, active: boolean) => ReactNode
  renderCatalog: (panel: CatalogPanel) => ReactNode
}

export function GroupPricingWorkspace(props: GroupPricingWorkspaceProps) {
  const { t } = useTranslation()
  const regionId = useId()
  const headingId = `${regionId}-heading`
  const buttons = useRef(new Map<WorkspaceSection, HTMLButtonElement>())
  const [location, setLocation] = useState<WorkspaceLocation>({
    section: 'groups',
    ratio: 'pricing',
    catalog: 'groups',
  })

  const sections = [
    {
      id: 'groups',
      label: t('Groups'),
      description: t(
        'Manage group rates, access, and marketplace presentation.'
      ),
      icon: Layers3,
    },
    {
      id: 'people',
      label: t('Personal Pricing and Access'),
      description: t(
        'Manage exclusive access and rates for a specific account.'
      ),
      icon: UsersRound,
    },
    {
      id: 'advanced',
      label: t('Advanced Rules'),
      description: t(
        'Manage group overrides, visibility rules, and legacy Auto order.'
      ),
      icon: SlidersHorizontal,
    },
  ] as const
  const selected =
    sections.find((section) => section.id === location.section) ?? sections[0]
  const ratioActive = location.section !== 'people'
  const catalogActive = location.section !== 'advanced'

  function selectSection(section: WorkspaceSection) {
    setLocation((previous) => {
      if (section === previous.section) return previous
      if (section === 'people') {
        return { ...previous, section, catalog: 'people' }
      }
      if (section === 'advanced') {
        return { ...previous, section, ratio: 'advanced' }
      }
      return { section, ratio: 'pricing', catalog: 'groups' }
    })
  }

  function moveFocus(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return
    let next: number
    switch (event.key) {
      case 'ArrowDown':
      case 'ArrowRight':
        next = (index + 1) % sections.length
        break
      case 'ArrowUp':
      case 'ArrowLeft':
        next = (index + sections.length - 1) % sections.length
        break
      case 'Home':
        next = 0
        break
      case 'End':
        next = sections.length - 1
        break
      default:
        return
    }
    event.preventDefault()
    buttons.current.get(sections[next].id)?.focus()
  }

  return (
    <div className='@container min-w-0'>
      <div className='grid min-w-0 gap-4 @min-[760px]:grid-cols-[13.5rem_minmax(0,1fr)] @min-[760px]:items-start @min-[760px]:gap-7'>
        <nav
          aria-label={t('Group pricing sections')}
          className='border-border/70 bg-muted/35 flex min-w-0 flex-col gap-1 rounded-2xl border p-1.5 @min-[760px]:p-2'
        >
          {sections.map((section, index) => {
            const isCurrent = section.id === location.section
            const Icon = section.icon
            return (
              <button
                key={section.id}
                ref={(node) => {
                  if (node) buttons.current.set(section.id, node)
                  else buttons.current.delete(section.id)
                }}
                type='button'
                aria-current={isCurrent ? 'true' : undefined}
                aria-controls={regionId}
                onClick={() => selectSection(section.id)}
                onKeyDown={(event) => moveFocus(event, index)}
                className={cn(
                  'focus-visible:ring-ring relative flex min-h-11 w-full min-w-0 cursor-pointer items-center gap-2.5 rounded-xl px-3 py-2 text-start text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:outline-none motion-reduce:transition-none',
                  isCurrent
                    ? 'bg-background text-foreground shadow-xs ring-1 ring-black/5 dark:ring-white/10'
                    : 'text-muted-foreground hover:bg-background/60 hover:text-foreground'
                )}
              >
                <Icon
                  aria-hidden='true'
                  className={cn('size-4 shrink-0', isCurrent && 'text-primary')}
                />
                <span className='min-w-0 flex-1 break-words'>
                  {section.label}
                </span>
                {isCurrent && (
                  <ChevronRight
                    aria-hidden='true'
                    className='text-primary size-3.5 shrink-0 rtl:rotate-180'
                  />
                )}
              </button>
            )
          })}
        </nav>
        <section
          id={regionId}
          aria-labelledby={headingId}
          className='min-w-0 space-y-5'
        >
          <header className='space-y-1'>
            <h2
              id={headingId}
              className='text-foreground text-xl font-semibold tracking-tight'
            >
              {selected.label}
            </h2>
            <p className='text-muted-foreground text-sm leading-relaxed'>
              {selected.description}
            </p>
          </header>
          <div
            hidden={!ratioActive}
            className={cn('min-w-0', !ratioActive && 'hidden')}
          >
            {props.renderRatio(location.ratio, ratioActive)}
          </div>
          <div
            hidden={!catalogActive}
            className={cn('min-w-0', !catalogActive && 'hidden')}
          >
            {props.renderCatalog(location.catalog)}
          </div>
        </section>
      </div>
    </div>
  )
}
