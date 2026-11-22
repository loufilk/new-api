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
import { ChevronDown, Menu } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { PublicLayout } from '@/components/layout'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { cn } from '@/lib/utils'

import { DocsSidebar } from './docs-sidebar'

/** 文档中心统一外壳：桌面端左侧粘性侧栏，移动端折叠导航 */
export function DocsShell({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const [mobileNavOpen, setMobileNavOpen] = useState(false)

  return (
    <PublicLayout showMainContainer={false}>
      <div className='mx-auto w-full max-w-7xl px-4 pt-22 pb-16 sm:px-6 lg:px-8'>
        <Collapsible
          open={mobileNavOpen}
          onOpenChange={setMobileNavOpen}
          className='mb-6 lg:hidden'
        >
          <CollapsibleTrigger
            className={cn(
              'border-border bg-card text-foreground flex w-full items-center justify-between rounded-lg border px-3 py-2 text-sm font-medium'
            )}
          >
            <span className='flex items-center gap-2'>
              <Menu className='size-4' />
              {t('Docs navigation')}
            </span>
            <ChevronDown
              className={cn(
                'size-4 transition-transform',
                mobileNavOpen && 'rotate-180'
              )}
            />
          </CollapsibleTrigger>
          <CollapsibleContent>
            <div className='border-border bg-card mt-2 rounded-lg border p-3'>
              <DocsSidebar onNavigate={() => setMobileNavOpen(false)} />
            </div>
          </CollapsibleContent>
        </Collapsible>

        <div className='lg:grid lg:grid-cols-[240px_minmax(0,1fr)] lg:gap-10'>
          <aside className='hidden lg:block'>
            <div className='sticky top-20 max-h-[calc(100vh-6rem)] overflow-y-auto pr-2 pb-6'>
              <DocsSidebar />
            </div>
          </aside>
          <main id='content' className='min-w-0'>
            {children}
          </main>
        </div>
      </div>
    </PublicLayout>
  )
}
