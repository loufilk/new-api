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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatDateTimeObject } from '@/lib/time'

import { ModelCapabilityBadge } from './capability-badge'
import { latestModelTestRuns } from './timeline'
import type { TestRun } from './types'

export function QualityHistoryDialog(props: {
  group: string
  model: string
  runs: TestRun[]
  onSelect: (run: TestRun) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [source, setSource] = useState('all')
  const runs = latestModelTestRuns(props.runs)
    .reverse()
    .filter((run) => source === 'all' || (run.source ?? 'user') === source)
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Quality result history')}
      description={`${props.group} · ${props.model}`}
      contentClassName='sm:max-w-3xl'
      bodyClassName='space-y-4'
    >
      <Tabs value={source} onValueChange={(value) => setSource(String(value))}>
        <TabsList>
          <TabsTrigger value='all'>{t('All results')}</TabsTrigger>
          <TabsTrigger value='site'>{t('Site tests')}</TabsTrigger>
          <TabsTrigger value='user'>{t('User tests')}</TabsTrigger>
        </TabsList>
      </Tabs>
      {runs.length === 0 && (
        <EmptyState title={t('No quality results yet')} className='min-h-32' />
      )}
      <div className='max-h-[55vh] space-y-2 overflow-y-auto'>
        {runs.map((run) => (
          <Button
            key={run.id}
            variant='outline'
            className='h-auto w-full flex-wrap justify-between gap-3 p-4 text-left'
            onClick={() => {
              props.onClose()
              props.onSelect(run)
            }}
          >
            <span className='space-y-1'>
              <time
                className='block text-sm'
                dateTime={new Date(run.created_at * 1000).toISOString()}
              >
                {formatDateTimeObject(new Date(run.created_at * 1000))}
              </time>
              <span className='text-muted-foreground block text-xs'>
                {run.source === 'site' ? t('Site tests') : t('User tests')}
              </span>
            </span>
            <span className='flex flex-wrap items-center gap-2'>
              <ModelCapabilityBadge run={run} />
              <Badge variant='outline'>
                {t(`intelligence.status.${run.status}`)}
              </Badge>
            </span>
          </Button>
        ))}
      </div>
    </Dialog>
  )
}
