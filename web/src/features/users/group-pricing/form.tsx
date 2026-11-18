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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { FieldDescription, FieldError, FieldGroup } from '@/components/ui/field'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  getServerErrorStatus,
} from '@/lib/server-error-message'

import { updateUserGroupPricing, userGroupPricingKey } from './api'
import { GroupPricingConflictComparison } from './conflict-comparison'
import {
  groupPricingFormDefaults,
  groupPricingFormSchema,
  type GroupPricingFormValues,
} from './lib/schema'
import { GroupPricingRulesEditor } from './rules-editor'
import type { UserGroupPricing, UserGroupPricingUpdate } from './types'

export function UserGroupPricingForm(props: {
  config: UserGroupPricing
  canUpdate: boolean
  refreshing: boolean
  onReload: () => Promise<UserGroupPricing>
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const submitting = useRef(false)
  const [baseline, setBaseline] = useState(props.config)
  const [latest, setLatest] = useState<UserGroupPricing | null>(null)
  const [conflict, setConflict] = useState(false)
  const [reloading, setReloading] = useState(false)
  const [freePricing, setFreePricing] = useState<UserGroupPricingUpdate | null>(
    null
  )
  const form = useForm<GroupPricingFormValues>({
    resolver: zodResolver(groupPricingFormSchema),
    defaultValues: groupPricingFormDefaults(props.config.ratios),
  })
  const rules = useWatch({ control: form.control, name: 'rules' })

  useEffect(() => {
    if (props.config.revision <= baseline.revision) return
    if (form.formState.isDirty) {
      setLatest(props.config)
      setConflict(true)
      form.setError('root', {
        message: t(
          'Personal group pricing changed. Reload and compare before saving.'
        ),
      })
      return
    }
    setBaseline(props.config)
    form.reset(groupPricingFormDefaults(props.config.ratios))
  }, [props.config, baseline.revision, form, form.formState.isDirty, t])

  const mutation = useMutation({
    mutationFn: (payload: UserGroupPricingUpdate) =>
      updateUserGroupPricing(props.config.user_id, payload),
    retry: false,
    onSuccess: (config) => {
      setBaseline(config)
      setLatest(null)
      setConflict(false)
      form.reset(groupPricingFormDefaults(config.ratios))
      client.setQueryData(userGroupPricingKey(config.user_id), config)
      void client.invalidateQueries({ queryKey: ['pricing'] })
      void client.invalidateQueries({
        queryKey: ['user-groups', config.user_id],
      })
      toast.success(t('Personal group pricing saved'))
    },
    onError: (error) => {
      const changed = getServerErrorStatus(error) === 409
      if (changed) setConflict(true)
      const message = changed
        ? t('Personal group pricing changed. Reload and compare before saving.')
        : getServerErrorMessage(
            error,
            t('Failed to save personal group pricing')
          )
      form.setError('root', { message })
      handleServerError(error, message)
    },
    onSettled: () => {
      submitting.current = false
      setFreePricing(null)
    },
  })
  const busy = mutation.isPending || reloading || props.refreshing
  const disabled = !props.canUpdate || busy

  function submit(values: GroupPricingFormValues): void {
    if (disabled || submitting.current || conflict) return
    for (const [index, rule] of values.rules.entries()) {
      if (
        !props.config.groups.some(
          (group) => group.name === rule.group && group.name !== 'auto'
        )
      ) {
        form.setError(`rules.${index}.group`, {
          message: t(
            'This billing group no longer exists. Remove the rule or choose another group.'
          ),
        })
        return
      }
    }
    const payload = {
      expected_revision: baseline.revision,
      ratios: Object.fromEntries(
        values.rules.map((rule) => [rule.group, Number(rule.ratio)])
      ),
    }
    const createsFreePricing = Object.entries(payload.ratios).some(
      ([group, ratio]) => ratio === 0 && baseline.ratios[group] !== 0
    )
    if (createsFreePricing) {
      setFreePricing(payload)
      return
    }
    submitting.current = true
    mutation.mutate(payload)
  }

  async function reload(): Promise<void> {
    if (submitting.current || busy) return
    setReloading(true)
    try {
      const config = await props.onReload()
      if (form.formState.isDirty || conflict) {
        setLatest(config)
      } else {
        setBaseline(config)
        form.reset(groupPricingFormDefaults(config.ratios))
      }
    } catch (error) {
      const message = getServerErrorMessage(
        error,
        t('Failed to load personal group pricing')
      )
      form.setError('root', { message })
      handleServerError(error, message)
    } finally {
      setReloading(false)
    }
  }

  function useLatest(keepDraft: boolean): void {
    if (!latest || busy) return
    const draft = form.getValues()
    setBaseline(latest)
    form.reset(groupPricingFormDefaults(latest.ratios))
    if (keepDraft) form.reset(draft, { keepDefaultValues: true })
    setLatest(null)
    setConflict(false)
    form.clearErrors('root')
  }

  return (
    <FieldGroup
      onKeyDown={(event) => {
        // This section is inside the account form but has its own save action.
        if (event.key === 'Enter' && event.target instanceof HTMLInputElement) {
          event.preventDefault()
        }
      }}
    >
      <GroupPricingRulesEditor
        form={form}
        groups={props.config.groups}
        disabled={disabled}
        canUpdate={props.canUpdate}
      />
      <FieldDescription>
        {t(
          '0 means free billing. Remove a rule to inherit default group pricing. Changes are saved separately from account details.'
        )}
      </FieldDescription>
      {!props.canUpdate && (
        <FieldDescription>
          {t('You can view these settings but cannot change them.')}
        </FieldDescription>
      )}
      <FieldError>
        {conflict
          ? t(
              'Personal group pricing changed. Reload and compare before saving.'
            )
          : form.formState.errors.root?.message}
      </FieldError>
      {latest && (
        <GroupPricingConflictComparison
          latest={latest}
          draft={rules}
          disabled={busy}
          onUseLatest={useLatest}
        />
      )}
      <div className='flex flex-wrap gap-2'>
        {props.canUpdate && (
          <>
            <Button
              type='button'
              disabled={disabled || conflict || !form.formState.isDirty}
              onClick={() => void form.handleSubmit(submit)()}
            >
              {mutation.isPending ? t('Saving...') : t('Save personal pricing')}
            </Button>
            <Button
              type='button'
              variant='ghost'
              disabled={disabled || rules.length === 0}
              onClick={() =>
                form.reset({ rules: [] }, { keepDefaultValues: true })
              }
            >
              {t('Restore all default pricing')}
            </Button>
          </>
        )}
        <Button
          type='button'
          variant='outline'
          disabled={busy}
          onClick={() => void reload()}
        >
          {reloading ? t('Loading...') : t('Reload and compare')}
        </Button>
      </div>
      <ConfirmDialog
        open={freePricing !== null}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setFreePricing(null)
        }}
        title={t('Enable free group billing?')}
        desc={t(
          'A multiplier of 0 makes new requests in these billing groups free for this user: {{groups}}.',
          {
            groups: Object.entries(freePricing?.ratios ?? {})
              .filter(
                ([group, ratio]) => ratio === 0 && baseline.ratios[group] !== 0
              )
              .map(([group]) => group)
              .join(', '),
          }
        )}
        confirmText={t('Confirm free billing')}
        isLoading={mutation.isPending}
        disabled={disabled || conflict}
        handleConfirm={() => {
          if (!freePricing || disabled || submitting.current || conflict) return
          submitting.current = true
          mutation.mutate(freePricing)
        }}
      />
    </FieldGroup>
  )
}
