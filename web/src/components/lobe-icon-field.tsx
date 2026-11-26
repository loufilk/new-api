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
import { useState, type AriaAttributes } from 'react'
import { useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { CatalogModelIcon } from '@/components/catalog-model-icon'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  catalogIconID,
  catalogModelIconID,
  getLobeIconNames,
} from '@/lib/catalog-icon-registry'

const iconOptions = getLobeIconNames().map((name) => ({
  value: name,
  label: name,
  // Loading an Avatar/Combine preview imports the optional UI toolkit. Only
  // load those heavier variants after selection, not for each search result.
  icon:
    !name.includes('.') || name.endsWith('.Color') ? (
      <CatalogModelIcon icon={name} size={18} />
    ) : undefined,
}))

export function LobeIconField(props: {
  id?: string
  value: string
  onChange: (value: string) => void
  allowInheritance?: boolean
  inheritedIcon?: string
  inheritedName?: string
  'aria-describedby'?: string
  'aria-invalid'?: AriaAttributes['aria-invalid']
}) {
  const { t } = useTranslation()
  // Both admin metadata forms use this control. Subscribe only the preview,
  // rather than forcing the whole drawer to render on each name keystroke.
  const recordName = useWatch<{ model_name: string; name: string }>({
    name: props.allowInheritance ? 'model_name' : 'name',
  })
  const [choosing, setChoosing] = useState(false)
  const usesCustom = !props.allowInheritance || Boolean(props.value) || choosing
  const own = catalogIconID(props.value)
  const vendorName = props.allowInheritance ? props.inheritedName : recordName
  const inherited = catalogModelIconID({
    vendorIcon: props.inheritedIcon,
    vendorName,
  })
  const effective = own || inherited
  const clearOverride = () => {
    setChoosing(false)
    props.onChange('')
  }
  return (
    <div className='space-y-3'>
      {props.allowInheritance && (
        <div className='flex flex-wrap gap-2'>
          <Button
            type='button'
            size='sm'
            variant={!usesCustom ? 'secondary' : 'outline'}
            aria-pressed={!usesCustom}
            onClick={clearOverride}
          >
            {t('Inherit vendor icon')}
          </Button>
          <Button
            id={usesCustom ? undefined : props.id}
            aria-label={t('Custom model icon')}
            type='button'
            size='sm'
            variant={usesCustom ? 'secondary' : 'outline'}
            aria-pressed={usesCustom}
            onClick={() => setChoosing(true)}
          >
            {t('Custom model icon')}
          </Button>
        </div>
      )}
      {usesCustom && (
        <div className='flex min-w-0 items-center gap-2'>
          <Combobox
            id={props.id}
            options={iconOptions}
            value={own}
            onValueChange={(value) => props.onChange(value ?? '')}
            searchPlaceholder={t('Search installed icons')}
            emptyText={t('No matching icons')}
            className='min-w-0 flex-1'
            aria-describedby={props['aria-describedby']}
            aria-invalid={props['aria-invalid']}
            limit={30}
          />
          {props.value && (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              onClick={clearOverride}
            >
              {t('Clear')}
            </Button>
          )}
        </div>
      )}
      <div
        aria-live='polite'
        className='bg-muted/40 flex items-center gap-3 rounded-lg border p-3'
      >
        <span className='bg-muted/50 flex size-10 shrink-0 items-center justify-center rounded-lg'>
          <CatalogModelIcon
            icon={props.value}
            vendorIcon={props.inheritedIcon}
            vendorName={vendorName}
            name={recordName}
            size={28}
          />
        </span>
        <div className='min-w-0 text-xs'>
          <p className='font-medium'>{t('Effective icon')}</p>
          <p className='text-muted-foreground break-all'>
            {effective || t('Default placeholder')}
          </p>
          {props.allowInheritance &&
            !own &&
            inherited &&
            props.inheritedName && (
              <p className='text-muted-foreground'>
                {t('Inherited from {{vendor}}', {
                  vendor: props.inheritedName,
                })}
              </p>
            )}
          {props.value && !own && (
            <p className='text-warning'>
              {t('Choose an installed icon or clear the saved key.')}
            </p>
          )}
        </div>
      </div>
    </div>
  )
}
