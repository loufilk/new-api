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
import { useEffect, useId, useRef, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getServerErrorMessage,
  getServerErrorStatus,
} from '@/lib/server-error-message'

import { callInterceptionKey, updateCallInterception } from './api'
import {
  callInterceptionSchema,
  type CallInterceptionFormValues,
} from './lib/schema'
import type { UserCallInterception } from './types'

type Props = {
  config: UserCallInterception
  canUpdate: boolean
  refreshing: boolean
  onReload: () => Promise<UserCallInterception>
}

export function CallInterceptionForm(props: Props) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const inputId = useId()
  const client = useQueryClient()
  const revision = useRef(props.config.revision)
  const submitting = useRef(false)
  const [confirmation, setConfirmation] = useState<'save' | 'reload' | null>(
    null
  )
  const [pendingValues, setPendingValues] =
    useState<CallInterceptionFormValues | null>(null)
  const [reloading, setReloading] = useState(false)
  const form = useForm<CallInterceptionFormValues>({
    resolver: zodResolver(callInterceptionSchema),
    defaultValues: { enabled: props.config.enabled, reply: props.config.reply },
  })
  // A save finishing in another mounted view may refresh this query. Adopt newer
  // revisions only while pristine; a draft keeps its original optimistic lease.
  useEffect(() => {
    if (props.config.revision <= revision.current) return
    if (form.formState.isDirty) {
      form.setError('root', {
        message: t(
          'Call interception configuration changed; reload and try again'
        ),
      })
      return
    }
    revision.current = props.config.revision
    form.reset({ enabled: props.config.enabled, reply: props.config.reply })
  }, [props.config, form, form.formState.isDirty, t])

  const mutation = useMutation({
    mutationFn: (values: CallInterceptionFormValues) =>
      updateCallInterception(props.config.user_id, {
        ...values,
        expected_revision: revision.current,
      }),
    onSuccess: (config) => {
      revision.current = config.revision
      form.reset({ enabled: config.enabled, reply: config.reply })
      client.setQueryData(callInterceptionKey(props.config.user_id), config)
      void client.invalidateQueries({
        queryKey: [...callInterceptionKey(props.config.user_id), 'audits'],
      })
      toast.success(t('Call interception settings saved'))
    },
    onError: (error) => {
      const message =
        getServerErrorStatus(error) === 409
          ? t('Call interception configuration changed; reload and try again')
          : getServerErrorMessage(
              error,
              t('Call interception operation failed, please try again later')
            )
      form.setError('root', { message })
      handleServerError(error, message)
    },
    onSettled: () => {
      submitting.current = false
      setConfirmation(null)
    },
  })
  const busy = mutation.isPending || reloading || props.refreshing
  const disabled = !props.canUpdate || busy

  async function reload(): Promise<void> {
    if (submitting.current) return
    submitting.current = true
    setReloading(true)
    try {
      const config = await props.onReload()
      revision.current = config.revision
      form.reset({ enabled: config.enabled, reply: config.reply })
      setConfirmation(null)
    } catch (error) {
      form.setError('root', {
        message: t('Failed to load interception settings'),
      })
      handleServerError(error, t('Failed to load interception settings'))
    } finally {
      submitting.current = false
      setReloading(false)
    }
  }

  function confirm(): void {
    if (confirmation === 'reload') {
      void reload()
      return
    }
    if (submitting.current || !pendingValues || disabled) return
    submitting.current = true
    mutation.mutate(pendingValues)
  }

  return (
    <FieldGroup className='pt-3'>
      <Field orientation='horizontal' data-disabled={disabled}>
        <FieldLabel htmlFor={`${inputId}-enabled`}>
          {t('Enable call interception')}
        </FieldLabel>
        <Controller
          control={form.control}
          name='enabled'
          render={({ field }) => (
            <Switch
              id={`${inputId}-enabled`}
              checked={field.value}
              onCheckedChange={field.onChange}
              disabled={disabled}
            />
          )}
        />
      </Field>
      <Field
        data-invalid={!!form.formState.errors.reply}
        data-disabled={disabled}
      >
        <FieldLabel htmlFor={`${inputId}-reply`}>
          {t('Custom reply')}
        </FieldLabel>
        <Textarea
          id={`${inputId}-reply`}
          {...form.register('reply')}
          rows={6}
          disabled={disabled}
          aria-invalid={!!form.formState.errors.reply}
          aria-describedby={`${inputId}-help ${inputId}-error`}
          className='resize-y whitespace-pre-wrap'
        />
        <FieldDescription id={`${inputId}-help`}>
          {t(
            'Plain text, including multiple lines and Chinese/English. Maximum 16000 UTF-8 bytes.'
          )}
        </FieldDescription>
        <FieldError id={`${inputId}-error`}>
          {form.formState.errors.reply?.message &&
            t(form.formState.errors.reply.message)}
        </FieldError>
      </Field>
      <FieldDescription>
        {t(
          'Saved separately from other user details. Disabling restores normal routing for new requests.'
        )}
      </FieldDescription>
      {!props.canUpdate && (
        <FieldDescription>
          {t('You can view these settings but cannot change them.')}
        </FieldDescription>
      )}
      <FieldDescription>
        {t('Configuration revision: {{revision}}', {
          revision: formatNumber(revision.current, locale),
        })}
      </FieldDescription>
      <FieldError>{form.formState.errors.root?.message}</FieldError>
      <div className='flex flex-wrap gap-2'>
        {props.canUpdate && (
          <Button
            type='button'
            disabled={disabled || !form.formState.isDirty}
            onClick={() => {
              void form.handleSubmit((values) => {
                setPendingValues(values)
                setConfirmation('save')
              })()
            }}
          >
            {mutation.isPending
              ? t('Saving...')
              : t('Save interception settings')}
          </Button>
        )}
        <Button
          type='button'
          variant='outline'
          disabled={busy}
          onClick={() => {
            if (form.formState.isDirty) setConfirmation('reload')
            else void reload()
          }}
        >
          {t('Reload settings')}
        </Button>
      </div>
      <ConfirmDialog
        open={confirmation !== null}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending && !reloading) setConfirmation(null)
        }}
        title={
          confirmation === 'reload'
            ? t('Discard unsaved interception changes?')
            : t('Apply interception settings?')
        }
        desc={
          confirmation === 'reload'
            ? t(
                'Reloading replaces your unsaved reply and switch with the latest saved settings.'
              )
            : t(
                'This changes responses for all valid keys and Playground requests from this user. Other user details are not saved.'
              )
        }
        confirmText={
          confirmation === 'reload' ? t('Reload settings') : t('Apply settings')
        }
        disabled={confirmation === 'save' && disabled}
        handleConfirm={confirm}
        isLoading={mutation.isPending || reloading}
      />
    </FieldGroup>
  )
}
