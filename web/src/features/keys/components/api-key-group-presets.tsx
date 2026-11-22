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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useAuthStore } from '@/stores/auth-store'

import { useKeyGroupPresetStore } from '../stores/group-presets'
import type { ApiKeyGroupOption } from './api-key-group-combobox'

export function ApiKeyGroupPresets(props: {
  groups: string[]
  options: ApiKeyGroupOption[]
  maxCount: number
  disabled?: boolean
  onApply: (groups: string[]) => void
}) {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id ?? null)
  const store = useKeyGroupPresetStore()
  const loadPresets = useKeyGroupPresetStore((state) => state.load)
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState('')
  useEffect(() => {
    loadPresets(userId)
  }, [userId, loadPresets])
  useEffect(() => {
    setEditing(false)
    setName('')
  }, [userId, props.disabled])
  const presets =
    store.ownerId === userId && userId !== null ? store.presets : []
  const allowedGroups = new Set(props.options.map((option) => option.value))
  const canSave =
    userId !== null &&
    props.groups.length > 0 &&
    props.groups.length <= props.maxCount &&
    props.groups.every((group) => allowedGroups.has(group)) &&
    !props.disabled
  return (
    <>
      <div className='flex min-w-0 gap-2'>
        <Select
          value={null}
          disabled={props.disabled || presets.length === 0}
          onValueChange={(value) => {
            if (props.disabled || value === null) return
            const preset = presets.find((item) => item.name === value)
            if (!preset) return
            const allowed = new Set(props.options.map((item) => item.value))
            if (
              preset.groups.length > props.maxCount ||
              preset.groups.some((group) => !allowed.has(group))
            ) {
              toast.error(
                t(
                  'This preset contains unavailable groups. Select current groups and save it again.'
                )
              )
              return
            }
            props.onApply([...preset.groups])
          }}
        >
          <SelectTrigger
            aria-label={t('Group preset')}
            className='min-w-0 flex-1 text-xs'
          >
            <SelectValue
              placeholder={
                presets.length === 0
                  ? t('No presets yet — select groups, then save')
                  : t('Select a group preset')
              }
            />
          </SelectTrigger>
          <SelectContent>
            {presets.map((preset) => (
              <SelectItem key={preset.name} value={preset.name}>
                {preset.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          type='button'
          variant='outline'
          size='sm'
          className='shrink-0 text-xs'
          disabled={!canSave}
          onClick={() => setEditing(true)}
        >
          {t('Save as preset')}
        </Button>
      </div>
      <Dialog
        open={editing && !props.disabled}
        onOpenChange={setEditing}
        title={t('Save group preset')}
        description={t(
          'Presets are saved only in this browser for your account. They contain group names, never API keys.'
        )}
        contentClassName='sm:max-w-md'
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => setEditing(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              disabled={!canSave || !name.trim()}
              onClick={() => {
                if (!canSave || userId === null || !name.trim()) return
                if (
                  !store.save(userId, {
                    name: name.trim(),
                    groups: [...props.groups],
                  })
                ) {
                  toast.error(
                    t(
                      'Could not save the preset. Browser storage may be unavailable or the preset limit reached.'
                    )
                  )
                  return
                }
                setEditing(false)
                setName('')
                toast.success(t('Group preset saved'))
              }}
            >
              {t('Save')}
            </Button>
          </>
        }
      >
        <div className='grid gap-2'>
          <Label htmlFor='key-group-preset-name'>{t('Preset name')}</Label>
          <Input
            id='key-group-preset-name'
            value={name}
            maxLength={50}
            onChange={(event) => setName(event.target.value)}
          />
        </div>
      </Dialog>
    </>
  )
}
