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
  Store,
  ShieldCheck,
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

type WorkspaceSection = 'groups' | 'base' | 'access' | 'personal' | 'advanced'
type RatioPanel = 'pricing' | 'advanced'
export type CatalogPanel = 'groups' | 'access' | 'personal'

type GroupPricingWorkspaceProps = {
  renderRatio: (panel: RatioPanel, active: boolean) => ReactNode
  renderCatalog: (panel: CatalogPanel) => ReactNode
}

export function GroupPricingWorkspace(props: GroupPricingWorkspaceProps) {
  const { t } = useTranslation()
  const regionId = useId()
  const headingId = `${regionId}-heading`
  const buttons = useRef(new Map<WorkspaceSection, HTMLButtonElement>())
  const [sectionId, setSectionId] = useState<WorkspaceSection>('groups')
  const [visited, setVisited] = useState<ReadonlySet<WorkspaceSection>>(
    () => new Set(['groups'])
  )
  const [catalogPanel, setCatalogPanel] = useState<CatalogPanel>('groups')

  const sections = [
    {
      id: 'groups',
      label: t('Marketplace display'),
      description: t(
        'Set the descriptions, badges and order customers see. Badges do not affect pricing or routing.'
      ),
      icon: Store,
    },
    {
      id: 'base',
      label: t('Base group rates'),
      description: t(
        'Base group ratios apply to calls billed to this group. Top-up ratios and availability are in Advanced.'
      ),
      icon: Layers3,
    },
    {
      id: 'access',
      label: t('Exclusive group access'),
      description: t(
        'This only controls group access. Revoking access does not remove personal rate rules.'
      ),
      icon: ShieldCheck,
    },
    {
      id: 'personal',
      label: t('Personal pricing'),
      description: t(
        'Override the rate for this account and group only. This does not grant access or change past charges.'
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
    sections.find((section) => section.id === sectionId) ?? sections[0]
  const catalogActive = sectionId !== 'base' && sectionId !== 'advanced'

  function selectSection(section: WorkspaceSection) {
    setSectionId(section)
    setVisited((previous) =>
      previous.has(section) ? previous : new Set([...previous, section])
    )
    if (section !== 'base' && section !== 'advanced') setCatalogPanel(section)
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
            const isCurrent = section.id === sectionId
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
          {/* Independent forms prevent Advanced drafts from being submitted by a base-rate save. */}
          {(['base', 'advanced'] as const).map((section) =>
            visited.has(section) ? (
              <div
                key={section}
                hidden={sectionId !== section}
                className={cn('min-w-0', sectionId !== section && 'hidden')}
              >
                {props.renderRatio(
                  section === 'base' ? 'pricing' : 'advanced',
                  section === sectionId
                )}
              </div>
            ) : null
          )}
          <div
            hidden={!catalogActive}
            className={cn('min-w-0', !catalogActive && 'hidden')}
          >
            {props.renderCatalog(catalogPanel)}
          </div>
        </section>
      </div>
    </div>
  )
}
