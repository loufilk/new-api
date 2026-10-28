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
import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Combobox } from '@/components/ui/combobox'

import {
  type GroupRenameField,
  type GroupRenameIssue,
  type GroupRenameResolutions,
  groupRenameResolutionKey,
  isReservedGroupName,
} from './group-rename'

const FIELD_LABELS: Record<GroupRenameField, string> = {
  GroupRatio: 'Group ratios',
  TopupGroupRatio: 'Top-up group ratios',
  UserUsableGroups: 'Selectable groups',
  GroupGroupRatio: 'Inter-group overrides',
  AutoGroups: 'Auto assignment order',
  GroupSpecialUsableGroup: 'Special usable group rules',
}

type GroupRenamePanelProps = {
  issues: GroupRenameIssue[]
  reservedViolations: string[]
  resolutions: GroupRenameResolutions
  onResolutionsChange: (resolutions: GroupRenameResolutions) => void
}

export function GroupRenamePanel(props: GroupRenamePanelProps) {
  const { t } = useTranslation()
  if (props.issues.length === 0 && props.reservedViolations.length === 0) {
    return null
  }

  const updateResolution = (key: string, value: string) => {
    const next = { ...props.resolutions }
    if (value) {
      next[groupRenameResolutionKey(key)] = value
    } else {
      delete next[groupRenameResolutionKey(key)]
    }
    props.onResolutionsChange(next)
  }

  return (
    <section className='space-y-3'>
      <Alert variant='destructive' aria-label={t('Resolve group changes')}>
        <AlertTriangle aria-hidden='true' />
        <AlertTitle>{t('Resolve group changes')}</AlertTitle>
        <AlertDescription>
          {t(
            'These groups were removed from the saved settings. Map each one to its new name before saving.'
          )}
        </AlertDescription>
      </Alert>
      <div className='divide-y'>
        {props.reservedViolations.length > 0 && (
          <p className='text-destructive text-sm'>
            {t('The reserved groups cannot be renamed or removed: {{groups}}', {
              groups: props.reservedViolations.join(', '),
            })}
          </p>
        )}
        {props.issues.map((issue) => (
          <div
            key={`${issue.field}-${issue.key}`}
            className='flex min-w-0 flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between'
          >
            <div className='min-w-0'>
              <p className='font-medium break-words'>
                {t('{{group}} was removed', { group: issue.key })}
              </p>
              <p className='text-muted-foreground text-xs'>
                {t(FIELD_LABELS[issue.field])}
              </p>
            </div>
            <Combobox
              className='w-full max-w-full sm:w-56 sm:shrink-0'
              options={issue.targets
                .filter((target) => !isReservedGroupName(target))
                .map((target) => ({ value: target, label: target }))}
              value={
                props.resolutions[groupRenameResolutionKey(issue.key)] ?? null
              }
              onValueChange={(value) => {
                if (value) updateResolution(issue.key, value)
              }}
              placeholder={t('Select a group')}
              aria-label={t('Resolve group changes')}
            />
          </div>
        ))}
      </div>
    </section>
  )
}
