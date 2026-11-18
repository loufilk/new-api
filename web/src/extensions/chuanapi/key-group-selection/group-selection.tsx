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
import type { ComponentProps } from 'react'

import { ApiKeyGroupCombobox } from '@/features/keys/components/api-key-group-combobox'

import { GroupSelectionPanel } from './group-selection-panel'

type ApiKeyGroupSelectionProps = Omit<
  ComponentProps<typeof ApiKeyGroupCombobox>,
  'value' | 'onValueChange' | 'values' | 'onValuesChange' | 'selectionPanel'
> & {
  values: string[]
  onChange: (values: string[]) => void
}

export function ApiKeyGroupSelection(props: ApiKeyGroupSelectionProps) {
  return (
    <ApiKeyGroupCombobox
      id={props.id}
      aria-label={props['aria-label']}
      aria-describedby={props['aria-describedby']}
      aria-invalid={props['aria-invalid']}
      aria-required={props['aria-required']}
      options={props.options}
      values={props.values}
      onValueChange={(value) => props.onChange([value])}
      onValuesChange={props.onChange}
      maxCount={props.maxCount}
      disabled={props.disabled}
      placeholder={props.placeholder}
      selectionPanel={(onSelect) => (
        <GroupSelectionPanel
          options={props.options}
          values={props.values}
          onChange={props.onChange}
          onSelect={onSelect}
          maxCount={props.maxCount ?? 5}
          disabled={props.disabled}
        />
      )}
    />
  )
}
