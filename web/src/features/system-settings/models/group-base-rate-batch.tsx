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

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'

type BatchGroup = { id: string; ratio: number }

export function GroupBaseRateBatch(props: {
  groups: BatchGroup[]
  disabled: boolean
  onStage: (ids: string[], rate: string) => void
}) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [rate, setRate] = useState('')
  const [selected, setSelected] = useState<ReadonlySet<string>>(() => new Set())
  const nextRate = Number(rate)
  const valid =
    Boolean(rate.trim()) && Number.isFinite(nextRate) && nextRate >= 0
  const changed = props.groups.filter(
    (group) => selected.has(group.id) && valid && nextRate !== group.ratio
  )

  function toggle(open: boolean) {
    setOpen(open)
    if (open) {
      setRate('')
      setSelected(new Set())
    }
  }

  return (
    <Dialog
      open={open && !props.disabled}
      onOpenChange={toggle}
      title={t('Batch edit base rates')}
      description={t(
        'Select the groups to change. Applying adds only those changes to your draft. Use Save group settings to save them.'
      )}
      trigger={
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={props.disabled || props.groups.length === 0}
          title={
            props.disabled
              ? t('Save or discard pending rate edits before starting a batch.')
              : undefined
          }
        >
          {t('Batch edit base rates')}
        </Button>
      }
      footer={
        <>
          <Button type='button' variant='outline' onClick={() => toggle(false)}>
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            disabled={!changed.length || props.disabled}
            onClick={() => {
              if (!changed.length || props.disabled) return
              props.onStage(
                changed.map((group) => group.id),
                String(nextRate)
              )
              toggle(false)
            }}
          >
            {t('Apply to draft')}
          </Button>
        </>
      }
      bodyClassName='space-y-4'
    >
      <label className='block max-w-xs space-y-1 text-sm font-medium'>
        <span>{t('New base rate')}</span>
        <Input
          type='number'
          min={0}
          step='any'
          value={rate}
          onChange={(event) => setRate(event.target.value)}
          aria-invalid={rate !== '' && !valid}
        />
      </label>
      <StaticDataTable
        data={props.groups}
        getRowKey={(group) => group.id}
        columns={[
          {
            id: 'select',
            header: t('Select'),
            className: 'w-16',
            cell: (group) => (
              <Checkbox
                aria-label={t('Select group {{id}}', { id: group.id })}
                checked={selected.has(group.id)}
                onCheckedChange={(checked) =>
                  setSelected((previous) => {
                    const next = new Set(previous)
                    if (checked) next.add(group.id)
                    else next.delete(group.id)
                    return next
                  })
                }
              />
            ),
          },
          {
            id: 'id',
            header: t('Group name'),
            cellClassName: 'whitespace-normal break-all',
            cell: (group) => group.id,
          },
          {
            id: 'preview',
            header: t('Base group rate'),
            cellClassName: 'whitespace-normal break-all tabular-nums',
            cell: (group) =>
              selected.has(group.id) && valid
                ? `${group.ratio}× → ${nextRate}×`
                : `${group.ratio}×`,
          },
        ]}
      />
    </Dialog>
  )
}
