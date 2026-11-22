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
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { SettingsSwitchField } from '../components/settings-form-layout'
import type { ModelTestCatalogue, ModelTestTarget } from './model-test-settings'

export function ModelTestTargetEditor(props: {
  target: ModelTestTarget
  catalogue: ModelTestCatalogue
  disabled: boolean
  onChange: (target: ModelTestTarget) => void
  onRemove: () => void
}) {
  const { t } = useTranslation()
  const target = props.target
  const models = [
    ...(props.catalogue.models_by_group[target.group] ?? []),
  ].sort(
    (left, right) => Number(/gpt/i.test(right)) - Number(/gpt/i.test(left))
  )
  return (
    <fieldset
      disabled={props.disabled}
      className='min-w-0 space-y-3 rounded-lg border p-4'
    >
      <legend className='px-1 text-sm'>{t('Monitoring target')}</legend>
      <div className='grid min-w-0 gap-4 sm:grid-cols-2'>
        <div className='min-w-0 space-y-2'>
          <Label htmlFor={`${target.id}-group`}>{t('Group')}</Label>
          <NativeSelect
            id={`${target.id}-group`}
            className='w-full'
            value={target.group}
            onChange={(event) =>
              props.onChange({
                ...target,
                group: event.target.value,
                model: '',
                channel_id: 0,
              })
            }
          >
            <NativeSelectOption value=''>
              {t('Select group')}
            </NativeSelectOption>
            {props.catalogue.groups.map((group) => (
              <NativeSelectOption key={group} value={group}>
                {group}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className='min-w-0 space-y-2'>
          <Label htmlFor={`${target.id}-model`}>{t('Model')}</Label>
          <NativeSelect
            id={`${target.id}-model`}
            className='w-full'
            value={target.model}
            onChange={(event) =>
              props.onChange({
                ...target,
                model: event.target.value,
                channel_id: 0,
              })
            }
          >
            <NativeSelectOption value=''>
              {t('Select model')}
            </NativeSelectOption>
            {models.map((model) => (
              <NativeSelectOption key={model} value={model}>
                {model}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className='space-y-2'>
          <Label htmlFor={`${target.id}-period`}>
            {t('Target interval (seconds, 0 inherits)')}
          </Label>
          <Input
            id={`${target.id}-period`}
            type='number'
            min={0}
            step={1}
            value={target.period_secs}
            onChange={(event) =>
              props.onChange({
                ...target,
                period_secs: Number(event.target.value),
              })
            }
          />
        </div>
      </div>
      <SettingsSwitchField
        controlId={`${target.id}-enabled`}
        label={t('Enable target')}
        checked={target.enabled}
        disabled={props.disabled}
        onCheckedChange={(enabled) => props.onChange({ ...target, enabled })}
      />
      <Button type='button' variant='outline' onClick={props.onRemove}>
        {t('Remove target')}
      </Button>
    </fieldset>
  )
}
