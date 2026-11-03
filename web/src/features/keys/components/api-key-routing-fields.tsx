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
import { Info } from 'lucide-react'
import { useFormContext } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Switch } from '@/components/ui/switch'
import { useAuthStore } from '@/stores/auth-store'

import type { ApiKeyFormValues } from '../lib/api-key-form'
import {
  ApiKeyGroupCombobox,
  type ApiKeyGroupOption,
} from './api-key-group-combobox'
import { ApiKeyGroupPresets } from './api-key-group-presets'
import { AutoGroupOrderEditor } from './auto-group-order-editor'

export function ApiKeyRoutingFields(props: {
  options: ApiKeyGroupOption[]
  globalGroups: string[]
  maxCount: number
  allowAuto: boolean
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const form = useFormContext<ApiKeyFormValues>()
  const userGroup = useAuthStore((state) => state.auth.user?.group)
  const smart = form.watch('smart_routing')
  const noRetry = form.watch('no_retry_within_group')
  const group = form.watch('group')
  const mode = form.watch('auto_groups_mode')
  const customGroups = form.watch('auto_groups')
  let selectedGroups: string[] = []
  if (group === 'auto') {
    selectedGroups = mode === 'custom' ? customGroups : props.globalGroups
  } else if (group) {
    selectedGroups = [group]
  } else if (userGroup) {
    selectedGroups = [userGroup]
  }
  // Explicit manual selections use concrete group permissions, not the
  // separate permission to inherit the global virtual Auto group.
  const maxCount = props.maxCount

  const setGroups = (groups: string[]) => {
    if (smart || props.disabled) return
    const next = [...groups]
    if (next.length === 1) {
      form.setValue('group', next[0], { shouldDirty: true })
    } else {
      // An empty custom list stays invalid until explicitly filled or reset.
      // Never silently replace it with the account/global default.
      form.setValue('group', 'auto', { shouldDirty: true })
    }
    form.setValue('auto_groups_mode', 'custom', { shouldDirty: true })
    form.setValue('auto_groups', next, {
      shouldDirty: true,
      shouldValidate: true,
    })
    form.setValue('cross_group_retry', next.length > 1, { shouldDirty: true })
  }

  return (
    <>
      <FormField
        control={form.control}
        name='no_retry_within_group'
        render={({ field }) => (
          <FormItem>
            <div className='flex items-center justify-between gap-3'>
              <FormLabel>{t('No retries within a group')}</FormLabel>
              <FormControl>
                <Switch
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  className='data-checked:bg-orange-500'
                />
              </FormControl>
            </div>
            <FormDescription className='text-xs leading-relaxed'>
              {t(
                'Try at most one channel per group. A safe retry may continue to another available group: in your selected order for manual routing, or across authorized groups for smart routing. With one group, a failure returns immediately. This does not change the request timeout.'
              )}
            </FormDescription>
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name='project_label'
        render={({ field }) => (
          <FormItem>
            <FormLabel>
              {t('Project label')}{' '}
              <Info className='size-3.5' aria-hidden='true' />
            </FormLabel>
            <FormControl>
              <Input
                {...field}
                maxLength={200}
                placeholder={t('For example: Support bot, Data team')}
              />
            </FormControl>
            <FormMessage />
          </FormItem>
        )}
      />
      <FormField
        control={form.control}
        name='smart_routing'
        render={({ field }) => (
          <FormItem>
            <div className='flex items-center justify-between gap-3'>
              <FormLabel>
                {t('Smart Routing')}{' '}
                <Info className='size-3.5' aria-hidden='true' />
              </FormLabel>
              <FormControl>
                <Switch
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  className='data-checked:bg-orange-500'
                />
              </FormControl>
            </div>
            <FormMessage />
          </FormItem>
        )}
      />
      {smart && (
        <FormField
          control={form.control}
          name='routing_strategy'
          render={({ field }) => (
            <FormItem className='-mt-2'>
              <FormControl>
                <RadioGroup
                  value={field.value}
                  onValueChange={field.onChange}
                  aria-label={t('Routing strategy')}
                  className='grid-cols-2 gap-x-4 gap-y-3'
                >
                  {[
                    {
                      value: 'auto',
                      label: t('Smart Auto'),
                      hint: t('best overall'),
                    },
                    {
                      value: 'success_rate',
                      label: t('Success rate first'),
                      hint: t('most stable'),
                    },
                    {
                      value: 'price',
                      label: t('Price first'),
                      hint: t('lowest cost'),
                    },
                    {
                      value: 'speed',
                      label: t('Speed first'),
                      hint: t('fastest'),
                    },
                  ].map((strategy) => (
                    <Label
                      key={strategy.value}
                      className='flex cursor-pointer items-center gap-2 text-xs font-normal'
                    >
                      <RadioGroupItem
                        value={strategy.value}
                        className='size-3.5 data-checked:border-orange-500 data-checked:bg-orange-500'
                      />
                      <span>{`${strategy.label} (${strategy.hint})`}</span>
                    </Label>
                  ))}
                </RadioGroup>
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
      )}
      <p
        role='status'
        className='rounded-md border border-red-200 bg-red-50 px-3 py-2.5 text-xs leading-relaxed font-semibold text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300'
      >
        {smart
          ? t(
              'Smart routing is active — the system chooses across every group you can access, and the group settings below are ignored.'
            )
          : t(
              'Smart routing is off — requests are routed by the groups you select below.'
            )}
      </p>
      <p className='text-muted-foreground text-xs leading-relaxed'>
        {t(
          'Routing selects a group and channel for the model in your API request. It does not replace the requested model.'
        )}
      </p>
      <fieldset
        disabled={smart || props.disabled}
        aria-label={t('Manual routing groups')}
        className='flex min-w-0 flex-col gap-3 border-0 p-0 disabled:opacity-40'
      >
        <FormField
          control={form.control}
          name='group'
          render={() => (
            <FormItem>
              <FormLabel>
                {t('Group')} <Info className='size-3.5' aria-hidden='true' />
              </FormLabel>
              <ApiKeyGroupPresets
                groups={selectedGroups}
                options={props.options}
                maxCount={maxCount}
                disabled={smart || props.disabled}
                onApply={setGroups}
              />
              <FormControl>
                <ApiKeyGroupCombobox
                  options={props.options}
                  value={group}
                  onValueChange={(value) => setGroups([value])}
                  values={selectedGroups}
                  onValuesChange={setGroups}
                  maxCount={maxCount}
                  disabled={smart || props.disabled}
                  placeholder={t('Select groups in routing order')}
                />
              </FormControl>
              <FormDescription className='text-xs'>
                {t(
                  'Select multiple authorized groups. Drag or use the arrow buttons to set the order from top to bottom.'
                )}
              </FormDescription>
              {selectedGroups.length > 0 && (
                <AutoGroupOrderEditor
                  selectionOnly
                  aria-label={t('Manual routing groups')}
                  value={selectedGroups}
                  mode='custom'
                  options={props.options}
                  globalOptions={[]}
                  maxCount={maxCount}
                  onChange={({ groups }) => setGroups(groups)}
                  className='min-w-0'
                />
              )}
              <FormMessage />
            </FormItem>
          )}
        />
        {group === 'auto' && mode === 'custom' && (
          <FormField
            control={form.control}
            name='auto_groups'
            render={() => (
              <FormItem>
                <FormMessage />
              </FormItem>
            )}
          />
        )}
        <div className='flex flex-wrap items-center gap-2'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            className='text-muted-foreground h-6 px-1 text-xs'
            disabled={smart || props.disabled || !group}
            onClick={() =>
              form.setValue('group', '', {
                shouldDirty: true,
                shouldValidate: true,
              })
            }
          >
            {t('Use my group')}
          </Button>
          {props.allowAuto && (
            <Button
              type='button'
              variant='ghost'
              size='sm'
              className='text-muted-foreground h-6 px-1 text-xs'
              disabled={
                smart ||
                props.disabled ||
                (group === 'auto' && mode === 'inherit')
              }
              onClick={() => {
                form.setValue('group', 'auto', { shouldDirty: true })
                form.setValue('auto_groups_mode', 'inherit', {
                  shouldDirty: true,
                })
                form.setValue('auto_groups', [], {
                  shouldDirty: true,
                  shouldValidate: true,
                })
                form.setValue('cross_group_retry', true, { shouldDirty: true })
              }}
            >
              {t('Restore global Auto')}
            </Button>
          )}
        </div>
        {group === 'auto' && mode === 'inherit' && (
          <p className='text-muted-foreground text-xs'>
            {t('Using the complete global Auto order ({{count}} groups)', {
              count: props.globalGroups.length,
            })}
          </p>
        )}
        {group === 'auto' && !noRetry && (
          <FormField
            control={form.control}
            name='cross_group_retry'
            render={({ field }) => (
              <FormItem className='flex items-center justify-between gap-3'>
                <div>
                  <FormLabel className='text-xs'>
                    {t('Cross-group retry')}
                  </FormLabel>
                  <FormDescription className='text-xs'>
                    {t(
                      'When enabled, if channels in the current group fail, it will try channels in the next group in order.'
                    )}
                  </FormDescription>
                </div>
                <FormControl>
                  <Switch
                    checked={!!field.value}
                    onCheckedChange={field.onChange}
                    disabled={smart || props.disabled}
                  />
                </FormControl>
              </FormItem>
            )}
          />
        )}
      </fieldset>
    </>
  )
}
